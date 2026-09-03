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
		if !isExposedModel(model.ID) || len(model.SupportedGenerationMethods) == 0 || model.Type != "claude" || model.ContextLength == 0 || model.MaxCompletionTokens == 0 {
			t.Fatalf("incomplete model metadata: %#v", model)
		}
		if model.SupportedGenerationMethods[0] != "messages" {
			t.Fatalf("Claude model advertises an unverified Responses route: %#v", model)
		}
	}
	byID := make(map[string]pluginapi.ModelInfo, len(resp.Models))
	for _, model := range resp.Models {
		byID[model.ID] = model
	}
	sonnet := byID["claude-sonnet-5"]
	if sonnet.ContextLength != 1000000 || sonnet.MaxCompletionTokens != 128000 || sonnet.Thinking == nil || !sonnet.Thinking.DynamicAllowed || !sonnet.Thinking.ZeroAllowed || len(sonnet.Thinking.Levels) != 1 || sonnet.Thinking.Levels[0] != "high" {
		t.Fatalf("Claude Sonnet 5 metadata = %#v", sonnet)
	}
	haiku := byID["claude-haiku-4-5"]
	if haiku.ContextLength != 200000 || haiku.MaxCompletionTokens != 64000 || haiku.Thinking == nil || haiku.Thinking.Min != 1024 || haiku.Thinking.Max != 128000 || haiku.Thinking.DynamicAllowed {
		t.Fatalf("Claude Haiku 4.5 metadata = %#v", haiku)
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

func TestModelInfoPreservesLiveIdentityAndAddsKnownCapabilities(t *testing.T) {
	model := modelInfo("gpt-5.6-sol", "custom-model", 42, "relay-owner")
	if model.Object != "custom-model" || model.Created != 42 || model.OwnedBy != "relay-owner" {
		t.Fatalf("live identity fields were replaced: %#v", model)
	}
	if model.Type != "openai" || model.DisplayName != "GPT 5.6 Sol" || model.ContextLength != 372000 || model.MaxCompletionTokens != 128000 || model.Thinking == nil {
		t.Fatalf("known GPT metadata was not enriched: %#v", model)
	}
	wantLevels := []string{"low", "medium", "high", "xhigh", "max"}
	if len(model.Thinking.Levels) != len(wantLevels) {
		t.Fatalf("thinking levels = %#v", model.Thinking.Levels)
	}
	for index, level := range wantLevels {
		if model.Thinking.Levels[index] != level {
			t.Fatalf("thinking levels = %#v", model.Thinking.Levels)
		}
	}
}

func TestModelInfoReturnsIndependentMetadata(t *testing.T) {
	first := modelInfo("claude-sonnet-5", "", 0, "")
	first.SupportedParameters[0] = "mutated"
	first.Thinking.Levels[0] = "mutated"
	second := modelInfo("claude-sonnet-5", "", 0, "")
	if second.SupportedParameters[0] == "mutated" || second.Thinking.Levels[0] == "mutated" {
		t.Fatalf("model metadata shares mutable slices: %#v", second)
	}
}
