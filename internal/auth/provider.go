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
}

func New(settings pluginconfig.Settings, pool *mirasim.Pool) *Provider {
	return &Provider{settings: settings, pool: pool}
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
	if errValidate := client.Validate(); errValidate != nil {
		return pluginapi.AuthParseResponse{Handled: true}, errValidate
	}
	auth := storage.AuthData(req.FileName, req.FileName, client.NextRefreshAfter(time.Now()))
	return pluginapi.AuthParseResponse{Handled: true, Auth: auth, Auths: []pluginapi.AuthData{auth}}, nil
}

func (p *Provider) StartLogin(context.Context, pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("Mirasim uses imported plaintext credentials; run CLIProxyAPI with --mirasim-import")
}

func (p *Provider) PollLogin(context.Context, pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "Mirasim login polling is not supported; use --mirasim-import"}, nil
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
	if _, errRefresh := client.RefreshAccess(ctx); errRefresh != nil {
		return pluginapi.AuthRefreshResponse{}, errRefresh
	}
	next := client.NextRefreshAfter(time.Now())
	auth := storage.AuthData(req.AuthID, req.AuthID, next)
	return pluginapi.AuthRefreshResponse{Auth: auth, NextRefreshAfter: next}, nil
}

func (p *Provider) RegisterCommandLine(context.Context, pluginapi.CommandLineRegistrationRequest) (pluginapi.CommandLineRegistrationResponse, error) {
	return pluginapi.CommandLineRegistrationResponse{Flags: []pluginapi.CommandLineFlag{
		{Name: "mirasim-import", Usage: "Import a Mirasim plaintext credential directory.", Type: "bool", DefaultValue: "false"},
		{Name: "mirasim-credential-dir", Usage: "Directory containing refresh-token.txt and device-private-key.pem.", Type: "string"},
		{Name: "mirasim-relay-url", Usage: "Mirasim relay base URL.", Type: "string"},
		{Name: "mirasim-admin-url", Usage: "Mirasim authentication service base URL.", Type: "string"},
		{Name: "mirasim-client-version", Usage: "Value sent in x-mirasim-client.", Type: "string"},
	}}, nil
}

func (p *Provider) ExecuteCommandLine(_ context.Context, req pluginapi.CommandLineExecutionRequest) (pluginapi.CommandLineExecutionResponse, error) {
	if !flagBool(req.TriggeredFlags, "mirasim-import") {
		return pluginapi.CommandLineExecutionResponse{}, nil
	}
	settings := p.settings
	if value := flagString(req.Flags, "mirasim-credential-dir"); value != "" {
		settings.CredentialDir = value
	}
	if value := flagString(req.Flags, "mirasim-relay-url"); value != "" {
		settings.RelayURL = value
	}
	if value := flagString(req.Flags, "mirasim-admin-url"); value != "" {
		settings.AdminURL = value
	}
	if value := flagString(req.Flags, "mirasim-client-version"); value != "" {
		settings.ClientVersion = value
	}
	storage, errStorage := credentials.FromSettings(settings)
	if errStorage != nil {
		return commandError(errStorage), nil
	}
	client := p.pool.Client(storage)
	if errValidate := client.Validate(); errValidate != nil {
		return commandError(errValidate), nil
	}
	auth := storage.AuthData("mirasim.json", "mirasim.json", client.NextRefreshAfter(time.Now()))
	stdout := fmt.Sprintf("Imported Mirasim credential directory: %s\nNo token or private-key value was copied into the auth JSON.\n", storage.CredentialDir)
	return pluginapi.CommandLineExecutionResponse{Stdout: []byte(stdout), Auths: []pluginapi.AuthData{auth}}, nil
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
