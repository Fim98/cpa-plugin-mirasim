package thinking

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	wireClaude = "claude"
	wireCodex  = "codex"
)

// ParsedModel contains the provider model name and an optional CPA-compatible
// thinking suffix. ModelName never contains a leading mirasim/ prefix or a
// trailing parenthesized suffix.
type ParsedModel struct {
	LongContext bool
	ModelName   string
	Config      pluginapi.ThinkingConfig
	HasSuffix   bool
	HasConfig   bool
}

// ConfigError is returned when a requested thinking control cannot be
// represented by the Mirasim relay protocol without silently changing it.
type ConfigError struct {
	Code    string
	Message string
}

func (e *ConfigError) Error() string   { return e.Message }
func (e *ConfigError) StatusCode() int { return http.StatusBadRequest }

// Applier exposes Mirasim's provider-specific thinking shapes to CPA.
type Applier struct{}

func NewApplier() *Applier { return &Applier{} }

func (a *Applier) Identifier() string { return "mirasim" }

func (a *Applier) ApplyThinking(_ context.Context, req pluginapi.ThinkingApplyRequest) (pluginapi.PayloadResponse, error) {
	model := ParseModel(req.Model.ID).ModelName
	wire := wireCodex
	if strings.HasPrefix(strings.ToLower(model), "claude-") {
		wire = wireClaude
	}
	body, errApply := ApplyForWire(req.Body, model, wire, req.Config)
	return pluginapi.PayloadResponse{Body: body}, errApply
}

// ParseModel mirrors CPA's final-parenthesized model suffix convention. An
// unknown suffix is still removed from the upstream model ID, but does not
// create a thinking configuration.
func ParseModel(model string) ParsedModel {
	model = strings.TrimSpace(model)
	for strings.HasPrefix(strings.ToLower(model), "mirasim/") {
		model = strings.TrimSpace(model[len("mirasim/"):])
	}
	parsed := ParsedModel{ModelName: model}
	open := strings.LastIndex(model, "(")
	if open < 0 || !strings.HasSuffix(model, ")") {
		return parseContextSelector(parsed)
	}
	parsed.ModelName = strings.TrimSpace(model[:open])
	parsed.HasSuffix = true
	raw := strings.ToLower(strings.TrimSpace(model[open+1 : len(model)-1]))
	switch raw {
	case "none":
		parsed.Config = pluginapi.ThinkingConfig{Mode: "none"}
		parsed.HasConfig = true
	case "auto", "-1":
		parsed.Config = pluginapi.ThinkingConfig{Mode: "auto", Budget: -1}
		parsed.HasConfig = true
	case "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		parsed.Config = pluginapi.ThinkingConfig{Mode: "level", Level: raw}
		parsed.HasConfig = true
	default:
		budget, errBudget := strconv.Atoi(raw)
		if errBudget == nil && budget >= 0 {
			if budget == 0 {
				parsed.Config = pluginapi.ThinkingConfig{Mode: "none"}
			} else {
				parsed.Config = pluginapi.ThinkingConfig{Mode: "budget", Budget: budget}
			}
			parsed.HasConfig = true
		}
	}
	return parseContextSelector(parsed)
}

func parseContextSelector(parsed ParsedModel) ParsedModel {
	if strings.HasPrefix(strings.ToLower(parsed.ModelName), "claude-") && strings.HasSuffix(strings.ToLower(parsed.ModelName), "[1m]") {
		parsed.ModelName = strings.TrimSpace(parsed.ModelName[:len(parsed.ModelName)-4])
		parsed.LongContext = true
	}
	return parsed
}

// ApplyForWire applies a canonical thinking configuration after request
// translation, when the executor knows the actual Mirasim wire protocol.
func ApplyForWire(body []byte, model, wire string, config pluginapi.ThinkingConfig) ([]byte, error) {
	body = validBody(body)
	config = normalizeConfig(config)
	if config.Level == "ultra" {
		return nil, workflowError()
	}
	if err := ValidateWorkflowRequest(body, model); err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case wireClaude:
		return applyClaude(body, model, config)
	case wireCodex, "openai-response":
		return applyCodex(body, config), nil
	default:
		return append([]byte(nil), body...), nil
	}
}

func workflowError() error {
	return &ConfigError{Code: "mirasim_client_workflow_required", Message: "Mirasim ultra requires the official client's workflow orchestration; use max for a single API request"}
}

func ValidateWorkflowRequest(body []byte, model string) error {
	if ParseModel(model).Config.Level == "ultra" {
		return workflowError()
	}
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, path).String()), "ultra") {
			return workflowError()
		}
	}
	return nil
}

func normalizeConfig(config pluginapi.ThinkingConfig) pluginapi.ThinkingConfig {
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	config.Level = strings.ToLower(strings.TrimSpace(config.Level))
	if config.Mode == "budget" && config.Budget <= 0 {
		config.Mode = "none"
		config.Budget = 0
	}
	return config
}

