package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

func TestRegisterCommandLineDeclaresOAuthOnlyFlags(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errRegister := provider.RegisterCommandLine(context.Background(), pluginapi.CommandLineRegistrationRequest{})
	if errRegister != nil {
		t.Fatalf("RegisterCommandLine() error = %v", errRegister)
	}
	flags := make(map[string]pluginapi.CommandLineFlag, len(resp.Flags))
	for _, flag := range resp.Flags {
		flags[flag.Name] = flag
	}
	for _, name := range []string{"mirasim-login", "mirasim-login-provider", "mirasim-login-email", "mirasim-login-code", "mirasim-relay-url", "mirasim-admin-url", "mirasim-client-version"} {
		if _, ok := flags[name]; !ok {
			t.Fatalf("missing command-line flag %q", name)
		}
	}
	for _, name := range []string{"mirasim-import", "mirasim-credential-dir"} {
		if _, present := flags[name]; present {
			t.Fatalf("obsolete command-line flag %q is still registered", name)
		}
	}
}

func TestParseManualOAuthResultAcceptsCallbackURLAndRejectsMissingToken(t *testing.T) {
	result, ok, errParse := parseManualOAuthResult("http://127.0.0.1/callback?state=state-1&access_token=access&refresh_token=refresh")
	if errParse != nil || !ok || result.state != "state-1" || result.accessToken != "access" || result.refreshToken != "refresh" {
		t.Fatalf("result = %#v, ok = %t, error = %v", result, ok, errParse)
	}
	if _, _, errMissing := parseManualOAuthResult("http://127.0.0.1/callback?state=state-1"); errMissing == nil {
		t.Fatal("missing-token callback was accepted")
	}
}

func TestLocalOAuthHandlerBindsMissingStateToRandomLoopbackPath(t *testing.T) {
	results := make(chan localOAuthResult, 1)
	handler := localOAuthHandler("/callback/random-path", "expected-state", results)
	req := httptest.NewRequest(http.MethodGet, "/callback/random-path?access_token=access&refresh_token=refresh", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	result := <-results
	if result.state != "expected-state" || result.accessToken != "access" || result.refreshToken != "refresh" {
		t.Fatalf("result = %#v", result)
	}

	wrong := httptest.NewRequest(http.MethodGet, "/callback/random-path?state=wrong&access_token=access&refresh_token=refresh", nil)
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrong)
	if wrongRecorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong-state status = %d", wrongRecorder.Code)
	}
}

func TestExecuteCommandLineIgnoresUntriggeredLogin(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errExecute := provider.ExecuteCommandLine(context.Background(), pluginapi.CommandLineExecutionRequest{})
	if errExecute != nil {
		t.Fatalf("ExecuteCommandLine() error = %v", errExecute)
	}
	if len(resp.Auths) != 0 || resp.ExitCode != 0 {
		t.Fatalf("response = %#v", resp)
	}
}

func TestRefreshAuthReturnsRotatedCredentialsForHostPersistence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/auth/refresh" {
			t.Errorf("refresh request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if errDecode := json.NewDecoder(r.Body).Decode(&body); errDecode != nil {
			t.Error(errDecode)
		}
		if body["refresh_token"] != "old-refresh" {
			t.Error("refresh request did not use the stored refresh token")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "new-access", "refresh_token": "new-refresh"})
	}))
	defer server.Close()

	storage, errInstall := credentials.InstallOAuth(credentials.Storage{
		Type:          credentials.Provider,
		RelayURL:      "https://relay.example",
		AdminURL:      server.URL,
		ClientVersion: "test-client",
		Raw:           map[string]any{"custom": "preserved"},
	}, "old-access", "old-refresh")
	if errInstall != nil {
		t.Fatal(errInstall)
	}
	storage.RecordProfile("", "", nil, time.Now())
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	response, errRefresh := provider.RefreshAuth(canceled, pluginapi.AuthRefreshRequest{
		AuthID:      "mirasim.json",
		StorageJSON: storage.JSON(),
		Metadata:    map[string]any{"custom_metadata": "preserved", "access_token": "do-not-copy"},
		Attributes:  map[string]string{"custom_attribute": "preserved"},
	})
	if errRefresh != nil {
		t.Fatalf("RefreshAuth() error = %v", errRefresh)
	}
	var persisted map[string]any
	if errJSON := json.Unmarshal(response.Auth.StorageJSON, &persisted); errJSON != nil {
		t.Fatal(errJSON)
	}
	if persisted["access_token"] != "new-access" || persisted["refresh_token"] != "new-refresh" || persisted["device_private_key"] != storage.DevicePrivateKey || persisted["custom"] != "preserved" {
		t.Fatal("RefreshAuth() did not return complete rotated provider storage")
	}
	if response.Auth.Metadata["custom_metadata"] != "preserved" {
		t.Fatal("RefreshAuth() lost host-managed metadata")
	}
	if response.Auth.Metadata["access_token"] != "new-access" || response.Auth.Metadata["refresh_token"] != "new-refresh" {
		t.Fatal("RefreshAuth() did not return rotated credentials in CPA runtime metadata")
	}
	if response.Auth.Metadata["expired"] == "" || response.Auth.Metadata["last_refresh"] == "" {
		t.Fatal("RefreshAuth() did not return conventional OAuth timing metadata")
	}
	if response.Auth.Attributes["custom_attribute"] != "preserved" || response.Auth.Attributes["auth_kind"] != "oauth" {
		t.Fatal("RefreshAuth() lost standard or host-managed attributes")
	}
}
