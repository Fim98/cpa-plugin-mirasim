package mirasim

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
)

const (
	sessionPath       = "/v1/device/session"
	modelsPath        = "/v1/models"
	accessRefreshLead = 2 * time.Minute
	ticketRefreshLead = time.Minute
	maxErrorBody      = 1 << 20
	maxErrorMessage   = 4 << 10
	quotaSource       = "GET /v1/models response headers"
)

var quotaHeaderNames = []string{
	"anthropic-ratelimit-unified-5h-utilization",
	"anthropic-ratelimit-unified-5h-reset",
	"anthropic-ratelimit-unified-7d-utilization",
	"anthropic-ratelimit-unified-7d-reset",
}

type Pool struct {
	mu      sync.Mutex
	clients map[string]*Client
}

func NewPool() *Pool {
	return &Pool{clients: make(map[string]*Client)}
}

func (p *Pool) Client(storage credentials.Storage) *Client {
	if p == nil {
		return NewClient(storage)
	}
	key := storage.Key()
	p.mu.Lock()
	defer p.mu.Unlock()
	if client := p.clients[key]; client != nil {
		return client
	}
	client := NewClient(storage)
	p.clients[key] = client
	return client
}

// Forget drops a cached client after its on-disk OAuth material is replaced.
// Existing in-flight requests keep their own client; future requests reload the
// newly installed tokens and key.
func (p *Pool) Forget(storage credentials.Storage) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.clients, storage.Key())
	p.mu.Unlock()
}

type Client struct {
	storage credentials.Storage

	mu              sync.Mutex
	loaded          bool
	accessToken     string
	refreshToken    string
	accessExpiresAt time.Time
	privateKey      ed25519.PrivateKey
	publicKeyBase64 string
	deviceID        string
	sessionID       string
	ticket          string
	ticketExpiresAt time.Time
	quota           QuotaSnapshot
	authProxyURL    string
}

func NewClient(storage credentials.Storage) *Client {
	return &Client{storage: storage}
}

func (c *Client) Storage() credentials.Storage {
	return c.storage
}

func (c *Client) Validate() error {
	if errFiles := c.storage.ValidateFiles(); errFiles != nil {
		return errFiles
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if errLoad := c.loadLocked(); errLoad != nil {
		return errLoad
	}
	if errSigner := c.loadSignerLocked(); errSigner != nil {
		return errSigner
	}
	_, errSealKey := relaySealPublicKey()
	return errSealKey
}

func (c *Client) NextRefreshAfter(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if errLoad := c.loadLocked(); errLoad != nil {
		return now
	}
	if c.accessExpiresAt.IsZero() {
		return now
	}
	next := c.accessExpiresAt.Add(-accessRefreshLead)
	if next.Before(now) {
		return now
	}
	return next
}

// RefreshAccess refreshes the access token through a private client. The
// refresh token is deliberately not sent through the host request logger.
func (c *Client) RefreshAccess(ctx context.Context) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if errLoad := c.loadLocked(); errLoad != nil {
		return time.Time{}, errLoad
	}
	if errRefresh := c.refreshAccessLocked(ctx); errRefresh != nil {
		return time.Time{}, errRefresh
	}
	c.ticket = ""
	c.ticketExpiresAt = time.Time{}
	return c.accessExpiresAt, nil
}

// SetAuthProxy configures the private token-refresh client without persisting
// proxy credentials in the provider auth JSON. Relay calls still use the host
// HTTP client; refresh calls deliberately avoid it because their body contains
// the long-lived refresh token.
func (c *Client) SetAuthProxy(proxyURL string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if _, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL); errBuild != nil {
		return fmt.Errorf("configure Mirasim auth proxy: %w", errBuild)
	}
	c.mu.Lock()
	c.authProxyURL = proxyURL
	c.mu.Unlock()
	return nil
}

// RefreshAccessWithProxy remembers the host proxy for both scheduled and
// request-time refreshes, then refreshes through the private client.
func (c *Client) RefreshAccessWithProxy(ctx context.Context, proxyURL string) (time.Time, error) {
	if errProxy := c.SetAuthProxy(proxyURL); errProxy != nil {
		return time.Time{}, errProxy
	}
	return c.RefreshAccess(ctx)
}

