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
	for _, name := range []string{"mirasim-import", "mirasim-credential-dir", "mirasim-relay-url", "mirasim-admin-url", "mirasim-client-version"} {
		if _, ok := flags[name]; !ok {
			t.Fatalf("missing command-line flag %q", name)
		}
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
