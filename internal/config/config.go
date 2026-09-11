package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultRelayURL      = "https://relay.mirasim.ai"
	DefaultAdminURL      = "https://auth.mirasim.ai"
	DefaultClientVersion = "0.0.272"
)

// Settings contains public provider and OAuth callback defaults. Credential
// material is supplied only by OAuth and persisted by CLIProxyAPI in auth-dir.
type Settings struct {
	Collect       *bool  `yaml:"collect"`
	Locale        string `yaml:"locale"`
	RelayURL      string `yaml:"relay-url"`
	AdminURL      string `yaml:"admin-url"`
	ClientVersion string `yaml:"client-version"`
	// OAuthPublicBaseURL is the externally reachable CPA origin used for the
	// browser callback. It is intentionally not persisted in auth records.
	OAuthPublicBaseURL string `yaml:"oauth-public-base-url"`
}

type rootConfig struct {
	Plugins struct {
		Configs map[string]Settings `yaml:"configs"`
	} `yaml:"plugins"`
}

// Parse accepts both CLIProxyAPI's runtime plugin subconfiguration and the
// legacy full-config shape used by early tests and direct embedders.
func Parse(raw []byte) Settings {
	settings := Defaults()
	if len(raw) == 0 {
		return settings
	}
	var root rootConfig
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return settings
	}
	configured, ok := root.Plugins.Configs["mirasim"]
	if !ok {
		if err := yaml.Unmarshal(raw, &configured); err != nil {
			return settings
		}
	}
	return merge(settings, configured)
}

func merge(settings, configured Settings) Settings {
	if configured.Collect != nil {
		value := *configured.Collect
		settings.Collect = &value
	}
	if value := strings.TrimSpace(configured.Locale); value != "" {
		settings.Locale = value
	}
	if value := cleanURL(configured.RelayURL); value != "" {
		settings.RelayURL = value
	}
	if value := cleanURL(configured.AdminURL); value != "" {
		settings.AdminURL = value
	}
	if value := strings.TrimSpace(configured.ClientVersion); value != "" {
		settings.ClientVersion = value
	}
	if value := cleanURL(configured.OAuthPublicBaseURL); value != "" {
		settings.OAuthPublicBaseURL = value
	}
	return settings
}

// Defaults resolves environment overrides and safe provider defaults.
func Defaults() Settings {
	return Settings{
		Collect:            optionalBool(os.Getenv("MIRASIM_COLLECT")),
		Locale:             strings.TrimSpace(os.Getenv("MIRASIM_LOCALE")),
		RelayURL:           firstNonEmpty(cleanURL(os.Getenv("MIRASIM_RELAY_URL")), DefaultRelayURL),
		AdminURL:           firstNonEmpty(cleanURL(os.Getenv("MIRASIM_ADMIN_URL")), DefaultAdminURL),
		ClientVersion:      firstNonEmpty(strings.TrimSpace(os.Getenv("MIRASIM_CLIENT_VERSION")), DefaultClientVersion),
		OAuthPublicBaseURL: cleanURL(os.Getenv("MIRASIM_OAUTH_PUBLIC_BASE_URL")),
	}
}

func optionalBool(value string) *bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off":
		value := false
		return &value
	case "true", "1", "on":
		value := true
		return &value
	}
	return nil
}

func cleanURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
