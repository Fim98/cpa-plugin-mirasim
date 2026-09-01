package models

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

func TestStaticModelsExposeOnlyClaudeFallback(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errModels := provider.StaticModels(context.Background(), pluginapi.StaticModelRequest{})
	if errModels != nil {
		t.Fatalf("StaticModels() error = %v", errModels)
	}
	if resp.Provider != "mirasim" || len(resp.Models) != len(fallbackModelIDs) {
		t.Fatalf("response = %#v", resp)
	}
	for _, model := range resp.Models {
		if !isExposedModel(model.ID) || len(model.SupportedGenerationMethods) == 0 {
			t.Fatalf("incomplete model metadata: %#v", model)
		}
		if len(model.SupportedGenerationMethods) != 1 || model.SupportedGenerationMethods[0] != "messages" {
			t.Fatalf("Claude model advertises an unverified Responses route: %#v", model)
		}
	}
}

func TestExposedModelsFiltersGPTCatalogEntries(t *testing.T) {
	models := exposedModels([]mirasim.RemoteModel{
		{ID: "claude-sonnet-5", Object: "model", OwnedBy: "anthropic"},
		{ID: "gpt-5.6-sol", Object: "model", OwnedBy: "openai"},
		{ID: " Claude-Opus-5 ", Object: "model", OwnedBy: "anthropic"},
	})

	if len(models) != 2 {
		t.Fatalf("exposedModels() returned %d models, want 2: %#v", len(models), models)
	}
	for _, model := range models {
		if !isExposedModel(model.ID) {
			t.Fatalf("non-Claude model was exposed: %#v", model)
		}
	}
}
