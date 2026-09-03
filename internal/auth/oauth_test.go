package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

func TestManagementOAuthReturnsSelfContainedAuthJSONWithoutExternalWrites(t *testing.T) {
	settings := pluginconfig.Defaults()
	settings.AdminURL = "https://auth.mirasim.example"
	settings.OAuthPublicBaseURL = "https://cpa.example/proxy"
	provider := New(settings, mirasim.NewPool())
	provider.ConfigureOAuthResourceBasePath("/v0/resource/plugins/mirasim-id")
	accessToken := identityJWT("account-123", "user@example.com", time.Now().Add(time.Hour))

	started, errStart := provider.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{Provider: "mirasim", BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback"})
	if errStart != nil {
		t.Fatalf("StartLogin() error = %v", errStart)
	}
	if started.State == "" || started.Provider != "mirasim" || !strings.HasPrefix(started.URL, "https://cpa.example/proxy/v0/resource/plugins/mirasim-id/oauth/start?") {
		t.Fatalf("start response = %#v", started)
	}
	if raw := []byte(toText(started.Metadata)); bytes.Contains(raw, []byte(accessToken)) || bytes.Contains(raw, []byte("refresh-secret")) {
		t.Fatalf("start metadata contains a token: %s", raw)
	}

	chooser, errChooser := provider.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/mirasim-id/oauth/start",
		Query:  url.Values{"state": []string{started.State}},
	})
	if errChooser != nil || chooser.StatusCode != http.StatusOK || !bytes.Contains(chooser.Body, []byte("Continue with GitHub")) {
		t.Fatalf("chooser = %#v, error = %v", chooser, errChooser)
	}

	redirect, errRedirect := provider.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/mirasim-id/oauth/start",
		Query:  url.Values{"state": []string{started.State}, "provider": []string{"github"}},
	})
	if errRedirect != nil || redirect.StatusCode != http.StatusFound {
		t.Fatalf("redirect = %#v, error = %v", redirect, errRedirect)
	}
	location, errParse := url.Parse(redirect.Headers.Get("Location"))
	if errParse != nil {
		t.Fatal(errParse)
	}
	if location.Path != "/auth/oauth/github/login" || location.Query().Get("state") != started.State || location.Query().Get("redirect_uri") != "https://cpa.example/proxy/v0/resource/plugins/mirasim-id/oauth/callback" {
		t.Fatalf("OAuth location = %s", location)
	}

	callback, errCallback := provider.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/mirasim-id/oauth/callback",
		Query: url.Values{
			"state":         []string{started.State},
			"access_token":  []string{accessToken},
			"refresh_token": []string{"refresh-secret"},
		},
	})
	if errCallback != nil || callback.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback = %#v, error = %v", callback, errCallback)
	}
	if location := callback.Headers.Get("Location"); strings.Contains(location, accessToken) || strings.Contains(location, "refresh-secret") || !strings.Contains(location, "result=complete") {
		t.Fatalf("callback redirect is not clean: %q", location)
	}
	cleanPage, errClean := provider.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/mirasim-id/oauth/callback",
		Query:  url.Values{"state": []string{started.State}, "result": []string{"complete"}},
	})
	if errClean != nil || cleanPage.StatusCode != http.StatusOK || !bytes.Contains(cleanPage.Body, []byte("sign-in complete")) {
		t.Fatalf("clean callback page = %#v, error = %v", cleanPage, errClean)
	}

	polled, errPoll := provider.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{Provider: "mirasim", State: started.State, Host: pluginapi.HostConfigSummary{ProxyURL: "direct"}, HTTPClient: oauthValidationClient{}})
	if errPoll != nil || polled.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("PollLogin() = %#v, error = %v", polled, errPoll)
	}
	var payload map[string]any
	if errJSON := json.Unmarshal(polled.Auth.StorageJSON, &payload); errJSON != nil {
		t.Fatal(errJSON)
	}
	if payload["access_token"] != accessToken || payload["refresh_token"] != "refresh-secret" || payload["device_private_key"] == "" || payload["auth_kind"] != "oauth" {
		t.Fatal("OAuth auth JSON is missing self-contained credential fields")
	}
	if payload["account_id"] != "account-123" || payload["email"] != "user@example.com" {
		t.Fatalf("OAuth identity = account_id:%v email:%v", payload["account_id"], payload["email"])
	}
	if polled.Auth.FileName != "mirasim-account-123.json" || polled.Auth.Label != "Mirasim (user@example.com)" {
		t.Fatalf("OAuth auth identity = file:%q label:%q", polled.Auth.FileName, polled.Auth.Label)
	}
	if _, present := payload["credential_dir"]; present {
		t.Fatal("OAuth auth JSON contains a legacy credential path")
	}
	parsed, errParseAuth := credentials.Parse(polled.Auth.StorageJSON, settings)
	if errParseAuth != nil || parsed == nil {
		t.Fatalf("parse OAuth auth JSON error = %v", errParseAuth)
	}
}

