package models

import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"strings"
)

// Register selectors with CPA as well as stripping them in the executor;
// otherwise model routing would reject the selector before reaching the plugin.
func withLongContextAliases(models []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	out := append([]pluginapi.ModelInfo(nil), models...)
	seen := map[string]bool{}
	for _, m := range models {
		seen[strings.ToLower(m.ID)] = true
	}
	for _, m := range models {
		if !strings.HasPrefix(strings.ToLower(m.ID), "claude-") || strings.Contains(m.ID, "[") || m.ContextLength < 1000000 {
			continue
		}
		id := m.ID + "[1m]"
		if seen[strings.ToLower(id)] {
			continue
		}
		m.ID = id
		m.Name = id
		m.DisplayName += " [1m]"
		m.Thinking = cloneThinking(m.Thinking)
		out = append(out, m)
		seen[strings.ToLower(id)] = true
	}
	return out
}
