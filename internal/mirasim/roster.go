package mirasim

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"net/http"
	"strings"
	"time"
)

const rosterPath = "/v1/model-roster"

type ModelSpec struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	ContextWindow    int64    `json:"contextWindow"`
	MaxOutput        int64    `json:"maxOutput"`
	AutoCompactRatio float64  `json:"autoCompactRatio"`
	Effort           []string `json:"effort"`
	Adaptive         bool     `json:"adaptive"`
}

type ModelRoster struct {
	Version string                 `json:"version"`
	Agents  map[string][]ModelSpec `json:"agents"`
}

func parseRoster(raw []byte) (ModelRoster, error) {
	var envelope struct {
		Version string                       `json:"version"`
		Agents  map[string][]json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || strings.TrimSpace(envelope.Version) == "" {
		return ModelRoster{}, fmt.Errorf("invalid Mirasim model roster")
	}
	roster := ModelRoster{Version: envelope.Version, Agents: make(map[string][]ModelSpec)}
	for _, family := range []string{"claude", "codex"} {
		seen := map[string]bool{}
		for _, entry := range envelope.Agents[family] {
			var spec ModelSpec
			if json.Unmarshal(entry, &spec) != nil {
				continue
			}
			spec.ID = strings.ToLower(strings.TrimSpace(spec.ID))
			prefix := "claude-"
			if family == "codex" {
				prefix = "gpt-"
			}
			if !strings.HasPrefix(spec.ID, prefix) || seen[spec.ID] || spec.ContextWindow <= 0 {
				continue
			}
			if spec.MaxOutput < 0 {
				spec.MaxOutput = 0
			}
			if spec.AutoCompactRatio <= 0 || spec.AutoCompactRatio > 1 {
				spec.AutoCompactRatio = 0
			}
			seen[spec.ID] = true
			roster.Agents[family] = append(roster.Agents[family], spec)
		}
	}
	if len(roster.Agents) == 0 {
		return ModelRoster{}, fmt.Errorf("Mirasim model roster contains no valid supported models")
	}
	return roster, nil
}

// ThinkingAdaptive reports the signed roster's adaptive flag for one model.
// The second result is false when the roster carries no entry for it, which
// leaves the upstream thinking form to the caller's default.
func (r ModelRoster) ThinkingAdaptive(modelID string) (bool, bool) {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return false, false
	}
	for _, specs := range r.Agents {
		for _, spec := range specs {
			if spec.ID == modelID {
				return spec.Adaptive, true
			}
		}
	}
	return false, false
}

func (r ModelRoster) Clone() ModelRoster {
	out := ModelRoster{Version: r.Version, Agents: make(map[string][]ModelSpec)}
	for k, v := range r.Agents {
		for _, spec := range v {
			spec.Effort = append([]string(nil), spec.Effort...)
			out.Agents[k] = append(out.Agents[k], spec)
		}
	}
	return out
}

// CachedModelRoster returns the roster already observed for this credential
// without issuing a request. Request paths read the roster through this so a
// cold or unreachable roster never adds latency to an inference call.
func (c *Client) CachedModelRoster() ModelRoster {
	c.rosterMu.Lock()
	defer c.rosterMu.Unlock()
	return c.roster.Clone()
}

// Optional metadata is cached on this credential's client, never globally.
// Failure leaves discovery usable, but never manufactures catalog membership.
func (c *Client) ModelRoster(ctx context.Context, host pluginapi.HostHTTPClient) ModelRoster {
	c.rosterMu.Lock()
	defer c.rosterMu.Unlock()
	now := c.nowTime()
	if now.Before(c.rosterNextCheck) {
		return c.roster.Clone()
	}
	c.rosterNextCheck = now.Add(time.Minute)
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resp, err := c.doControl(probeCtx, host, http.MethodGet, rosterPath, nil, nil, nil, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		return c.roster.Clone()
	}
	roster, err := parseRoster(resp.Body)
	if err != nil {
		return c.roster.Clone()
	}
	c.roster = roster
	c.rosterNextCheck = now.Add(10 * time.Minute)
	return roster.Clone()
}
