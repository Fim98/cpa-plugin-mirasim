package auth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
)

// cliManualPromptDelay is a variable only so tests can shorten the wait before
// the manual paste prompt appears.
var cliManualPromptDelay = 15 * time.Second

const manualPastePrompt = "Paste the Mirasim callback URL (or press Enter to keep waiting): "

// These are constant errors on purpose. The pasted value is operator-supplied
// and may carry a credential, so nothing derived from it may reach an error
// string, a log line or stderr — url.Parse in particular quotes the offending
// input in its message.
var (
	errInvalidCallbackURL   = errors.New("invalid Mirasim callback URL")
	errForeignCallbackURL   = errors.New("pasted URL is not this login's Mirasim callback address")
	errMissingCallbackToken = errors.New("Mirasim callback URL is missing access_token")
)

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
	// The prompt is served by the process's single stdin reader rather than by a
	// reader of this login's own: os.Stdin cannot be read with cancellation, so a
	// per-login reader would outlive every login that times out.
	var promptRequests chan<- chan<- stdinReply
	var pasted chan stdinReply
	for {
		select {
		case <-ctx.Done():
			return pluginapi.AuthData{}, nil, ctx.Err()
		case <-timer.C:
			return pluginapi.AuthData{}, nil, fmt.Errorf("Mirasim OAuth login timed out")
		case result := <-capture.Results():
			return p.finishLocalLogin(ctx, settings, proxyURL, state, result)
		case <-manualTimer.C:
			pasted = make(chan stdinReply, 1)
			promptRequests = p.promptRequests()
		case promptRequests <- pasted:
			// Only ask once the reader is actually on this login's line.
			promptRequests = nil
			_, _ = os.Stdout.Write([]byte(manualPastePrompt))
		case reply := <-pasted:
			pasted = nil
			if reply.err != nil && reply.line == "" {
				return pluginapi.AuthData{}, nil, reply.err
			}
			result, okResult, errParse := parseManualOAuthResult(reply.line, capture.CallbackURL())
			if errParse != nil {
				return pluginapi.AuthData{}, nil, errParse
			}
			if okResult {
				// Mirasim 0.0.272 omits state from its token callback. The paste has
				// already been checked against this login's own callback address, so
				// bind a state-less result to this invocation before the
				// constant-time check in finishLocalLogin.
				bindMissingOAuthState(&result, state)
				return p.finishLocalLogin(ctx, settings, proxyURL, state, result)
			}
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

// parseManualOAuthResult reads a callback URL the operator pasted at the prompt,
// and accepts it only if it names this login's own callback address.
//
// That check is the paste's channel binding. Mirasim 0.0.272 omits the state it
// was handed, so a state-less paste carries nothing else tying it to the login in
// progress, and any URL an operator could be talked into pasting would otherwise
// install someone else's credentials as their own. The legitimate paste always
// passes: it is copied from the browser's address bar, which holds the
// redirect_uri this login just advertised, down to its 144-bit random path.
func parseManualOAuthResult(input, callbackURL string) (localOAuthResult, bool, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return localOAuthResult{}, false, nil
	}
	if !strings.Contains(value, "://") {
		// An address bar that hides the scheme still yields host, path and query.
		value = "http://" + value
	}
	parsed, errParse := url.Parse(value)
	if errParse != nil {
		return localOAuthResult{}, false, errInvalidCallbackURL
	}
	expected, errExpected := url.Parse(callbackURL)
	if errExpected != nil {
		return localOAuthResult{}, false, errInvalidCallbackURL
	}
	if !strings.EqualFold(parsed.Host, expected.Host) || parsed.Path != expected.Path {
		return localOAuthResult{}, false, errForeignCallbackURL
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
		return localOAuthResult{}, false, errMissingCallbackToken
	}
	return result, true, nil
}

// stdinReply is one line read from standard input, or the error that ended the
// read.
type stdinReply struct {
	line string
	err  error
}

// stdinPrompter serves every interactive prompt in the process from one
// goroutine. A blocking read of os.Stdin cannot be cancelled, so a prompt that
// starts a reader of its own leaves that reader — and its buffered reader —
// behind whenever the login it belongs to times out or is cancelled, one per
// login. This reader is created once, parks while no prompt is outstanding, and
// hands each line to whichever login asked for it.
type stdinPrompter struct {
	requests chan chan<- stdinReply
}

func newStdinPrompter(source io.Reader) *stdinPrompter {
	prompter := &stdinPrompter{requests: make(chan chan<- stdinReply)}
	go prompter.serve(source)
	return prompter
}

// serve reads on demand only, so a plugin that never prompts never consumes a
// byte of the host's stdin.
func (s *stdinPrompter) serve(source io.Reader) {
	reader := bufio.NewReader(source)
	for reply := range s.requests {
		line, errRead := reader.ReadString('\n')
		// Every reply channel is buffered, so a caller that stopped waiting drops
		// its line instead of parking this goroutine on the send.
		reply <- stdinReply{line: line, err: errRead}
	}
}

var (
	processStdinOnce   sync.Once
	processStdinReader *stdinPrompter
)

// processStdinPrompts is created on first prompt, not at init, so a plugin that
// never runs an interactive login starts no goroutine at all.
func processStdinPrompts() *stdinPrompter {
	processStdinOnce.Do(func() { processStdinReader = newStdinPrompter(os.Stdin) })
	return processStdinReader
}

// promptRequests is the channel a waiting login hands its reply channel to.
// Tests substitute a prompter over a scripted reader; every real login shares
// the one reader over os.Stdin.
func (p *Provider) promptRequests() chan<- chan<- stdinReply {
	if p != nil && p.prompter != nil {
		return p.prompter.requests
	}
	return processStdinPrompts().requests
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
