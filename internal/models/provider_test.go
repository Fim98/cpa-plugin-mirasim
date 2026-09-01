package models

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
)

func TestStaticModelsExposeKnownCatalogFallback(t *testing.T) {
	provider := New(pluginconfig.Defaults(), mirasim.NewPool())
	resp, errModels := provider.StaticModels(context.Background(), pluginapi.StaticModelRequest{})
	if errModels != nil {
		t.Fatalf("StaticModels() error = %v", errModels)
	}
	if resp.Provider != "mirasim" || len(resp.Models) != len(fallbackModelIDs) {
		t.Fatalf("response = %#v", resp)
	}
	for _, model := range resp.Models {
		if model.ID == "" || len(model.SupportedGenerationMethods) == 0 {
			t.Fatalf("incomplete model metadata: %#v", model)
		}
		if model.ID == "claude-sonnet-5" && len(model.SupportedGenerationMethods) != 1 {
			t.Fatalf("Claude model advertises an unverified Responses route: %#v", model)
		}
		if model.ID == "gpt-5.6-sol" && len(model.SupportedGenerationMethods) != 2 {
			t.Fatalf("GPT model is missing Messages or Responses: %#v", model)
		}
	}
}
