package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
)

const (
	OAuthStartResource    = "/oauth/start"
	OAuthCallbackResource = "/oauth/callback"
	oauthLoginTTL         = 3 * time.Minute
	maxOAuthSessions      = 64
	maxOAuthCredentialLen = 64 << 10
)

type oauthSession struct {
	proxyURL     string
	state        string
	callbackURL  string
	expiresAt    time.Time
	accessToken  string
	refreshToken string
	callbackDone bool
	finalizing   bool
	errorMessage string
	auth         *pluginapi.AuthData
}

type oauthCoordinator struct {
	mu               sync.Mutex
	resourceBasePath string
	sessions         map[string]*oauthSession
	now              func() time.Time
}

func newOAuthCoordinator() *oauthCoordinator {
	return &oauthCoordinator{sessions: make(map[string]*oauthSession), now: time.Now}
}

// ConfigureOAuthResourceBasePath receives the concrete plugin resource prefix
// from CPA management registration. Keeping this host-assigned value avoids
// guessing the plugin ID or patching CPA's built-in OAuth callback.
func (p *Provider) ConfigureOAuthResourceBasePath(value string) {
	value = "/" + strings.Trim(strings.TrimSpace(value), "/")
	if value == "/" || strings.Contains(value, "..") {
		value = ""
	}
	p.oauth.mu.Lock()
	p.oauth.resourceBasePath = value
	p.oauth.mu.Unlock()
}

func (p *Provider) StartLogin(_ context.Context, req pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	if provider := strings.TrimSpace(req.Provider); provider != "" && !strings.EqualFold(provider, credentials.Provider) {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("unsupported OAuth provider %q", provider)
	}
	base, errBase := publicOAuthBase(p.settings.OAuthPublicBaseURL, req.BaseURL)
	if errBase != nil {
		return pluginapi.AuthLoginStartResponse{}, errBase
	}

	state, errState := randomOAuthValue(32)
	if errState != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("generate Mirasim OAuth state: %w", errState)
	}
	now := p.oauth.now()
	expiresAt := now.Add(oauthLoginTTL)

	p.oauth.mu.Lock()
	p.oauth.purgeLocked(now)
	resourceBasePath := p.oauth.resourceBasePath
	if resourceBasePath == "" {
		p.oauth.mu.Unlock()
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("Mirasim OAuth resources are not registered")
	}
	if len(p.oauth.sessions) >= maxOAuthSessions {
		p.oauth.mu.Unlock()
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("too many pending Mirasim OAuth sessions")
	}
	callbackURL := joinPublicURL(base, resourceBasePath+OAuthCallbackResource)
	p.oauth.sessions[state] = &oauthSession{state: state, callbackURL: callbackURL, expiresAt: expiresAt, proxyURL: req.Host.ProxyURL}
	p.oauth.mu.Unlock()

	startURL := joinPublicURL(base, resourceBasePath+OAuthStartResource)
	parsedStart, _ := url.Parse(startURL)
	query := parsedStart.Query()
	query.Set("state", state)
	parsedStart.RawQuery = query.Encode()
	return pluginapi.AuthLoginStartResponse{
		Provider:  credentials.Provider,
		URL:       parsedStart.String(),
		State:     state,
		ExpiresAt: expiresAt,
		Metadata: map[string]any{
			"flow":       "browser_oauth",
			"expires_at": expiresAt.UTC().Format(time.RFC3339),
		},
	}, nil
}

