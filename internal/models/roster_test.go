package models

import (
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
	"testing"
)

func TestRosterOverridesSpecWithoutAddingModels(t *testing.T) {
	models := exposedModels([]mirasim.RemoteModel{{ID: "gpt-6-astra"}, {ID: "claude-sonnet-5"}})
	applyRoster(models, mirasim.ModelRoster{Version: "live", Agents: map[string][]mirasim.ModelSpec{
		"codex": {{ID: "gpt-6-astra", ContextWindow: 1050000, MaxOutput: 64000, Effort: []string{"high", "max", "ultra"}}, {ID: "gpt-not-in-catalog", ContextWindow: 999}},
	}})
	if len(models) != 2 || models[0].ContextLength != 1050000 || models[0].MaxCompletionTokens != 64000 || len(models[0].Thinking.Levels) != 3 {
		t.Fatalf("models=%+v", models)
	}
	if models[1].ContextLength != 1000000 {
		t.Fatal("missing spec erased static metadata")
	}
}

func TestRosterPublishesOnlyTheThinkingFormTheModelAccepts(t *testing.T) {
	models := exposedModels([]mirasim.RemoteModel{{ID: "claude-haiku-4-5"}, {ID: "claude-sonnet-5"}})
	applyRoster(models, mirasim.ModelRoster{Version: "live", Agents: map[string][]mirasim.ModelSpec{
		"claude": {
			{ID: "claude-haiku-4-5", ContextWindow: 200000, Adaptive: false},
			{ID: "claude-sonnet-5", ContextWindow: 1000000, Adaptive: true},
		},
	}})
	if models[0].Thinking == nil || models[0].Thinking.Min != 1024 || models[0].Thinking.Max != 128000 {
		t.Fatalf("non-adaptive model did not publish budget bounds: %+v", models[0].Thinking)
	}
	if models[1].Thinking == nil || models[1].Thinking.Min != 0 || models[1].Thinking.Max != 0 || !models[1].Thinking.DynamicAllowed {
		t.Fatalf("adaptive model published a token budget: %+v", models[1].Thinking)
	}
}