func TestManagementOAuthPendingErrorAndExpiry(t *testing.T) {
	settings := pluginconfig.Defaults()
	provider := New(settings, mirasim.NewPool())
	provider.ConfigureOAuthResourceBasePath("/v0/resource/plugins/mirasim-id")
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	provider.oauth.now = func() time.Time { return now }

	started, errStart := provider.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback"})
	if errStart != nil {
		t.Fatal(errStart)
	}
	pending, _ := provider.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: started.State})
	if pending.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("pending poll = %#v", pending)
	}
	now = now.Add(oauthLoginTTL + time.Second)
	expired, _ := provider.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: started.State})
	if expired.Status != pluginapi.AuthLoginStatusError || !strings.Contains(expired.Message, "expired") {
		t.Fatalf("expired poll = %#v", expired)
	}

	denied, _ := provider.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback"})
	callback, _ := provider.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{Path: "/oauth/callback", Query: url.Values{"state": []string{denied.State}, "error": []string{"access_denied"}, "error_description": []string{"do-not-reflect"}}})
	if callback.StatusCode != http.StatusBadRequest || bytes.Contains(callback.Body, []byte("do-not-reflect")) {
		t.Fatalf("denied callback = %#v", callback)
	}
	errorPoll, _ := provider.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: denied.State})
	if errorPoll.Status != pluginapi.AuthLoginStatusError {
		t.Fatalf("error poll = %#v", errorPoll)
	}
}

func TestStartLoginRequiresHTTPSForPublicNonLoopbackURL(t *testing.T) {
	settings := pluginconfig.Defaults()
	settings.OAuthPublicBaseURL = "http://cpa.example"
	provider := New(settings, mirasim.NewPool())
	provider.ConfigureOAuthResourceBasePath("/v0/resource/plugins/mirasim-id")
	if _, errStart := provider.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{BaseURL: "http://127.0.0.1:8317/callback"}); errStart == nil || !strings.Contains(errStart.Error(), "HTTPS") {
		t.Fatalf("StartLogin() error = %v", errStart)
	}
}

func TestManagementOAuthRejectsCredentialsThatFailRemoteValidation(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	provider.ConfigureOAuthResourceBasePath("/v0/resource/plugins/mirasim-id")
	started, errStart := provider.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{BaseURL: "http://127.0.0.1:8317/callback"})
	if errStart != nil {
		t.Fatal(errStart)
	}
	_, _ = provider.HandleOAuthResource(context.Background(), pluginapi.ManagementRequest{Path: "/oauth/callback", Query: url.Values{
		"state":         []string{started.State},
		"access_token":  []string{identityJWT("rejected", "", time.Now().Add(time.Hour))},
		"refresh_token": []string{"refresh-secret"},
	}})
	failedClient := oauthValidationClient{status: http.StatusUnauthorized, body: []byte(`{"error":"PRIVATE_UPSTREAM_DETAIL"}`)}
	polled, errPoll := provider.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{State: started.State, HTTPClient: failedClient})
	if errPoll != nil || polled.Status != pluginapi.AuthLoginStatusError || polled.Auth.FileName != "" {
		t.Fatalf("PollLogin() = %#v, error = %v", polled, errPoll)
	}
	if strings.Contains(polled.Message, "PRIVATE_UPSTREAM_DETAIL") || !strings.Contains(polled.Message, "HTTP 401") {
		t.Fatalf("unsafe or incomplete validation error = %q", polled.Message)
	}
}

type oauthValidationClient struct {
	status int
	body   []byte
}

func (c oauthValidationClient) Do(_ context.Context, req pluginapi.HTTPRequest) (pluginapi.HTTPResponse, error) {
	parsed, _ := url.Parse(req.URL)
	if c.status != 0 {
		return pluginapi.HTTPResponse{StatusCode: c.status, Headers: make(http.Header), Body: append([]byte(nil), c.body...)}, nil
	}
	switch parsed.Path {
	case "/v1/device/session":
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(`{"ticket":"device-ticket","expiresIn":900}`)}, nil
	case "/v1/models":
		return pluginapi.HTTPResponse{StatusCode: http.StatusOK, Headers: make(http.Header), Body: []byte(`{"data":[{"id":"claude-sonnet-5"}]}`)}, nil
	default:
		return pluginapi.HTTPResponse{}, fmt.Errorf("unexpected validation path %s", parsed.Path)
	}
}

func (oauthValidationClient) DoStream(context.Context, pluginapi.HTTPRequest) (pluginapi.HTTPStreamResponse, error) {
	return pluginapi.HTTPStreamResponse{}, fmt.Errorf("unexpected validation stream")
}

func identityJWT(accountID, email string, expiry time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{"sub": accountID, "email": email, "exp": expiry.Unix()})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func toText(value any) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(value)), "\n", " "), "\r", " "))
}