func (p *Provider) PollLogin(ctx context.Context, req pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	state := strings.TrimSpace(req.State)
	now := p.oauth.now()

	p.oauth.mu.Lock()
	p.oauth.purgeLocked(now)
	session := p.oauth.sessions[state]
	if session == nil || !constantTimeEqual(session.state, state) {
		p.oauth.mu.Unlock()
		return oauthPollError("unknown or expired Mirasim OAuth state"), nil
	}
	if session.auth != nil {
		auth := cloneAuthData(*session.auth)
		p.oauth.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "Mirasim OAuth login completed", Auth: auth, Auths: []pluginapi.AuthData{auth}}, nil
	}
	if session.errorMessage != "" {
		message := session.errorMessage
		p.oauth.mu.Unlock()
		return oauthPollError(message), nil
	}
	if !session.callbackDone || session.finalizing {
		p.oauth.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "Waiting for Mirasim OAuth callback"}, nil
	}
	accessToken, refreshToken := session.accessToken, session.refreshToken
	session.accessToken = ""
	session.refreshToken = ""
	session.finalizing = true
	p.oauth.mu.Unlock()

	storage, errStorage := p.finalizeOAuthStorage(ctx, p.settings, accessToken, refreshToken, req.Host.ProxyURL, req.HTTPClient)
	accessToken, refreshToken = "", ""

	p.oauth.mu.Lock()
	defer p.oauth.mu.Unlock()
	session = p.oauth.sessions[state]
	if session == nil {
		return oauthPollError("Mirasim OAuth session expired while credentials were being installed"), nil
	}
	session.finalizing = false
	if errStorage != nil {
		session.errorMessage = errStorage.Error()
		return oauthPollError(session.errorMessage), nil
	}
	fileName := storage.DefaultAuthFileName()
	auth := storage.AuthData(fileName, fileName, p.pool.Client(storage).NextRefreshAfter(now))
	session.auth = &auth
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "Mirasim OAuth login completed", Auth: auth, Auths: []pluginapi.AuthData{auth}}, nil
}

// HandleOAuthResource serves only the two browser-facing resources registered
// by the management capability. Tokens are accepted from the Mirasim callback
// into bounded process memory and are never reflected into the response.
func (p *Provider) HandleOAuthResource(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	switch {
	case strings.HasSuffix(req.Path, OAuthStartResource):
		return p.handleOAuthStart(ctx, req), nil
	case strings.HasSuffix(req.Path, OAuthCallbackResource):
		return p.handleOAuthCallback(req), nil
	default:
		return htmlResponse(http.StatusNotFound, "Mirasim OAuth", "OAuth resource not found."), nil
	}
}

func (p *Provider) handleOAuthStart(ctx context.Context, req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	session, ok := p.oauth.getPending(req.Query.Get("state"))
	if !ok {
		return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "This login link is invalid or has expired. Start the login again from Management Center.")
	}
	providers, errDiscovery := discoverLoginProviders(ctx, p.settings.AdminURL, session.proxyURL)
	if errDiscovery != nil {
		return htmlResponse(http.StatusServiceUnavailable, "Mirasim OAuth", errDiscovery.Error())
	}
	if len(providers) == 0 {
		return htmlResponse(http.StatusServiceUnavailable, "Mirasim OAuth", "No sign-in providers are currently enabled.")
	}
	provider := strings.ToLower(strings.TrimSpace(req.Query.Get("provider")))
	if provider == "" {
		return renderProviderPage(session.state, providers)
	}
	if !providerOffered(providers, provider) {
		return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "Unsupported Mirasim sign-in provider.")
	}
	authURL, errURL := buildMirasimOAuthURL(p.settings.AdminURL, provider, session.callbackURL, session.state)
	if errURL != nil {
		return htmlResponse(http.StatusInternalServerError, "Mirasim OAuth", "Could not build the Mirasim sign-in URL.")
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusFound,
		Headers:    browserHeaders(http.Header{"Location": []string{authURL}}),
	}
}

