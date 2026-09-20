package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultRelayURL      = "https://relay.mirasim.ai"
	DefaultAdminURL      = "https://auth.mirasim.ai"
	DefaultClientVersion = "0.0.310"
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
	// HTTP1Only asks the host transport to skip HTTP/2 negotiation for relay
	// calls. On by default: the official client offers only http/1.1 in its TLS
	// ALPN, even though the relay itself will negotiate h2 when offered it.
	HTTP1Only *bool `yaml:"http1-only"`
	// LowercaseRelayHeaders asks the host to put relay header names on the wire
	// in lower case rather than Go's canonical form. On by default, because the
	// official client spells every header lower case. The host implements this
	// by rewriting the request line, which requires HTTP/1.1.
	LowercaseRelayHeaders *bool `yaml:"lowercase-relay-headers"`
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
	if configured.HTTP1Only != nil {
		value := *configured.HTTP1Only
		settings.HTTP1Only = &value
	}
	if configured.LowercaseRelayHeaders != nil {
		value := *configured.LowercaseRelayHeaders
		settings.LowercaseRelayHeaders = &value
	}
	return settings
}

// Defaults resolves environment overrides and safe provider defaults.
func Defaults() Settings {
	return Settings{
		Collect:               optionalBool(os.Getenv("MIRASIM_COLLECT")),
		Locale:                strings.TrimSpace(os.Getenv("MIRASIM_LOCALE")),
		RelayURL:              firstNonEmpty(cleanURL(os.Getenv("MIRASIM_RELAY_URL")), DefaultRelayURL),
		AdminURL:              firstNonEmpty(cleanURL(os.Getenv("MIRASIM_ADMIN_URL")), DefaultAdminURL),
		ClientVersion:         firstNonEmpty(strings.TrimSpace(os.Getenv("MIRASIM_CLIENT_VERSION")), DefaultClientVersion),
		OAuthPublicBaseURL:    cleanURL(os.Getenv("MIRASIM_OAUTH_PUBLIC_BASE_URL")),
		HTTP1Only:             boolOrDefault(os.Getenv("MIRASIM_HTTP1_ONLY"), true),
		LowercaseRelayHeaders: boolOrDefault(os.Getenv("MIRASIM_LOWERCASE_RELAY_HEADERS"), true),
	}
}

// boolOrDefault honours an environment override and otherwise falls back to the
// wire profile the official client was observed using.
func boolOrDefault(value string, fallback bool) *bool {
	if parsed := optionalBool(value); parsed != nil {
		return parsed
	}
	return &fallback
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
