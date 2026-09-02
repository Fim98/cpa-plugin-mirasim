package auth

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

func TestRegisterCommandLineDeclaresImportFlags(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errRegister := provider.RegisterCommandLine(context.Background(), pluginapi.CommandLineRegistrationRequest{})
	if errRegister != nil {
		t.Fatalf("RegisterCommandLine() error = %v", errRegister)
	}
	flags := make(map[string]pluginapi.CommandLineFlag, len(resp.Flags))
	for _, flag := range resp.Flags {
		flags[flag.Name] = flag
	}
	for _, name := range []string{"mirasim-login", "mirasim-login-provider", "mirasim-import", "mirasim-credential-dir", "mirasim-relay-url", "mirasim-admin-url", "mirasim-client-version"} {
		if _, ok := flags[name]; !ok {
			t.Fatalf("missing command-line flag %q", name)
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

func TestExecuteCommandLineRejectsLoginImportConflict(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	triggered := map[string]pluginapi.CommandLineFlagValue{
		"mirasim-login":  {Name: "mirasim-login", Type: "bool", Value: "true", Set: true},
		"mirasim-import": {Name: "mirasim-import", Type: "bool", Value: "true", Set: true},
	}
	resp, errExecute := provider.ExecuteCommandLine(context.Background(), pluginapi.CommandLineExecutionRequest{TriggeredFlags: triggered})
	if errExecute != nil || resp.ExitCode != 1 {
		t.Fatalf("response = %#v, error = %v", resp, errExecute)
	}
}

func TestExecuteCommandLineIgnoresUntriggeredImport(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errExecute := provider.ExecuteCommandLine(context.Background(), pluginapi.CommandLineExecutionRequest{})
	if errExecute != nil {
		t.Fatalf("ExecuteCommandLine() error = %v", errExecute)
	}
	if len(resp.Auths) != 0 || resp.ExitCode != 0 {
		t.Fatalf("response = %#v", resp)
	}
}