func (p *Provider) handleOAuthCallback(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	state := strings.TrimSpace(req.Query.Get("state"))
	p.oauth.mu.Lock()
	defer p.oauth.mu.Unlock()
	now := p.oauth.now()
	p.oauth.purgeLocked(now)
	session := p.oauth.sessions[state]
	if session == nil || !constantTimeEqual(session.state, state) {
		return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "This login callback is invalid or has expired.")
	}
	if strings.EqualFold(strings.TrimSpace(req.Query.Get("result")), "complete") && session.callbackDone {
		if session.errorMessage != "" {
			return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "Mirasim did not complete the sign-in. You may close this tab and try again.")
		}
		return htmlResponse(http.StatusOK, "Mirasim sign-in complete", "Return to Management Center. You may close this tab.")
	}
	if session.callbackDone || session.finalizing || session.auth != nil {
		return htmlResponse(http.StatusConflict, "Mirasim OAuth", "This login callback has already been used.")
	}
	if strings.TrimSpace(req.Query.Get("error")) != "" {
		session.callbackDone = true
		session.errorMessage = "Mirasim OAuth login was cancelled or rejected"
		return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "Mirasim did not complete the sign-in. You may close this tab and try again.")
	}
	accessToken := strings.TrimSpace(req.Query.Get("access_token"))
	if accessToken == "" {
		accessToken = strings.TrimSpace(req.Query.Get("token"))
	}
	refreshToken := strings.TrimSpace(req.Query.Get("refresh_token"))
	if accessToken == "" || refreshToken == "" {
		session.callbackDone = true
		session.errorMessage = "Mirasim OAuth callback did not include renewable credentials"
		return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "Mirasim returned an incomplete sign-in. No credentials were saved.")
	}
	if len(accessToken) > maxOAuthCredentialLen || len(refreshToken) > maxOAuthCredentialLen || strings.ContainsAny(accessToken, "\r\n\x00") || strings.ContainsAny(refreshToken, "\r\n\x00") {
		session.callbackDone = true
		session.errorMessage = "Mirasim OAuth callback contained invalid credentials"
		return htmlResponse(http.StatusBadRequest, "Mirasim OAuth", "Mirasim returned an invalid sign-in. No credentials were saved.")
	}
	session.accessToken = accessToken
	session.refreshToken = refreshToken
	session.callbackDone = true
	cleanLocation := "?result=complete&state=" + url.QueryEscape(state)
	return pluginapi.ManagementResponse{StatusCode: http.StatusSeeOther, Headers: browserHeaders(http.Header{"Location": []string{cleanLocation}})}
}

func (c *oauthCoordinator) getPending(state string) (oauthSession, bool) {
	state = strings.TrimSpace(state)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeLocked(c.now())
	session := c.sessions[state]
	if session == nil || session.callbackDone || session.finalizing || session.auth != nil || !constantTimeEqual(session.state, state) {
		return oauthSession{}, false
	}
	return *session, true
}

func (c *oauthCoordinator) purgeLocked(now time.Time) {
	for state, session := range c.sessions {
		if session == nil || !now.Before(session.expiresAt) {
			if session != nil {
				session.accessToken = ""
				session.refreshToken = ""
			}
			delete(c.sessions, state)
		}
	}
}

