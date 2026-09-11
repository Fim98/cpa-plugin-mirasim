package models

import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/mirasim"
	"strings"
)

// Overlay specifications only on models the account's live catalog exposes.
func applyRoster(models []pluginapi.ModelInfo, roster mirasim.ModelRoster) {
	specs := map[string]mirasim.ModelSpec{}
	for _, entries := range roster.Agents {
		for _, spec := range entries {
			specs[spec.ID] = spec
		}
	}
	for i := range models {
		m := &models[i]
		spec, ok := specs[strings.ToLower(m.ID)]
		if !ok {
			continue
		}
		m.ContextLength = spec.ContextWindow
		m.InputTokenLimit = spec.ContextWindow
		if spec.MaxOutput > 0 {
			m.MaxCompletionTokens = spec.MaxOutput
			m.OutputTokenLimit = spec.MaxOutput
		}
		if spec.Label != "" {
			m.DisplayName = spec.Label
		}
		// CPA 7.2.146 has no auto-compaction-ratio field. Do not mislabel it as
		// the model's context limit; compaction remains the caller's responsibility.
		levels := []string{}
		seen := map[string]bool{}
		for _, level := range spec.Effort {
			level = strings.ToLower(strings.TrimSpace(level))
			switch level {
			case "low", "medium", "high", "xhigh", "max":
				if !seen[level] {
					levels = append(levels, level)
					seen[level] = true
				}
			}
		}
		_, known := modelDefinitions[strings.ToLower(m.ID)]
		if spec.Adaptive && m.Type == "claude" && known {
			if m.Thinking == nil {
				m.Thinking = adaptiveRelayThinking()
			}
			m.Thinking.DynamicAllowed = true
			m.SupportedParameters = appendUnique(m.SupportedParameters, "thinking", "output_config")
		}
		if len(levels) > 0 && (m.Type != "claude" || known) {
			if m.Thinking == nil {
				m.Thinking = &pluginapi.ThinkingSupport{}
			}
			m.Thinking.Levels = levels
			m.SupportedParameters = appendUnique(m.SupportedParameters, "thinking")
		}
	}
}

func appendUnique(values []string, extra ...string) []string {
	for _, v := range extra {
		found := false
		for _, old := range values {
			if old == v {
				found = true
				break
			}
		}
		if !found {
			values = append(values, v)
		}
	}
	return values
}
