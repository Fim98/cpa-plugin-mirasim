package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
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

const testCallbackURL = "http://127.0.0.1:65123/callback/Hn7_2xKq-zR4vAe1"

func TestParseManualOAuthResultAcceptsCallbackURLAndRejectsMissingToken(t *testing.T) {
	result, ok, errParse := parseManualOAuthResult(testCallbackURL+"?state=state-1&access_token=access&refresh_token=refresh", testCallbackURL)
	if errParse != nil || !ok || result.state != "state-1" || result.accessToken != "access" || result.refreshToken != "refresh" {
		t.Fatalf("result = %#v, ok = %t, error = %v", result, ok, errParse)
	}
	if _, _, errMissing := parseManualOAuthResult(testCallbackURL+"?state=state-1", testCallbackURL); !errors.Is(errMissing, errMissingCallbackToken) {
		t.Fatalf("missing-token callback error = %v", errMissing)
	}
}

// A pasted URL that does not name this login's own callback address carries no
// binding to it at all once Mirasim has dropped the state parameter, so pasting
// one would install whatever credentials it carries — an attacker's, if the
// operator was talked into pasting an attacker's URL.
func TestManualPasteMustNameThisLoginsCallbackAddress(t *testing.T) {
	credentialQuery := "?access_token=injected-access&refresh_token=injected-refresh"
	for name, pasted := range map[string]string{
		"foreign host":        "http://attacker.invalid/callback/Hn7_2xKq-zR4vAe1" + credentialQuery,
		"foreign loopback":    "http://127.0.0.1:65124/callback/Hn7_2xKq-zR4vAe1" + credentialQuery,
		"guessed path":        "http://127.0.0.1:65123/callback/guessed" + credentialQuery,
		"no path":             "http://127.0.0.1:65123/" + credentialQuery,
		"bare query":          "access_token=injected-access&refresh_token=injected-refresh",
		"bare query with '?'": credentialQuery,
	} {
		result, ok, errPaste := parseManualOAuthResult(pasted, testCallbackURL)
		if ok || errPaste == nil {
			t.Fatalf("%s: result = %#v, ok = %t, error = %v", name, result, ok, errPaste)
		}
		if strings.Contains(errPaste.Error(), "injected") {
			t.Fatalf("%s: error quoted the pasted credential: %q", name, errPaste)
		}
	}

	// The legitimate paste is copied from the address bar, so it always carries
	// the callback path this login advertised - with or without the scheme the
	// browser hides, and with the state Mirasim 0.0.272 drops.
	for name, pasted := range map[string]string{
		"address bar":   testCallbackURL + "?access_token=access&refresh_token=refresh",
		"hidden scheme": strings.TrimPrefix(testCallbackURL, "http://") + "?access_token=access&refresh_token=refresh",
	} {
		result, ok, errPaste := parseManualOAuthResult(pasted, testCallbackURL)
		if errPaste != nil || !ok {
			t.Fatalf("%s: legitimate state-less paste was rejected: ok = %t, error = %v", name, ok, errPaste)
		}
		if result.state != "" || result.accessToken != "access" || result.refreshToken != "refresh" {
			t.Fatalf("%s: result = %#v", name, result)
		}
	}
}

// auth-dir holds bearer tokens: a malformed paste must not echo any part of
// itself back, and url.Parse's own error quotes the whole URL it was given.
func TestManualPasteErrorNeverEchoesTheInput(t *testing.T) {
	pasted := testCallbackURL + "?refresh_token=refresh#access_token=SUPERSECRETTOKEN%ZZ"
	result, ok, errPaste := parseManualOAuthResult(pasted, testCallbackURL)
	if ok || !errors.Is(errPaste, errInvalidCallbackURL) {
		t.Fatalf("result = %#v, ok = %t, error = %v", result, ok, errPaste)
	}
	if message := errPaste.Error(); strings.Contains(message, "SUPERSECRETTOKEN") || strings.Contains(message, "%ZZ") || strings.Contains(message, "refresh") {
		t.Fatalf("parse error echoed the pasted input: %q", message)
	}
}

// The whole point of keeping bindMissingOAuthState: Mirasim 0.0.272 omits the
// state it was handed, and --mirasim-login must still complete when the operator
// pastes that state-less callback URL.
func TestStatelessPasteOnThisLoginsCallbackURLStillCompletesTheLogin(t *testing.T) {
	admin := newOAuthProfileServer(t)
	t.Cleanup(admin.Close)
	relay := newRelayValidationServer(t)
	settings := pluginconfig.Defaults()
	settings.AdminURL = admin.URL
	settings.RelayURL = relay.URL
	provider := New(settings, mirasim.NewPool())

	state := "state-of-the-login-in-progress"
	accessToken := identityJWT("account-77", "user@example.com", time.Now().Add(time.Hour))
	result, ok, errPaste := parseManualOAuthResult(testCallbackURL+"?access_token="+accessToken+"&refresh_token=refresh-secret", testCallbackURL)
	if errPaste != nil || !ok {
		t.Fatalf("state-less paste was rejected: ok = %t, error = %v", ok, errPaste)
	}
	if result.state != "" {
		t.Fatalf("paste carried a state: %q", result.state)
	}
	bindMissingOAuthState(&result, state)

	auth, stdout, errLogin := provider.finishLocalLogin(context.Background(), settings, "direct", state, result)
	if errLogin != nil {
		t.Fatalf("state-less paste login error = %v", errLogin)
	}
	if auth.FileName != "mirasim-account-77.json" || !strings.Contains(string(stdout), "successful") {
		t.Fatalf("auth = %q, stdout = %q", auth.FileName, stdout)
	}
}

