package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DefaultRelayURL      = "https://mirasim-relay.mirofish.ai"
	DefaultAdminURL      = "https://admin.test.mirofish.ai"
	DefaultClientVersion = "0.0.146"
)

// Settings contains provider defaults. Concrete auth files may override these
// values so multiple Mirasim identities can coexist in one host.
type Settings struct {
	CredentialDir string `yaml:"credential-dir"`
	RelayURL      string `yaml:"relay-url"`
	AdminURL      string `yaml:"admin-url"`
	ClientVersion string `yaml:"client-version"`
}

type rootConfig struct {
	Plugins struct {
		Configs map[string]Settings `yaml:"configs"`
	} `yaml:"plugins"`
}

// Parse reads plugins.configs.mirasim without depending on CLIProxyAPI's
// internal configuration package.
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
		return settings
	}
	if value := strings.TrimSpace(configured.CredentialDir); value != "" {
		settings.CredentialDir = value
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
	return settings
}

// Defaults resolves environment overrides and safe provider defaults.
func Defaults() Settings {
	credentialDir := strings.TrimSpace(os.Getenv("MIRASIM_CREDENTIAL_DIR"))
	if credentialDir == "" {
		credentialDir = ".mirasim-credentials"
	}
	return Settings{
		CredentialDir: credentialDir,
		RelayURL:      firstNonEmpty(cleanURL(os.Getenv("MIRASIM_RELAY_URL")), DefaultRelayURL),
		AdminURL:      firstNonEmpty(cleanURL(os.Getenv("MIRASIM_ADMIN_URL")), DefaultAdminURL),
		ClientVersion: firstNonEmpty(strings.TrimSpace(os.Getenv("MIRASIM_CLIENT_VERSION")), DefaultClientVersion),
	}
}

// ResolveCredentialDir converts the configured path to a stable absolute path.
func ResolveCredentialDir(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = ".mirasim-credentials"
	}
	value = os.ExpandEnv(value)
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, errHome := os.UserHomeDir()
		if errHome != nil {
			return "", errHome
		}
		if value == "~" {
			value = home
		} else {
			remainder := strings.TrimLeft(strings.TrimPrefix(value, "~"), `/\`)
			remainder = strings.ReplaceAll(remainder, `\`, "/")
			value = filepath.Join(home, filepath.FromSlash(remainder))
		}
	}
	abs, errAbs := filepath.Abs(value)
	if errAbs != nil {
		return "", errAbs
	}
	return filepath.Clean(abs), nil
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
