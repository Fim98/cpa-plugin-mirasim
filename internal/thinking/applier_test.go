package thinking

import (
	"context"
	"errors"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

func TestUltraCannotSilentlyBecomeOrdinaryCompletion(t *testing.T) {
	for _, model := range []string{"gpt-6-astra(ultra)", "claude-sonnet-5(ultra)"} {
		parsed := ParseModel(model)
		_, err := ApplyForWire([]byte("{}"), parsed.ModelName, "codex", parsed.Config)
		var e *ConfigError
		if !errors.As(err, &e) || e.Code != "mirasim_client_workflow_required" {
			t.Fatalf("%s: %v", model, err)
		}
	}
	for _, body := range []string{`{"reasoning":{"effort":"ultra"}}`, `{"output_config":{"effort":"ultra"}}`, `{"reasoning_effort":"ultra"}`} {
		if ValidateWorkflowRequest([]byte(body), "gpt-6-astra") == nil {
			t.Fatal("workflow request accepted")
		}
	}
	if ValidateWorkflowRequest([]byte(`{"reasoning":{"effort":"max"}}`), "gpt-6-astra") != nil {
		t.Fatal("max rejected")
	}
}

func TestParseModelUsesCPASuffixConvention(t *testing.T) {
	tests := []struct {
		input     string
		model     string
		mode      string
		budget    int
		level     string
		hasSuffix bool
		hasConfig bool
	}{
		{input: "mirasim/claude-sonnet-5(auto)", model: "claude-sonnet-5", mode: "auto", budget: -1, hasSuffix: true, hasConfig: true},
		{input: "gpt-5.6-sol(high)", model: "gpt-5.6-sol", mode: "level", level: "high", hasSuffix: true, hasConfig: true},
		{input: "gpt-5.6-sol(8192)", model: "gpt-5.6-sol", mode: "budget", budget: 8192, hasSuffix: true, hasConfig: true},
		{input: "claude-haiku-4-5(0)", model: "claude-haiku-4-5", mode: "none", hasSuffix: true, hasConfig: true},
		{input: "claude-sonnet-5(custom)", model: "claude-sonnet-5", hasSuffix: true, hasConfig: false},
		{input: "claude-sonnet-5", model: "claude-sonnet-5"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got := ParseModel(test.input)
			if got.ModelName != test.model || got.Config.Mode != test.mode || got.Config.Budget != test.budget || got.Config.Level != test.level || got.HasSuffix != test.hasSuffix || got.HasConfig != test.hasConfig {
				t.Fatalf("ParseModel(%q) = %#v", test.input, got)
			}
		})
	}
}

func TestApplyForWireUsesAdaptiveClaudeControls(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","max_tokens":4096,"messages":[],"output_config":{"effort":"low","format":{"type":"json_schema"}}}`)
	auto, errAuto := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "auto", Budget: -1})
	if errAuto != nil {
		t.Fatal(errAuto)
	}
	if got := gjson.GetBytes(auto, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, body = %s", got, auto)
	}
	if gjson.GetBytes(auto, "thinking.budget_tokens").Exists() || gjson.GetBytes(auto, "output_config.effort").Exists() || gjson.GetBytes(auto, "output_config.format.type").String() != "json_schema" {
		t.Fatalf("adaptive auto request was not normalized safely: %s", auto)
	}

	disabled, errDisabled := ApplyForWire(auto, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "none"})
	if errDisabled != nil {
		t.Fatal(errDisabled)
	}
	if got := gjson.GetBytes(disabled, "thinking.type").String(); got != "disabled" {
		t.Fatalf("thinking.type = %q, body = %s", got, disabled)
	}

	high, errHigh := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "high"})
	if errHigh != nil || gjson.GetBytes(high, "thinking.type").String() != "adaptive" || gjson.GetBytes(high, "output_config.effort").String() != "high" {
		t.Fatalf("high adaptive body = %s, error = %v", high, errHigh)
	}
}

func TestApplyForWireMapsAdaptiveClaudeEffort(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","max_tokens":4096,"messages":[]}`)
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		out, errApply := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: effort})
		if errApply != nil || gjson.GetBytes(out, "thinking.type").String() != "adaptive" || gjson.GetBytes(out, "output_config.effort").String() != effort {
			t.Fatalf("effort %q body = %s, error = %v", effort, out, errApply)
		}
	}

	_, errApply := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "minimal"})
	var configErr *ConfigError
	if !errors.As(errApply, &configErr) || configErr.Code != "mirasim_claude_effort_invalid" || configErr.StatusCode() != 400 {
		t.Fatalf("error = %#v", errApply)
	}
}