// A paste that names this login's callback address but carries someone else's
// state is still refused: the state check is not weakened by the new one.
func TestPasteCarryingAForeignStateIsStillRefused(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	result, ok, errPaste := parseManualOAuthResult(testCallbackURL+"?state=someone-elses-state&access_token=access&refresh_token=refresh", testCallbackURL)
	if errPaste != nil || !ok {
		t.Fatalf("ok = %t, error = %v", ok, errPaste)
	}
	bindMissingOAuthState(&result, "state-of-the-login-in-progress")
	if _, _, errLogin := provider.finishLocalLogin(context.Background(), pluginconfig.Defaults(), "direct", "state-of-the-login-in-progress", result); errLogin == nil {
		t.Fatal("a paste with a foreign state was accepted")
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

// A blocking read of os.Stdin cannot be cancelled, so every login that times out
// or is cancelled while its paste prompt is unanswered used to strand the reader
// it had started. Here the count of live goroutines must be the same after six
// abandoned logins as it was after the first.
func TestRepeatedManualPromptsDoNotAccumulateGoroutines(t *testing.T) {
	provider, _ := newLoopbackLoginProvider(t, pluginconfig.Defaults())
	source := newScriptedStdin()
	provider.prompter = newStdinPrompter(source)
	// Retire the reader with the test rather than leaving it parked for the rest
	// of the run, so a later goroutine count cannot inherit it.
	t.Cleanup(func() {
		close(provider.prompter.requests)
		source.close()
	})
	restoreDelay := cliManualPromptDelay
	cliManualPromptDelay = time.Millisecond
	t.Cleanup(func() { cliManualPromptDelay = restoreDelay })

	const logins = 6
	baseline := 0
	for attempt := 1; attempt <= logins; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		// The scripted reader cancels the login at the moment it serves the prompt,
		// so each iteration reaches the prompt and then abandons it, exactly as a
		// TTL expiry would.
		source.queue("\n", cancel)
		_, _, errLogin := provider.runLocalLogin(ctx, provider.settings, "github", "", true)
		cancel()
		if !errors.Is(errLogin, context.Canceled) {
			t.Fatalf("login %d error = %v, want context.Canceled", attempt, errLogin)
		}
		if served := source.served(); served != attempt {
			t.Fatalf("login %d: prompt reads served = %d, want %d", attempt, served, attempt)
		}
		if attempt == 1 {
			baseline = settledGoroutines(t)
		}
	}
	if after := settledGoroutines(t); after > baseline {
		t.Fatalf("goroutines = %d after %d logins, want no more than %d", after, logins, baseline)
	}
}

// settledGoroutines reports the count once it has stopped moving, so a
// listener's Serve goroutine returning just after its Shutdown is not mistaken
// for a leak in either direction.
func settledGoroutines(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	previous, stable := -1, 0
	for {
		count := runtime.NumGoroutine()
		switch {
		case count != previous:
			previous, stable = count, 0
		case stable >= 5:
			return count
		default:
			stable++
		}
		if time.Now().After(deadline) {
			return count
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// scriptedStdin stands in for os.Stdin. Read blocks while no line is queued, so
// an unanswered prompt parks the reader exactly as a real terminal would.
type scriptedStdin struct {
	lines  chan scriptedLine
	reads  chan struct{}
	closed chan struct{}
}

type scriptedLine struct {
	text   string
	onRead func()
}

func newScriptedStdin() *scriptedStdin {
	return &scriptedStdin{lines: make(chan scriptedLine, 16), reads: make(chan struct{}, 64), closed: make(chan struct{})}
}

func (s *scriptedStdin) queue(text string, onRead func()) {
	s.lines <- scriptedLine{text: text, onRead: onRead}
}

func (s *scriptedStdin) served() int { return len(s.reads) }

func (s *scriptedStdin) close() { close(s.closed) }

func (s *scriptedStdin) Read(p []byte) (int, error) {
	select {
	case line := <-s.lines:
		s.reads <- struct{}{}
		if line.onRead != nil {
			line.onRead()
		}
		return copy(p, line.text), nil
	case <-s.closed:
		return 0, io.EOF
	}
}

func newRelayValidationServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/device/session":
			_, _ = w.Write([]byte(`{"ticket":"device-ticket","expiresIn":900}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-5"}]}`))
		default:
			t.Errorf("unexpected relay request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
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