func (c *Client) Do(ctx context.Context, client pluginapi.HostHTTPClient, method, requestPath string, query url.Values, headers http.Header, body []byte) (pluginapi.HTTPResponse, error) {
	if client == nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("host HTTP client is required")
	}
	endpoint, signaturePath, errURL := c.endpoint(requestPath, query)
	if errURL != nil {
		return pluginapi.HTTPResponse{}, errURL
	}
	for attempt := 0; attempt < 2; attempt++ {
		authHeaders, errAuth := c.authHeaders(ctx, client, method, signaturePath, body, attempt > 0)
		if errAuth != nil {
			return pluginapi.HTTPResponse{}, errAuth
		}
		outboundHeaders := prepareHeaders(headers, authHeaders, false)
		resp, errDo := client.Do(ctx, pluginapi.HTTPRequest{
			Method:  method,
			URL:     endpoint,
			Headers: outboundHeaders,
			Body:    append([]byte(nil), body...),
		})
		if errDo != nil {
			return pluginapi.HTTPResponse{}, errDo
		}
		c.observeQuota(resp.Headers)
		if resp.StatusCode != http.StatusUnauthorized || attempt == 1 {
			return resp, nil
		}
	}
	return pluginapi.HTTPResponse{}, fmt.Errorf("Mirasim request retry exhausted")
}

func (c *Client) DoStream(ctx context.Context, client pluginapi.HostHTTPClient, method, requestPath string, query url.Values, headers http.Header, body []byte) (pluginapi.HTTPStreamResponse, error) {
	if client == nil {
		return pluginapi.HTTPStreamResponse{}, fmt.Errorf("host HTTP client is required")
	}
	endpoint, signaturePath, errURL := c.endpoint(requestPath, query)
	if errURL != nil {
		return pluginapi.HTTPStreamResponse{}, errURL
	}
	for attempt := 0; attempt < 2; attempt++ {
		authHeaders, errAuth := c.authHeaders(ctx, client, method, signaturePath, body, attempt > 0)
		if errAuth != nil {
			return pluginapi.HTTPStreamResponse{}, errAuth
		}
		outboundHeaders := prepareHeaders(headers, authHeaders, true)
		resp, errDo := client.DoStream(ctx, pluginapi.HTTPRequest{
			Method:  method,
			URL:     endpoint,
			Headers: outboundHeaders,
			Body:    append([]byte(nil), body...),
		})
		if errDo != nil {
			return pluginapi.HTTPStreamResponse{}, errDo
		}
		c.observeQuota(resp.Headers)
		if resp.StatusCode != http.StatusUnauthorized || attempt == 1 {
			return resp, nil
		}
		drainStream(ctx, resp.Chunks, maxErrorBody)
	}
	return pluginapi.HTTPStreamResponse{}, fmt.Errorf("Mirasim stream retry exhausted")
}

func (c *Client) ListModels(ctx context.Context, client pluginapi.HostHTTPClient) (Catalog, error) {
	resp, errDo := c.Do(ctx, client, http.MethodGet, modelsPath, nil, http.Header{
		"Accept": []string{"application/json"},
	}, nil)
	if errDo != nil {
		return Catalog{}, errDo
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Catalog{}, NewStatusError(resp.StatusCode, resp.Body, resp.Headers)
	}
	models, errParse := ParseModelCatalog(resp.Body)
	if errParse != nil {
		return Catalog{}, errParse
	}
	quota, available := QuotaFromHeaders(resp.Headers, time.Now())
	if !available {
		quota = QuotaSnapshot{
			Available:  false,
			Source:     quotaSource,
			ObservedAt: time.Now().UTC(),
			Headers:    make(map[string]string),
		}
	}
	return Catalog{Models: models, Quota: quota}, nil
}

func (c *Client) LastQuota() QuotaSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quota.Clone()
}

