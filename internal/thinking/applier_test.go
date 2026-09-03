package thinking

import (
	"context"
	"errors"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

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
	body := []byte(`{"model":"claude-sonnet-5","max_tokens":4096,"messages":[]}`)
	auto, errAuto := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "auto", Budget: -1})
	if errAuto != nil {
		t.Fatal(errAuto)
	}
	if got := gjson.GetBytes(auto, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, body = %s", got, auto)
	}
	if gjson.GetBytes(auto, "thinking.budget_tokens").Exists() || gjson.GetBytes(auto, "output_config").Exists() {
		t.Fatalf("adaptive request contains unsupported fields: %s", auto)
	}

	disabled, errDisabled := ApplyForWire(auto, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "none"})
	if errDisabled != nil {
		t.Fatal(errDisabled)
	}
	if got := gjson.GetBytes(disabled, "thinking.type").String(); got != "disabled" {
		t.Fatalf("thinking.type = %q, body = %s", got, disabled)
	}

	high, errHigh := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "high"})
	if errHigh != nil || gjson.GetBytes(high, "thinking.type").String() != "adaptive" {
		t.Fatalf("high adaptive body = %s, error = %v", high, errHigh)
	}
}

func TestApplyForWireRejectsUnrepresentableAdaptiveClaudeEffort(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","max_tokens":4096,"messages":[]}`)
	_, errApply := ApplyForWire(body, "claude-sonnet-5", wireClaude, pluginapi.ThinkingConfig{Mode: "level", Level: "low"})
	var configErr *ConfigError
	if !errors.As(errApply, &configErr) || configErr.Code != "mirasim_claude_effort_unsupported" || configErr.StatusCode() != 400 {
		t.Fatalf("error = %#v", errApply)
	}
}

func TestApplyForWireNormalizesManualClaudeBudget(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":2048,"messages":[],"output_config":{"effort":"high"}}`)
	out, errApply := ApplyForWire(body, "claude-haiku-4-5", wireClaude, pluginapi.ThinkingConfig{Mode: "budget", Budget: 4096})
	if errApply != nil {
		t.Fatal(errApply)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "enabled" {
		t.Fatalf("thinking.type = %q, body = %s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.budget_tokens").Int(); got != 2047 {
		t.Fatalf("thinking.budget_tokens = %d, body = %s", got, out)
	}
	if gjson.GetBytes(out, "output_config").Exists() {
		t.Fatalf("output_config was not removed: %s", out)
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
