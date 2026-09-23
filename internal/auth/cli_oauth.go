package auth

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
)

const cliManualPromptDelay = 15 * time.Second

func (p *Provider) runLocalLogin(ctx context.Context, settings pluginconfig.Settings, provider string, proxyURL string, noBrowser bool) (pluginapi.AuthData, []byte, error) {
	loginProvider, errProvider := resolveLoginProvider(provider, settings.OAuthLoginProvider)
	if errProvider != nil {
		return pluginapi.AuthData{}, nil, errProvider
	}
	offered, errDiscovery := discoverLoginProviders(ctx, settings.AdminURL, proxyURL)
	if errDiscovery != nil {
		return pluginapi.AuthData{}, nil, errDiscovery
	}
	if !providerOffered(offered, loginProvider) {
		return pluginapi.AuthData{}, nil, unsupportedLoginProviderError(loginProvider, offered)
	}
	state, errState := randomOAuthValue(32)
	if errState != nil {
		return pluginapi.AuthData{}, nil, errState
	}
	// The same loopback capture the Management Center flow uses, so both paths
	// share one listener, one single-use random callback path, and one set of
	// credential checks.
	capture, errCapture := startLoopbackCapture(loopbackCallbackPort(settings.OAuthCallbackPort), state)
	if errCapture != nil {
		return pluginapi.AuthData{}, nil, errCapture
	}
	defer capture.Close()
	authURL, errURL := buildMirasimOAuthURL(settings.AdminURL, loginProvider, capture.CallbackURL(), state)
	if errURL != nil {
		return pluginapi.AuthData{}, nil, errURL
	}

	prompt := []byte("Open this URL to authenticate Mirasim:\n\n" + authURL + "\n\n")
	_, _ = os.Stdout.Write(prompt)
	if !noBrowser {
		_ = openBrowser(authURL)
	}
	timer := time.NewTimer(cliLoginTTL)
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
		case result := <-capture.Results():
			return p.finishLocalLogin(ctx, settings, proxyURL, state, result)
		case <-manualTimer.C:
			manualInput, manualError = asyncPrompt("Paste the Mirasim callback URL (or press Enter to keep waiting): ")
		case input := <-manualInput:
			manualInput, manualError = nil, nil
			result, okResult, errParse := parseManualOAuthResult(input)
			if errParse != nil {
				return pluginapi.AuthData{}, nil, errParse
			}
			if okResult {
				// Mirasim 0.0.272 omits state from its token callback. The
				// interactive prompt is scoped to this one active CLI login, so
				// bind a state-less pasted result to that invocation before the
				// constant-time check in finishLocalLogin.
				bindMissingOAuthState(&result, state)
				return p.finishLocalLogin(ctx, settings, proxyURL, state, result)
			}
		case errRead := <-manualError:
			return pluginapi.AuthData{}, nil, errRead
		}
	}
}

func (p *Provider) finishLocalLogin(ctx context.Context, settings pluginconfig.Settings, proxyURL, state string, result localOAuthResult) (pluginapi.AuthData, []byte, error) {
	if result.errorMessage != "" {
		return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth login failed: %s", result.errorMessage)
	}
	if !constantTimeEqual(state, strings.TrimSpace(result.state)) {
		return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth state mismatch")
	}
	storage, errStorage := p.finalizeOAuthStorage(ctx, settings, result.accessToken, result.refreshToken, proxyURL, nil)
	if errStorage != nil {
		return pluginapi.AuthData{}, nil, errStorage
	}
	result.accessToken, result.refreshToken = "", ""
	client := p.pool.Client(storage)
	fileName := storage.DefaultAuthFileName()
	auth := storage.AuthData(fileName, fileName, client.NextRefreshAfter(time.Now()))
	return auth, []byte("Mirasim authentication successful.\n"), nil
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
