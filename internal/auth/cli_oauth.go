package auth

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
)

const cliManualPromptDelay = 15 * time.Second

type localOAuthResult struct {
	state        string
	accessToken  string
	refreshToken string
	errorMessage string
}

func (p *Provider) runLocalLogin(ctx context.Context, settings pluginconfig.Settings, provider string, proxyURL string, noBrowser bool) (pluginapi.AuthData, []byte, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "github"
	}
	if provider != "github" && provider != "google" {
		return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth provider must be github or google")
	}
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		return pluginapi.AuthData{}, nil, fmt.Errorf("start Mirasim OAuth callback listener: %w", errListen)
	}
	defer func() { _ = listener.Close() }()
	state, errState := randomOAuthValue(32)
	if errState != nil {
		return pluginapi.AuthData{}, nil, errState
	}
	pathToken, errPath := randomOAuthValue(18)
	if errPath != nil {
		return pluginapi.AuthData{}, nil, errPath
	}
	callbackPath := "/callback/" + pathToken
	callbackURL := "http://" + listener.Addr().String() + callbackPath
	authURL, errURL := buildMirasimOAuthURL(settings.AdminURL, provider, callbackURL, state)
	if errURL != nil {
		return pluginapi.AuthData{}, nil, errURL
	}

	resultCh := make(chan localOAuthResult, 1)
	server := &http.Server{Handler: localOAuthHandler(callbackPath, state, resultCh), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if errServe := server.Serve(listener); errServe != nil && errServe != http.ErrServerClosed {
			select {
			case resultCh <- localOAuthResult{errorMessage: "Mirasim OAuth callback listener stopped"}:
			default:
			}
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	prompt := []byte("Open this URL to authenticate Mirasim:\n\n" + authURL + "\n\n")
	_, _ = os.Stdout.Write(prompt)
	if !noBrowser {
		_ = openBrowser(authURL)
	}
	timer := time.NewTimer(oauthLoginTTL)
	defer timer.Stop()
	manualTimer := time.NewTimer(cliManualPromptDelay)
	defer manualTimer.Stop()
	var manualInput <-chan string
	var manualError <-chan error
	for {
		select {
		case <-ctx.Done():
			return pluginapi.AuthData{}, nil, ctx.Err()
		case <-timer.C:
			return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth login timed out")
		case result := <-resultCh:
			return p.finishLocalLogin(settings, proxyURL, state, result)
		case <-manualTimer.C:
			manualInput, manualError = asyncPrompt("Paste the Mirasim callback URL (or press Enter to keep waiting): ")
		case input := <-manualInput:
			manualInput, manualError = nil, nil
			result, okResult, errParse := parseManualOAuthResult(input)
			if errParse != nil {
				return pluginapi.AuthData{}, nil, errParse
			}
			if okResult {
				return p.finishLocalLogin(settings, proxyURL, state, result)
			}
		case errRead := <-manualError:
			return pluginapi.AuthData{}, nil, errRead
		}
	}
}

func (p *Provider) finishLocalLogin(settings pluginconfig.Settings, proxyURL, state string, result localOAuthResult) (pluginapi.AuthData, []byte, error) {
	if result.errorMessage != "" {
		return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth login failed: %s", result.errorMessage)
	}
	if !constantTimeEqual(state, strings.TrimSpace(result.state)) {
		return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth state mismatch")
	}
	storage, errStorage := credentials.FromSettings(settings)
	if errStorage != nil {
		return pluginapi.AuthData{}, nil, errStorage
	}
	if errInstall := credentials.InstallOAuth(storage, result.accessToken, result.refreshToken); errInstall != nil {
		return pluginapi.AuthData{}, nil, errInstall
	}
	result.accessToken, result.refreshToken = "", ""
	p.pool.Forget(storage)
	client := p.pool.Client(storage)
	if errProxy := client.SetAuthProxy(proxyURL); errProxy != nil {
		return pluginapi.AuthData{}, nil, errProxy
	}
	if errValidate := client.Validate(); errValidate != nil {
		return pluginapi.AuthData{}, nil, errValidate
	}
	auth := storage.AuthData("mirasim.json", "mirasim.json", client.NextRefreshAfter(time.Now()))
	auth.Metadata["credential_mode"] = "oauth-managed-plaintext"
	auth.Metadata["auth_kind"] = "oauth"
	return auth, []byte("Mirasim authentication successful.\n"), nil
}

func localOAuthHandler(callbackPath, expectedState string, results chan<- localOAuthResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		for key, values := range browserHeaders(nil) {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		if !strings.EqualFold(r.Method, http.MethodGet) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result := oauthResultFromValues(r.URL.Query())
		if !constantTimeEqual(expectedState, result.state) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("<html><body><h1>Invalid OAuth state</h1></body></html>"))
			return
		}
		if result.errorMessage == "" && (result.accessToken == "" || result.refreshToken == "") {
			result.errorMessage = "callback did not include renewable credentials"
		}
		select {
		case results <- result:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html><body><h1>Mirasim sign-in complete</h1><p>You may close this window.</p></body></html>"))
		default:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("<html><body><h1>This callback was already used</h1></body></html>"))
		}
	})
	return mux
}

func oauthResultFromValues(values url.Values) localOAuthResult {
	accessToken := strings.TrimSpace(values.Get("access_token"))
	if accessToken == "" {
		accessToken = strings.TrimSpace(values.Get("token"))
	}
	errorMessage := ""
	if strings.TrimSpace(values.Get("error")) != "" {
		errorMessage = "Mirasim cancelled or rejected the login"
	}
	return localOAuthResult{
		state:        strings.TrimSpace(values.Get("state")),
		accessToken:  accessToken,
		refreshToken: strings.TrimSpace(values.Get("refresh_token")),
		errorMessage: errorMessage,
	}
}

func parseManualOAuthResult(input string) (localOAuthResult, bool, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return localOAuthResult{}, false, nil
	}
	candidate := value
	if !strings.Contains(candidate, "://") {
		switch {
		case strings.HasPrefix(candidate, "?"):
			candidate = "http://localhost" + candidate
		case strings.Contains(candidate, "="):
			candidate = "http://localhost/?" + strings.TrimPrefix(candidate, "?")
		default:
			return localOAuthResult{}, false, fmt.Errorf("invalid Mirasim callback URL")
		}
	}
	parsed, errParse := url.Parse(candidate)
	if errParse != nil {
		return localOAuthResult{}, false, fmt.Errorf("parse Mirasim callback URL: %w", errParse)
	}
	values := parsed.Query()
	if parsed.Fragment != "" {
		if fragment, errFragment := url.ParseQuery(parsed.Fragment); errFragment == nil {
			for key, entries := range fragment {
				if values.Get(key) == "" && len(entries) > 0 {
					values.Set(key, entries[0])
				}
			}
		}
	}
	result := oauthResultFromValues(values)
	if result.accessToken == "" && result.errorMessage == "" {
		return localOAuthResult{}, false, fmt.Errorf("Mirasim callback URL is missing access_token")
	}
	return result, true, nil
}

func asyncPrompt(message string) (<-chan string, <-chan error) {
	inputCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		_, _ = os.Stdout.Write([]byte(message))
		input, errRead := bufio.NewReader(os.Stdin).ReadString('\n')
		if errRead != nil && input == "" {
			errCh <- errRead
			return
		}
		inputCh <- input
	}()
	return inputCh, errCh
}

func openBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", target)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}

func flagBoolValue(flags map[string]pluginapi.CommandLineFlagValue, name string) bool {
	value, ok := flags[name]
	return ok && strings.EqualFold(strings.TrimSpace(value.Value), "true")
}
