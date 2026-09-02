package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

type Provider struct {
	settings pluginconfig.Settings
	pool     *mirasim.Pool
	oauth    *oauthCoordinator
}

func New(settings pluginconfig.Settings, pool *mirasim.Pool) *Provider {
	return &Provider{settings: settings, pool: pool, oauth: newOAuthCoordinator()}
}

func (p *Provider) Identifier() string { return credentials.Provider }

func (p *Provider) ParseAuth(_ context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	storage, errParse := credentials.Parse(req.RawJSON, p.settings)
	if errParse != nil {
		return pluginapi.AuthParseResponse{Handled: true}, errParse
	}
	if storage == nil {
		return pluginapi.AuthParseResponse{}, nil
	}
	client := p.pool.Client(*storage)
	if errProxy := client.SetAuthProxy(req.Host.ProxyURL); errProxy != nil {
		return pluginapi.AuthParseResponse{Handled: true}, errProxy
	}
	if errValidate := client.Validate(); errValidate != nil {
		return pluginapi.AuthParseResponse{Handled: true}, errValidate
	}
	auth := storage.AuthData(req.FileName, req.FileName, client.NextRefreshAfter(time.Now()))
	return pluginapi.AuthParseResponse{Handled: true, Auth: auth, Auths: []pluginapi.AuthData{auth}}, nil
}

func (p *Provider) RefreshAuth(ctx context.Context, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	storage, errParse := credentials.Parse(req.StorageJSON, p.settings)
	if errParse != nil {
		return pluginapi.AuthRefreshResponse{}, errParse
	}
	if storage == nil {
		return pluginapi.AuthRefreshResponse{}, fmt.Errorf("Mirasim auth storage is missing")
	}
	client := p.pool.Client(*storage)
	if _, errRefresh := client.RefreshAccessWithProxy(ctx, req.Host.ProxyURL); errRefresh != nil {
		return pluginapi.AuthRefreshResponse{}, errRefresh
	}
	next := client.NextRefreshAfter(time.Now())
	auth := storage.AuthData(req.AuthID, req.AuthID, next)
	return pluginapi.AuthRefreshResponse{Auth: auth, NextRefreshAfter: next}, nil
}

func (p *Provider) RegisterCommandLine(context.Context, pluginapi.CommandLineRegistrationRequest) (pluginapi.CommandLineRegistrationResponse, error) {
	return pluginapi.CommandLineRegistrationResponse{Flags: []pluginapi.CommandLineFlag{
		{Name: "mirasim-login", Usage: "Run Mirasim browser OAuth login.", Type: "bool", DefaultValue: "false"},
		{Name: "mirasim-login-provider", Usage: "Mirasim OAuth account provider: github or google.", Type: "string", DefaultValue: "github"},
		{Name: "mirasim-import", Usage: "Import a Mirasim plaintext credential directory.", Type: "bool", DefaultValue: "false"},
		{Name: "mirasim-credential-dir", Usage: "Directory containing refresh-token.txt and device-private-key.pem.", Type: "string"},
		{Name: "mirasim-relay-url", Usage: "Mirasim relay base URL.", Type: "string"},
		{Name: "mirasim-admin-url", Usage: "Mirasim authentication service base URL.", Type: "string"},
		{Name: "mirasim-client-version", Usage: "Value sent in x-mirasim-client.", Type: "string"},
	}}, nil
}

func (p *Provider) ExecuteCommandLine(ctx context.Context, req pluginapi.CommandLineExecutionRequest) (pluginapi.CommandLineExecutionResponse, error) {
	login := flagBool(req.TriggeredFlags, "mirasim-login")
	importCredentials := flagBool(req.TriggeredFlags, "mirasim-import")
	if login && importCredentials {
		return commandError(fmt.Errorf("--mirasim-login and --mirasim-import cannot be used together")), nil
	}
	if !login && !importCredentials {
		return pluginapi.CommandLineExecutionResponse{}, nil
	}
	settings := p.settingsFromFlags(req.Flags)
	if login {
		auth, stdout, errLogin := p.runLocalLogin(ctx, settings, flagString(req.Flags, "mirasim-login-provider"), req.Host.ProxyURL, flagBoolValue(req.Flags, "no-browser"))
		if errLogin != nil {
			return pluginapi.CommandLineExecutionResponse{Stdout: stdout, Stderr: []byte(errLogin.Error() + "\n"), ExitCode: 1}, nil
		}
		return pluginapi.CommandLineExecutionResponse{Stdout: stdout, Auths: []pluginapi.AuthData{auth}}, nil
	}
	storage, errStorage := credentials.FromSettings(settings)
	if errStorage != nil {
		return commandError(errStorage), nil
	}
	client := p.pool.Client(storage)
	if errProxy := client.SetAuthProxy(req.Host.ProxyURL); errProxy != nil {
		return commandError(errProxy), nil
	}
	if errValidate := client.Validate(); errValidate != nil {
		return commandError(errValidate), nil
	}
	auth := storage.AuthData("mirasim.json", "mirasim.json", client.NextRefreshAfter(time.Now()))
	stdout := fmt.Sprintf("Imported Mirasim credential directory: %s\nNo token or private-key value was copied into the auth JSON.\n", storage.CredentialDir)
	return pluginapi.CommandLineExecutionResponse{Stdout: []byte(stdout), Auths: []pluginapi.AuthData{auth}}, nil
}

func (p *Provider) settingsFromFlags(flags map[string]pluginapi.CommandLineFlagValue) pluginconfig.Settings {
	settings := p.settings
	if value := flagString(flags, "mirasim-credential-dir"); value != "" {
		settings.CredentialDir = value
	}
	if value := flagString(flags, "mirasim-relay-url"); value != "" {
		settings.RelayURL = value
	}
	if value := flagString(flags, "mirasim-admin-url"); value != "" {
		settings.AdminURL = value
	}
	if value := flagString(flags, "mirasim-client-version"); value != "" {
		settings.ClientVersion = value
	}
	return settings
}

func commandError(err error) pluginapi.CommandLineExecutionResponse {
	return pluginapi.CommandLineExecutionResponse{Stderr: []byte(err.Error() + "\n"), ExitCode: 1}
}

func flagBool(flags map[string]pluginapi.CommandLineFlagValue, name string) bool {
	value, ok := flags[name]
	return ok && value.Set && strings.EqualFold(strings.TrimSpace(value.Value), "true")
}

func flagString(flags map[string]pluginapi.CommandLineFlagValue, name string) string {
	value, ok := flags[name]
	if !ok {
		return ""
	}
	return strings.TrimSpace(value.Value)
}