func applyClaude(body []byte, model string, config pluginapi.ThinkingConfig) ([]byte, error) {
	model = strings.ToLower(strings.TrimSpace(model))
	switch config.Mode {
	case "none":
		body = setString(body, "thinking.type", "disabled")
		body = deletePath(body, "thinking.budget_tokens")
		return deleteClaudeEffort(body), nil
	case "auto":
		if supportsAdaptiveClaude(model) {
			body = setString(body, "thinking.type", "adaptive")
			body = deletePath(body, "thinking.budget_tokens")
			return deleteClaudeEffort(body), nil
		}
		return applyManualClaude(body, 1024)
	case "level":
		if supportsAdaptiveClaude(model) {
			if !isAdaptiveClaudeEffort(config.Level) {
				return body, &ConfigError{
					Code:    "mirasim_claude_effort_invalid",
					Message: fmt.Sprintf("unsupported Claude thinking effort %q for %s", config.Level, model),
				}
			}
			body = setString(body, "thinking.type", "adaptive")
			body = deletePath(body, "thinking.budget_tokens")
			return setString(body, "output_config.effort", config.Level), nil
		}
		budget, okBudget := levelToBudget(config.Level)
		if !okBudget {
			return body, &ConfigError{Code: "mirasim_thinking_level_invalid", Message: fmt.Sprintf("unsupported thinking level %q", config.Level)}
		}
		return applyManualClaude(body, budget)
	case "budget":
		if supportsAdaptiveOnlyClaude(model) {
			return body, &ConfigError{
				Code:    "mirasim_claude_budget_unsupported",
				Message: fmt.Sprintf("Claude model %s requires adaptive thinking and does not accept a fixed token budget", model),
			}
		}
		return applyManualClaude(body, config.Budget)
	default:
		return body, nil
	}
}

func applyManualClaude(body []byte, budget int) ([]byte, error) {
	if budget < 1024 {
		budget = 1024
	}
	if maxTokens := int(gjson.GetBytes(body, "max_tokens").Int()); maxTokens > 0 && budget >= maxTokens {
		budget = maxTokens - 1
		if budget < 1024 {
			return body, &ConfigError{
				Code:    "mirasim_claude_budget_out_of_range",
				Message: "Claude thinking budget must be at least 1024 and lower than max_tokens",
			}
		}
	}
	body = setString(body, "thinking.type", "enabled")
	body = setInt(body, "thinking.budget_tokens", budget)
	return deleteClaudeEffort(body), nil
}

func deleteClaudeEffort(body []byte) []byte {
	body = deletePath(body, "output_config.effort")
	outputConfig := gjson.GetBytes(body, "output_config")
	if outputConfig.Exists() && outputConfig.IsObject() && len(outputConfig.Map()) == 0 {
		body = deletePath(body, "output_config")
	}
	return body
}

func applyCodex(body []byte, config pluginapi.ThinkingConfig) []byte {
	effort := ""
	switch config.Mode {
	case "none":
		effort = "none"
	case "auto":
		effort = "auto"
	case "level":
		effort = config.Level
	case "budget":
		effort = budgetToLevel(config.Budget)
	}
	if effort == "" {
		return body
	}
	body = setString(body, "reasoning.effort", effort)
	return deletePath(body, "reasoning_effort")
}

func supportsAdaptiveClaude(model string) bool {
	return supportsAdaptiveOnlyClaude(model) ||
		strings.HasPrefix(model, "claude-haiku-4-5") ||
		strings.Contains(model, "-4-6")
}

func supportsAdaptiveOnlyClaude(model string) bool {
	return strings.HasPrefix(model, "claude-fable-5") ||
		strings.HasPrefix(model, "claude-mythos-") ||
		strings.HasPrefix(model, "claude-opus-5") ||
		strings.Contains(model, "claude-opus-4-7") ||
		strings.Contains(model, "claude-opus-4-8") ||
		strings.HasPrefix(model, "claude-sonnet-5")
}

func isAdaptiveClaudeEffort(level string) bool {
	switch level {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func levelToBudget(level string) (int, bool) {
	switch level {
	case "minimal":
		return 512, true
	case "low":
		return 1024, true
	case "medium":
		return 8192, true
	case "high":
		return 24576, true
	case "xhigh":
		return 32768, true
	case "max":
		return 128000, true
	default:
		return 0, false
	}
}

func budgetToLevel(budget int) string {
	switch {
	case budget < 0:
		return ""
	case budget == 0:
		return "none"
	case budget <= 512:
		return "minimal"
	case budget <= 1024:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 24576:
		return "high"
	default:
		return "xhigh"
	}
}

func validBody(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return []byte(`{}`)
	}
	return append([]byte(nil), body...)
}

func setString(body []byte, path, value string) []byte {
	updated, errSet := sjson.SetBytes(body, path, value)
	if errSet != nil {
		return body
	}
	return updated
}

func setInt(body []byte, path string, value int) []byte {
	updated, errSet := sjson.SetBytes(body, path, value)
	if errSet != nil {
		return body
	}
	return updated
}

func deletePath(body []byte, path string) []byte {
	updated, errDelete := sjson.DeleteBytes(body, path)
	if errDelete != nil {
		return body
	}
	return updated
}

var _ pluginapi.ThinkingApplier = (*Applier)(nil)
