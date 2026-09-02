package auth

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

func TestManagementOAuthCompletesWithoutPuttingTokensInAuthJSON(t *testing.T) {
	settings := pluginconfig.Defaults()
	settings.CredentialDir = t.TempDir()
	settings.AdminURL = "https://auth.mirasim.example"
	settings.OAuthPublicBaseURL = "https://cpa.example/proxy"
	provider := New(settings, mirasim.NewPool())
	provider.ConfigureOAuthResourceBasePath("/v0/resource/plugins/mirasim-id")

	started, errStart := provider.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{Provider: "mirasim", BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback"})
	if errStart != nil {
		t.Fatalf("StartLogin() error = %v", errStart)
	}
	if started.State == "" || started.Provider != "mirasim" || !strings.HasPrefix(started.URL, "https://cpa.example/proxy/v0/resource/plugins/mirasim-id/oauth/start?") {
		t.Fatalf("start response = %#v", started)
	}
	if raw := []byte(toText(started.Metadata)); bytes.Contains(raw, []byte("access-secret")) || bytes.Contains(raw, []byte("refresh-secret")) {
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
			"access_token":  []string{"access-secret"},
			"refresh_token": []string{"refresh-secret"},
		},
	})
	if errCallback != nil || callback.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback = %#v, error = %v", callback, errCallback)
	}
	if location := callback.Headers.Get("Location"); strings.Contains(location, "access-secret") || strings.Contains(location, "refresh-secret") || !strings.Contains(location, "result=complete") {
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

	polled, errPoll := provider.PollLogin(context.Background(), pluginapi.AuthLoginPollRequest{Provider: "mirasim", State: started.State, Host: pluginapi.HostConfigSummary{ProxyURL: "direct"}})
	if errPoll != nil || polled.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("PollLogin() = %#v, error = %v", polled, errPoll)
	}
	if bytes.Contains(polled.Auth.StorageJSON, []byte("access-secret")) || bytes.Contains(polled.Auth.StorageJSON, []byte("refresh-secret")) {
		t.Fatalf("auth JSON leaked a token: %s", polled.Auth.StorageJSON)
	}
	assertOAuthFile(t, settings.CredentialDir, "access-token.txt", "access-secret")
	assertOAuthFile(t, settings.CredentialDir, "refresh-token.txt", "refresh-secret")
	if info, errStat := os.Stat(filepath.Join(settings.CredentialDir, "device-private-key.pem")); errStat != nil || info.Size() == 0 {
		t.Fatalf("device key stat = %#v, error = %v", info, errStat)
	}
}

func TestManagementOAuthPendingErrorAndExpiry(t *testing.T) {
	settings := pluginconfig.Defaults()
	settings.CredentialDir = t.TempDir()
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

func assertOAuthFile(t *testing.T, dir, name, want string) {
	t.Helper()
	raw, errRead := os.ReadFile(filepath.Join(dir, name))
	if errRead != nil {
		t.Fatal(errRead)
	}
	if strings.TrimSpace(string(raw)) != want {
		t.Fatalf("%s = %q, want %q", name, raw, want)
	}
}

func toText(value any) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(value)), "\n", " "), "\r", " "))
}