func (c *Client) authHeaders(ctx context.Context, client pluginapi.HostHTTPClient, method, requestPath string, body []byte, forceTicket bool) (http.Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if errLoad := c.loadLocked(); errLoad != nil {
		return nil, errLoad
	}
	if errSigner := c.loadSignerLocked(); errSigner != nil {
		return nil, errSigner
	}
	if forceTicket {
		c.ticket = ""
		c.ticketExpiresAt = time.Time{}
	}
	ticket, errTicket := c.ticketLocked(ctx, client)
	if errTicket != nil {
		return nil, errTicket
	}
	metadata, errMetadata := c.relayMetadataLocked(requestPath)
	if errMetadata != nil {
		return nil, errMetadata
	}
	headers, errSign := c.signatureHeadersLocked(method, requestPath, ticket, metadata, body)
	if errSign != nil {
		return nil, errSign
	}
	headers.Set("Authorization", "Bearer "+ticket)
	if errSeal := sealRelayHeaders(headers, method, requestPath); errSeal != nil {
		return nil, errSeal
	}
	return headers, nil
}

func (c *Client) ticketLocked(ctx context.Context, client pluginapi.HostHTTPClient) (string, error) {
	now := time.Now()
	if c.ticket != "" && now.Before(c.ticketExpiresAt.Add(-ticketRefreshLead)) {
		return c.ticket, nil
	}
	if errToken := c.ensureAccessTokenLocked(ctx); errToken != nil {
		return "", errToken
	}
	for attempt := 0; attempt < 2; attempt++ {
		body, errMarshal := json.Marshal(struct {
			PublicKey string `json:"publicKey"`
			DeviceID  string `json:"deviceId"`
		}{PublicKey: c.publicKeyBase64, DeviceID: c.deviceID})
		if errMarshal != nil {
			return "", errMarshal
		}
		signed, errSign := c.signatureHeadersLocked(http.MethodPost, sessionPath, c.accessToken, nil, body)
		if errSign != nil {
			return "", errSign
		}
		signed.Set("Authorization", "Bearer "+c.accessToken)
		signed.Set("Content-Type", "application/json")
		endpoint, _, errURL := c.endpoint(sessionPath, nil)
		if errURL != nil {
			return "", errURL
		}
		resp, errDo := client.Do(ctx, pluginapi.HTTPRequest{Method: http.MethodPost, URL: endpoint, Headers: signed, Body: body})
		if errDo != nil {
			return "", fmt.Errorf("mint Mirasim device ticket: %w", errDo)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if errRefresh := c.refreshAccessLocked(ctx); errRefresh != nil {
				return "", errRefresh
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", NewStatusError(resp.StatusCode, resp.Body, resp.Headers)
		}
		var payload struct {
			Ticket    string `json:"ticket"`
			ExpiresIn int64  `json:"expiresIn"`
		}
		if errDecode := json.Unmarshal(resp.Body, &payload); errDecode != nil {
			return "", fmt.Errorf("decode Mirasim device ticket: %w", errDecode)
		}
		payload.Ticket = strings.TrimSpace(payload.Ticket)
		if payload.Ticket == "" {
			return "", fmt.Errorf("Mirasim device ticket response is missing ticket")
		}
		if payload.ExpiresIn <= 0 {
			payload.ExpiresIn = 900
		}
		c.ticket = payload.Ticket
		c.ticketExpiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)
		return c.ticket, nil
	}
	return "", fmt.Errorf("mint Mirasim device ticket retry exhausted")
}

func (c *Client) ensureAccessTokenLocked(ctx context.Context) error {
	now := time.Now()
	if c.accessToken != "" && now.Before(c.accessExpiresAt.Add(-accessRefreshLead)) {
		return nil
	}
	return c.refreshAccessLocked(ctx)
}

