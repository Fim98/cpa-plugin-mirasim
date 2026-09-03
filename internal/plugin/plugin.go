package plugin

import (
	"context"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/auth"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/executor"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/management"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/models"
	thinkingpkg "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/thinking"
)

type MirasimPlugin struct {
	auth       *auth.Provider
	models     *models.Provider
	executor   *executor.Executor
	management *management.Handler
	thinking   *thinkingpkg.Applier
}

func Build(configYAML []byte) pluginapi.Plugin {
	settings := pluginconfig.Parse(configYAML)
	pool := mirasim.NewPool()
	authProvider := auth.New(settings, pool)
	p := &MirasimPlugin{
		auth:       authProvider,
		models:     models.New(settings, pool),
		executor:   executor.New(settings, pool),
		management: management.New(settings, pool, authProvider),
		thinking:   thinkingpkg.NewApplier(),
	}
	return pluginapi.Plugin{
		Metadata: pluginapi.Metadata{
			Name:             "Mirasim Provider",
			Version:          "0.5.0",
			Author:           "router-for-me",
			GitHubRepository: "https://github.com/router-for-me/cpa-plugin-mirasim",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "relay-url", Type: pluginapi.ConfigFieldTypeString, Description: "Mirasim relay base URL."},
				{Name: "admin-url", Type: pluginapi.ConfigFieldTypeString, Description: "Mirasim authentication service base URL."},
				{Name: "client-version", Type: pluginapi.ConfigFieldTypeString, Description: "Value sent in x-mirasim-client."},
				{Name: "oauth-public-base-url", Type: pluginapi.ConfigFieldTypeString, Description: "Externally reachable CPA base URL for Mirasim OAuth callbacks."},
			},
		},
		Capabilities: pluginapi.Capabilities{
			AuthProvider:          p,
			ModelProvider:         p,
			Executor:              p,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeOAuth,
			ExecutorInputFormats:  append([]string(nil), executor.SupportedFormats...),
			ExecutorOutputFormats: append([]string(nil), executor.SupportedFormats...),
			ThinkingApplier:       p,
			CommandLinePlugin:     p,
			ManagementAPI:         p,
		},
	}
}

func (p *MirasimPlugin) Identifier() string { return credentials.Provider }

func (p *MirasimPlugin) ParseAuth(ctx context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	return p.auth.ParseAuth(ctx, req)
}

func (p *MirasimPlugin) StartLogin(ctx context.Context, req pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	return p.auth.StartLogin(ctx, req)
}

func (p *MirasimPlugin) PollLogin(ctx context.Context, req pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	return p.auth.PollLogin(ctx, req)
}

func (p *MirasimPlugin) RefreshAuth(ctx context.Context, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	return p.auth.RefreshAuth(ctx, req)
}

func (p *MirasimPlugin) StaticModels(ctx context.Context, req pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return p.models.StaticModels(ctx, req)
}

func (p *MirasimPlugin) ModelsForAuth(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	return p.models.ModelsForAuth(ctx, req)
}

func (p *MirasimPlugin) Execute(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	return p.executor.Execute(ctx, req)
}

func (p *MirasimPlugin) ExecuteStream(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
	return p.executor.ExecuteStream(ctx, req)
}

func (p *MirasimPlugin) CountTokens(ctx context.Context, req pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
	return p.executor.CountTokens(ctx, req)
}

func (p *MirasimPlugin) HttpRequest(ctx context.Context, req pluginapi.ExecutorHTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	return p.executor.HttpRequest(ctx, req)
}

func (p *MirasimPlugin) ApplyThinking(ctx context.Context, req pluginapi.ThinkingApplyRequest) (pluginapi.PayloadResponse, error) {
	return p.thinking.ApplyThinking(ctx, req)
}

func (p *MirasimPlugin) RegisterCommandLine(ctx context.Context, req pluginapi.CommandLineRegistrationRequest) (pluginapi.CommandLineRegistrationResponse, error) {
	return p.auth.RegisterCommandLine(ctx, req)
}

func (p *MirasimPlugin) ExecuteCommandLine(ctx context.Context, req pluginapi.CommandLineExecutionRequest) (pluginapi.CommandLineExecutionResponse, error) {
	return p.auth.ExecuteCommandLine(ctx, req)
}

func (p *MirasimPlugin) RegisterManagement(ctx context.Context, req pluginapi.ManagementRegistrationRequest) (pluginapi.ManagementRegistrationResponse, error) {
	return p.management.RegisterManagement(ctx, req)
}

func (p *MirasimPlugin) HandleManagement(ctx context.Context, req pluginapi.ManagementRequest, host management.HostServices) (pluginapi.ManagementResponse, error) {
	return p.management.HandleWithHost(ctx, req, host)
}

var _ pluginapi.AuthProvider = (*MirasimPlugin)(nil)
var _ pluginapi.ModelProvider = (*MirasimPlugin)(nil)
var _ pluginapi.ProviderExecutor = (*MirasimPlugin)(nil)
var _ pluginapi.ThinkingApplier = (*MirasimPlugin)(nil)
var _ pluginapi.CommandLinePlugin = (*MirasimPlugin)(nil)
var _ pluginapi.ManagementAPI = (*MirasimPlugin)(nil)