func TestApplyForWireNormalizesManualClaudeBudget(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":2048,"messages":[],"output_config":{"effort":"high","format":{"type":"json_schema"}}}`)
	out, errApply := ApplyForWireWithShape(body, "claude-haiku-4-5", wireClaude, pluginapi.ThinkingConfig{Mode: "budget", Budget: 4096}, ShapeBudget)
	if errApply != nil {
		t.Fatal(errApply)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "enabled" {
		t.Fatalf("thinking.type = %q, body = %s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.budget_tokens").Int(); got != 2047 {
		t.Fatalf("thinking.budget_tokens = %d, body = %s", got, out)
	}
	if gjson.GetBytes(out, "output_config.effort").Exists() || gjson.GetBytes(out, "output_config.format.type").String() != "json_schema" {
		t.Fatalf("output_config was not preserved without effort: %s", out)
	}
}

func TestApplyForWireKeepsUnrosteredClaudeOnTheEffortForm(t *testing.T) {
	body := []byte(`{"model":"claude-opus-9","max_tokens":4096,"messages":[]}`)
	// A Claude model released after this build has no static entry and no
	// roster entry. The relay generation is uniformly adaptive, so it must not
	// fall back to a token budget the Messages mount would reject.
	out, errApply := ApplyForWire(body, "claude-opus-9", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "high"})
	if errApply != nil || gjson.GetBytes(out, "thinking.type").String() != "adaptive" || gjson.GetBytes(out, "output_config.effort").String() != "high" {
		t.Fatalf("body = %s, error = %v", out, errApply)
	}
	if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
		t.Fatalf("effort form carried a token budget: %s", out)
	}

	for _, model := range []string{"claude-haiku-4-5", "claude-opus-4-6", "claude-opus-9"} {
		_, errBudget := ApplyForWire(body, model, wireClaude, pluginapi.ThinkingConfig{Mode: "budget", Budget: 4096})
		var configErr *ConfigError
		if !errors.As(errBudget, &configErr) || configErr.Code != "mirasim_claude_budget_unsupported" {
			t.Fatalf("%s accepted a token budget: %v", model, errBudget)
		}
	}
}

func TestApplyForWireFollowsRosterShape(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","max_tokens":16384,"messages":[]}`)
	budget, errBudget := ApplyForWireWithShape(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "medium"}, ShapeBudget)
	if errBudget != nil || gjson.GetBytes(budget, "thinking.type").String() != "enabled" || gjson.GetBytes(budget, "thinking.budget_tokens").Int() != 8192 {
		t.Fatalf("budget body = %s, error = %v", budget, errBudget)
	}
	if gjson.GetBytes(budget, "output_config.effort").Exists() {
		t.Fatalf("budget form carried an effort string: %s", budget)
	}

	adaptive, errAdaptive := ApplyForWireWithShape(body, "claude-haiku-4-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "medium"}, ShapeAdaptive)
	if errAdaptive != nil || gjson.GetBytes(adaptive, "thinking.type").String() != "adaptive" || gjson.GetBytes(adaptive, "output_config.effort").String() != "medium" {
		t.Fatalf("adaptive body = %s, error = %v", adaptive, errAdaptive)
	}
}

func TestApplyForWireMapsCodexLevelsAndBudgets(t *testing.T) {
	level, errLevel := ApplyForWire([]byte(`{"model":"gpt-5.6-sol"}`), "gpt-5.6-sol", wireCodex, pluginapi.ThinkingConfig{Mode: "level", Level: "max"})
	if errLevel != nil || gjson.GetBytes(level, "reasoning.effort").String() != "max" {
		t.Fatalf("level body = %s, error = %v", level, errLevel)
	}
	budget, errBudget := ApplyForWire([]byte(`{"reasoning_effort":"low"}`), "gpt-5.6-sol", wireCodex, pluginapi.ThinkingConfig{Mode: "budget", Budget: 8192})
	if errBudget != nil || gjson.GetBytes(budget, "reasoning.effort").String() != "medium" || gjson.GetBytes(budget, "reasoning_effort").Exists() {
		t.Fatalf("budget body = %s, error = %v", budget, errBudget)
	}
}

func TestApplierSelectsModelFamily(t *testing.T) {
	resp, errApply := NewApplier().ApplyThinking(context.Background(), pluginapi.ThinkingApplyRequest{
		Model:  pluginapi.ModelInfo{ID: "gpt-5.6-terra"},
		Config: pluginapi.ThinkingConfig{Mode: "level", Level: "high"},
		Body:   []byte(`{"model":"gpt-5.6-terra"}`),
	})
	if errApply != nil || gjson.GetBytes(resp.Body, "reasoning.effort").String() != "high" {
		t.Fatalf("response = %s, error = %v", resp.Body, errApply)
	}
}
