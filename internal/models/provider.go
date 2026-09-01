package models

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/credentials"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

var fallbackModelIDs = []string{
	"claude-fable-5",
	"claude-haiku-4-5",
	"claude-opus-4-8",
	"claude-opus-5",
	"claude-sonnet-5",
}

type Provider struct {
	settings pluginconfig.Settings
	pool     *mirasim.Pool
}

func New(settings pluginconfig.Settings, pool *mirasim.Pool) *Provider {
	return &Provider{settings: settings, pool: pool}
}

func (p *Provider) StaticModels(context.Context, pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: credentials.Provider, Models: fallbackModels()}, nil
}

func (p *Provider) ModelsForAuth(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	storage, errParse := credentials.Parse(req.StorageJSON, p.settings)
	if errParse != nil {
		return pluginapi.ModelResponse{}, errParse
	}
	if storage == nil {
		return pluginapi.ModelResponse{Provider: credentials.Provider, Models: fallbackModels()}, nil
	}
	catalog, errCatalog := p.pool.Client(*storage).ListModels(ctx, req.HTTPClient)
	if errCatalog != nil {
		return pluginapi.ModelResponse{}, errCatalog
	}
	return pluginapi.ModelResponse{Provider: credentials.Provider, Models: exposedModels(catalog.Models)}, nil
}

func fallbackModels() []pluginapi.ModelInfo {
	models := make([]pluginapi.ModelInfo, 0, len(fallbackModelIDs))
	for _, id := range fallbackModelIDs {
		if !isExposedModel(id) {
			continue
		}
		models = append(models, modelInfo(id, "model", 0, "mirasim"))
	}
	return models
}

func exposedModels(catalog []mirasim.RemoteModel) []pluginapi.ModelInfo {
	models := make([]pluginapi.ModelInfo, 0, len(catalog))
	for _, remote := range catalog {
		if !isExposedModel(remote.ID) {
			continue
		}
		models = append(models, modelInfo(remote.ID, remote.Object, remote.Created, remote.OwnedBy))
	}
	return models
}

func isExposedModel(id string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "claude-")
}

func modelInfo(id, object string, created int64, owner string) pluginapi.ModelInfo {
	id = strings.TrimSpace(id)
	if object == "" {
		object = "model"
	}
	if owner == "" {
		owner = "mirasim"
	}
	return pluginapi.ModelInfo{
		ID:                         id,
		Object:                     object,
		Created:                    created,
		OwnedBy:                    owner,
		Type:                       "chat",
		DisplayName:                id,
		Name:                       id,
		Description:                id + " via Mirasim",
		SupportedGenerationMethods: []string{"messages"},
		SupportedInputModalities:   []string{"text"},
		SupportedOutputModalities:  []string{"text"},
	}
}