func (c *Client) refreshAccessLocked(ctx context.Context) error {
	// Another process may rotate the project-local refresh token. Always prefer
	// the latest complete value on disk before using the cached copy.
	latest, errLatest := c.readCredentialLocked("refresh-token.txt", true)
	if errLatest != nil {
		return errLatest
	}
	c.refreshToken = latest
	if strings.TrimSpace(c.refreshToken) == "" {
		return fmt.Errorf("Mirasim refresh token is missing")
	}
	body, errMarshal := json.Marshal(map[string]string{"refresh_token": c.refreshToken})
	if errMarshal != nil {
		return errMarshal
	}
	request, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, c.storage.AdminURL+"/auth/refresh", bytes.NewReader(body))
	if errRequest != nil {
		return fmt.Errorf("create Mirasim token refresh request: %w", errRequest)
	}
	request.Header.Set("Content-Type", "application/json")
	transport, _, errTransport := proxyutil.BuildHTTPTransport(c.authProxyURL)
	if errTransport != nil {
		return fmt.Errorf("configure Mirasim auth proxy: %w", errTransport)
	}
	authHTTPClient := &http.Client{Timeout: 60 * time.Second}
	if transport != nil {
		authHTTPClient.Transport = transport
		defer transport.CloseIdleConnections()
	}
	resp, errDo := authHTTPClient.Do(request)
	if errDo != nil {
		return fmt.Errorf("refresh Mirasim access token: %w", errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, errRead := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody+1))
	if errRead != nil {
		return fmt.Errorf("read Mirasim token refresh response: %w", errRead)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The refresh endpoint receives a long-lived secret in its request body.
		// Do not risk reflecting an upstream response body into host logs.
		return fmt.Errorf("Mirasim token refresh returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if errDecode := json.Unmarshal(responseBody, &payload); errDecode != nil {
		return fmt.Errorf("decode Mirasim token refresh response: %w", errDecode)
	}
	payload.AccessToken = strings.TrimSpace(payload.AccessToken)
	payload.RefreshToken = strings.TrimSpace(payload.RefreshToken)
	if payload.AccessToken == "" {
		return fmt.Errorf("Mirasim token refresh response is missing access_token")
	}
	if errWrite := c.writeCredentialLocked("access-token.txt", payload.AccessToken); errWrite != nil {
		return errWrite
	}
	c.accessToken = payload.AccessToken
	c.accessExpiresAt = jwtExpiry(payload.AccessToken)
	if payload.RefreshToken != "" {
		if errWrite := c.writeCredentialLocked("refresh-token.txt", payload.RefreshToken); errWrite != nil {
			return errWrite
		}
		c.refreshToken = payload.RefreshToken
	}
	return nil
}

func (c *Client) loadLocked() error {
	if c.loaded {
		return nil
	}
	refreshToken, errRefresh := c.readCredentialLocked("refresh-token.txt", true)
	if errRefresh != nil {
		return errRefresh
	}
	accessToken, errAccess := c.readCredentialLocked("access-token.txt", false)
	if errAccess != nil {
		return errAccess
	}
	c.refreshToken = refreshToken
	c.accessToken = accessToken
	c.accessExpiresAt = jwtExpiry(accessToken)
	c.loaded = true
	return nil
}

func (c *Client) loadSignerLocked() error {
	if len(c.privateKey) != 0 {
		return nil
	}
	raw, errRead := c.readCredentialLocked("device-private-key.pem", true)
	if errRead != nil {
		return errRead
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return fmt.Errorf("decode Mirasim device private key PEM")
	}
	parsed, errParse := x509.ParsePKCS8PrivateKey(block.Bytes)
	if errParse != nil {
		return fmt.Errorf("parse Mirasim device private key: %w", errParse)
	}
	privateKey, okKey := parsed.(ed25519.PrivateKey)
	if !okKey {
		return fmt.Errorf("Mirasim device private key is not Ed25519")
	}
	publicDER, errPublic := x509.MarshalPKIXPublicKey(privateKey.Public())
	if errPublic != nil {
		return fmt.Errorf("marshal Mirasim device public key: %w", errPublic)
	}
	publicBase64 := base64.StdEncoding.EncodeToString(publicDER)
	digest := sha256.Sum256([]byte(publicBase64))
	c.privateKey = append(ed25519.PrivateKey(nil), privateKey...)
	c.publicKeyBase64 = publicBase64
	c.deviceID = base64.RawURLEncoding.EncodeToString(digest[:])[:22]
	return nil
}

