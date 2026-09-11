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
	if len(models) != 2 || models[0].ContextLength != 1050000 || models[0].MaxCompletionTokens != 64000 || len(models[0].Thinking.Levels) != 2 {
		t.Fatalf("models=%+v", models)
	}
	if models[1].ContextLength != 1000000 {
		t.Fatal("missing spec erased static metadata")
	}
}