func randomOAuthValue(size int) (string, error) {
	raw := make([]byte, size)
	if _, errRead := rand.Read(raw); errRead != nil {
		return "", errRead
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func constantTimeEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func publicOAuthBase(configured, fallback string) (*url.URL, error) {
	raw := strings.TrimSpace(configured)
	keepPath := raw != ""
	if raw == "" {
		raw = strings.TrimSpace(fallback)
	}
	parsed, errParse := url.Parse(raw)
	if errParse != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid Mirasim OAuth public base URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("Mirasim OAuth public base URL must use HTTP or HTTPS")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("Mirasim OAuth public base URL must use HTTPS unless it is loopback")
	}
	if !keepPath {
		parsed.Path = ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func joinPublicURL(base *url.URL, route string) string {
	joined := *base
	joined.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(route, "/")
	joined.RawPath = ""
	joined.RawQuery = ""
	joined.Fragment = ""
	return joined.String()
}

// adminBaseURL validates the configured authentication service origin. Every
// route built against it, not just OAuth login, passes through here.
func adminBaseURL(adminURL string) (*url.URL, error) {
	base, errParse := url.Parse(strings.TrimRight(strings.TrimSpace(adminURL), "/"))
	if errParse != nil || base.Scheme == "" || base.Host == "" || base.User != nil || base.Opaque != "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("invalid Mirasim authentication service URL")
	}
	base.Scheme = strings.ToLower(base.Scheme)
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("invalid Mirasim authentication service URL")
	}
	if base.Scheme == "http" && !isLoopbackHost(base.Hostname()) {
		return nil, fmt.Errorf("Mirasim authentication service URL must use HTTPS unless it is loopback")
	}
	return base, nil
}

func adminEndpoint(adminURL, resource string) (string, error) {
	base, errBase := adminBaseURL(adminURL)
	if errBase != nil {
		return "", errBase
	}
	base.Path = strings.TrimRight(base.Path, "/") + resource
	base.RawPath = ""
	return base.String(), nil
}

func buildMirasimOAuthURL(adminURL, provider, callbackURL, state string) (string, error) {
	base, errBase := adminBaseURL(adminURL)
	if errBase != nil {
		return "", errBase
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/auth/oauth/" + url.PathEscape(provider) + "/login"
	base.RawPath = ""
	query := base.Query()
	query.Set("redirect_uri", callbackURL)
	query.Set("state", state)
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func oauthPollError(message string) pluginapi.AuthLoginPollResponse {
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: message}
}

func cloneAuthData(source pluginapi.AuthData) pluginapi.AuthData {
	out := source
	out.StorageJSON = append([]byte(nil), source.StorageJSON...)
	out.Metadata = make(map[string]any, len(source.Metadata))
	for key, value := range source.Metadata {
		out.Metadata[key] = value
	}
	out.Attributes = make(map[string]string, len(source.Attributes))
	for key, value := range source.Attributes {
		out.Attributes[key] = value
	}
	return out
}

var providerPageTemplate = template.Must(template.New("providers").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign in to Mirasim</title><style>body{font:16px system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1.25rem;color:#171717}h1{font-size:1.6rem}.button{display:block;margin:.75rem 0;padding:.85rem 1rem;border:1px solid #bbb;border-radius:.65rem;color:inherit;text-decoration:none}.button:hover{background:#f4f4f4}p{color:#555}</style></head>
<body><h1>Sign in to Mirasim</h1><p>Choose the account provider used by your Mirasim account.</p>
{{range .Providers}}<a class="button" href="?provider={{.ID}}&amp;state={{$.State}}">Continue with {{.Label}}</a>{{end}}</body></html>`))

var messagePageTemplate = template.Must(template.New("message").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title><style>body{font:16px system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1.25rem;color:#171717}h1{font-size:1.6rem}p{color:#555}</style></head>
<body><h1>{{.Title}}</h1><p>{{.Message}}</p></body></html>`))

func renderProviderPage(state string, providers []loginProvider) pluginapi.ManagementResponse {
	var body bytes.Buffer
	_ = providerPageTemplate.Execute(&body, struct {
		State     string
		Providers []loginProvider
	}{State: state, Providers: providers})
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: browserHeaders(nil), Body: body.Bytes()}
}

func htmlResponse(status int, title, message string) pluginapi.ManagementResponse {
	var body bytes.Buffer
	_ = messagePageTemplate.Execute(&body, struct {
		Title   string
		Message string
	}{Title: title, Message: message})
	return pluginapi.ManagementResponse{StatusCode: status, Headers: browserHeaders(nil), Body: body.Bytes()}
}

func browserHeaders(extra http.Header) http.Header {
	headers := make(http.Header)
	for key, values := range extra {
		headers[key] = append([]string(nil), values...)
	}
	headers.Set("Content-Type", "text/html; charset=utf-8")
	headers.Set("Cache-Control", "no-store")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	return headers
}