func (c *Client) readCredentialLocked(name string, required bool) (string, error) {
	path := filepath.Join(c.storage.CredentialDir, name)
	raw, errRead := os.ReadFile(path)
	if errRead != nil {
		if !required && os.IsNotExist(errRead) {
			return "", nil
		}
		return "", fmt.Errorf("read Mirasim credential %s: %w", path, errRead)
	}
	value := strings.TrimSpace(string(raw))
	if required && value == "" {
		return "", fmt.Errorf("Mirasim credential is empty: %s", path)
	}
	return value, nil
}

func (c *Client) writeCredentialLocked(name, value string) error {
	path := filepath.Join(c.storage.CredentialDir, name)
	file, errOpen := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if errOpen != nil {
		return fmt.Errorf("write Mirasim credential %s: %w", path, errOpen)
	}
	_, errWrite := io.WriteString(file, strings.TrimSpace(value)+"\n")
	errClose := file.Close()
	if errWrite != nil {
		return fmt.Errorf("write Mirasim credential %s: %w", path, errWrite)
	}
	if errClose != nil {
		return fmt.Errorf("close Mirasim credential %s: %w", path, errClose)
	}
	if errChmod := os.Chmod(path, 0o600); errChmod != nil && os.PathSeparator != '\\' {
		return fmt.Errorf("set Mirasim credential permissions %s: %w", path, errChmod)
	}
	return nil
}

func (c *Client) endpoint(requestPath string, query url.Values) (string, string, error) {
	base, errParse := url.Parse(c.storage.RelayURL)
	if errParse != nil || base.Scheme == "" || base.Host == "" {
		return "", "", fmt.Errorf("invalid Mirasim relay URL %q", c.storage.RelayURL)
	}
	requestPath = "/" + strings.TrimLeft(strings.TrimSpace(requestPath), "/")
	base.Path = strings.TrimRight(base.Path, "/") + requestPath
	base.RawPath = ""
	base.RawQuery = query.Encode()
	return base.String(), base.Path, nil
}

func (c *Client) observeQuota(headers http.Header) {
	snapshot, ok := QuotaFromHeaders(headers, time.Now())
	if !ok {
		return
	}
	c.mu.Lock()
	c.quota = snapshot
	c.mu.Unlock()
}

func prepareHeaders(source, auth http.Header, stream bool) http.Header {
	headers := cloneHeader(source)
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-mirasim-") {
			headers.Del(name)
		}
	}
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "X-Api-Key", "Host", "Content-Length",
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		headers.Del(name)
	}
	for key, values := range auth {
		headers[key] = append([]string(nil), values...)
	}
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}
	if stream {
		headers.Set("Accept", "text/event-stream")
	} else if headers.Get("Accept") == "" {
		headers.Set("Accept", "application/json")
	}
	return headers
}

func cloneHeader(source http.Header) http.Header {
	if source == nil {
		return make(http.Header)
	}
	out := make(http.Header, len(source))
	for key, values := range source {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func drainStream(ctx context.Context, chunks <-chan pluginapi.HTTPStreamChunk, limit int) []byte {
	if chunks == nil {
		return nil
	}
	body := make([]byte, 0)
	for {
		select {
		case <-ctx.Done():
			return body
		case chunk, ok := <-chunks:
			if !ok {
				return body
			}
			if len(chunk.Payload) > 0 && len(body) < limit {
				remaining := limit - len(body)
				if len(chunk.Payload) > remaining {
					body = append(body, chunk.Payload[:remaining]...)
				} else {
					body = append(body, chunk.Payload...)
				}
			}
			if chunk.Err != nil {
				return body
			}
		}
	}
}

func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}
	}
	payload, errDecode := base64.RawURLEncoding.DecodeString(parts[1])
	if errDecode != nil {
		return time.Time{}
	}
	var claims struct {
		ExpiresAt json.Number `json:"exp"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if errJSON := decoder.Decode(&claims); errJSON != nil {
		return time.Time{}
	}
	seconds, errNumber := claims.ExpiresAt.Int64()
	if errNumber != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

type Catalog struct {
	Models []RemoteModel
	Quota  QuotaSnapshot
}

type RemoteModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func ParseModelCatalog(raw []byte) ([]RemoteModel, error) {
	var payload struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if errDecode := json.Unmarshal(raw, &payload); errDecode != nil {
		return nil, fmt.Errorf("decode Mirasim model catalog: %w", errDecode)
	}
	items := payload.Data
	if len(items) == 0 {
		items = payload.Models
	}
	models := make([]RemoteModel, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		var model RemoteModel
		if errObject := json.Unmarshal(item, &model); errObject != nil || strings.TrimSpace(model.ID) == "" {
			var id string
			if errString := json.Unmarshal(item, &id); errString != nil {
				continue
			}
			model.ID = id
		}
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" {
			continue
		}
		if _, exists := seen[model.ID]; exists {
			continue
		}
		seen[model.ID] = struct{}{}
		if model.Object == "" {
			model.Object = "model"
		}
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Mirasim model catalog contains no models")
	}
	return models, nil
}

type QuotaSnapshot struct {
	Available  bool              `json:"available"`
	Source     string            `json:"source"`
	ObservedAt time.Time         `json:"observed_at"`
	Headers    map[string]string `json:"headers"`
	FiveHour   QuotaWindow       `json:"five_hour"`
	SevenDay   QuotaWindow       `json:"seven_day"`
}

type QuotaWindow struct {
	Utilization string     `json:"utilization,omitempty"`
	Reset       string     `json:"reset,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
}

func QuotaFromHeaders(headers http.Header, observedAt time.Time) (QuotaSnapshot, bool) {
	values := make(map[string]string, len(quotaHeaderNames))
	for _, name := range quotaHeaderNames {
		if value := safeHeaderValue(headers.Get(name)); value != "" {
			values[name] = value
		}
	}
	if len(values) == 0 {
		return QuotaSnapshot{}, false
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	return QuotaSnapshot{
		Available:  true,
		Source:     quotaSource,
		ObservedAt: observedAt.UTC(),
		Headers:    values,
		FiveHour: QuotaWindow{
			Utilization: values[quotaHeaderNames[0]],
			Reset:       values[quotaHeaderNames[1]],
			ResetAt:     resetTime(values[quotaHeaderNames[1]]),
		},
		SevenDay: QuotaWindow{
			Utilization: values[quotaHeaderNames[2]],
			Reset:       values[quotaHeaderNames[3]],
			ResetAt:     resetTime(values[quotaHeaderNames[3]]),
		},
	}, true
}

func (q QuotaSnapshot) Clone() QuotaSnapshot {
	clone := q
	if q.Headers != nil {
		clone.Headers = make(map[string]string, len(q.Headers))
		for key, value := range q.Headers {
			clone.Headers[key] = value
		}
	}
	return clone
}

func safeHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 {
		return ""
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return ""
		}
	}
	return value
}

func resetTime(value string) *time.Time {
	seconds, errParse := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if errParse != nil || seconds <= 0 {
		return nil
	}
	parsed := time.Unix(seconds, 0).UTC()
	return &parsed
}

type StatusError struct {
	status  int
	body    []byte
	headers http.Header
}

func NewStatusError(status int, body []byte, headers http.Header) *StatusError {
	if len(body) > maxErrorBody {
		body = body[:maxErrorBody]
	}
	return &StatusError{status: status, body: append([]byte(nil), body...), headers: cloneHeader(headers)}
}

func (e *StatusError) Error() string {
	if e == nil {
		return "Mirasim upstream request failed"
	}
	message := strings.TrimSpace(string(e.body))
	if message == "" {
		message = http.StatusText(e.status)
	}
	if len(message) > maxErrorMessage {
		message = message[:maxErrorMessage] + "..."
	}
	return fmt.Sprintf("Mirasim upstream returned HTTP %d: %s", e.status, message)
}

func (e *StatusError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func (e *StatusError) Headers() http.Header {
	if e == nil {
		return nil
	}
	return cloneHeader(e.headers)
}
