package config

import (
	"testing"
)

func TestExplicitCollectionOverridesEnvironment(t *testing.T) {
	t.Setenv("MIRASIM_COLLECT", "true")
	t.Setenv("MIRASIM_LOCALE", "en-US")
	cfg := Parse([]byte("collect: false\nlocale: zh-CN\n"))
	if cfg.Collect == nil || *cfg.Collect || cfg.Locale != "zh-CN" {
		t.Fatalf("config=%+v", cfg)
	}
	t.Setenv("MIRASIM_COLLECT", "")
	if Defaults().Collect != nil {
		t.Fatal("omitted collection should follow relay default")
	}
}

func TestParseMirasimConfig(t *testing.T) {
	t.Setenv("MIRASIM_RELAY_URL", "https://env-relay.example/")
	t.Setenv("MIRASIM_ADMIN_URL", "https://env-admin.example/")
	t.Setenv("MIRASIM_CLIENT_VERSION", "env-version")
	t.Setenv("MIRASIM_OAUTH_PUBLIC_BASE_URL", "https://env-cpa.example/")

	settings := Parse([]byte(`
plugins:
  configs:
    mirasim:
      relay-url: https://relay.example/
      admin-url: https://admin.example/
      client-version: 1.2.3
      oauth-public-base-url: https://cpa.example/
`))
	if settings.RelayURL != "https://relay.example" {
		t.Fatalf("RelayURL = %q", settings.RelayURL)
	}
	if settings.AdminURL != "https://admin.example" {
		t.Fatalf("AdminURL = %q", settings.AdminURL)
	}
	if settings.ClientVersion != "1.2.3" {
		t.Fatalf("ClientVersion = %q", settings.ClientVersion)
	}
	if settings.OAuthPublicBaseURL != "https://cpa.example" {
		t.Fatalf("OAuthPublicBaseURL = %q", settings.OAuthPublicBaseURL)
	}
}

func TestParseRuntimePluginSubconfiguration(t *testing.T) {
	settings := Parse([]byte(`
enabled: true
priority: 2
relay-url: https://relay.runtime.example/
admin-url: https://auth.runtime.example/
client-version: 9.8.7
oauth-public-base-url: https://cpa.runtime.example/gateway/
`))
	if settings.RelayURL != "https://relay.runtime.example" || settings.AdminURL != "https://auth.runtime.example" || settings.ClientVersion != "9.8.7" || settings.OAuthPublicBaseURL != "https://cpa.runtime.example/gateway" {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestParseInvalidConfigFallsBackToDefaults(t *testing.T) {
	t.Setenv("MIRASIM_RELAY_URL", "https://env-relay.example/")
	t.Setenv("MIRASIM_ADMIN_URL", "https://env-admin.example/")
	t.Setenv("MIRASIM_CLIENT_VERSION", "env-version")

	settings := Parse([]byte("plugins: ["))
	if settings.RelayURL != "https://env-relay.example" {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestDefaultsUseCurrentMirasimEndpointsAndProtocolVersion(t *testing.T) {
	t.Setenv("MIRASIM_RELAY_URL", "")
	t.Setenv("MIRASIM_ADMIN_URL", "")
	t.Setenv("MIRASIM_CLIENT_VERSION", "")
	settings := Defaults()
	if settings.RelayURL != "https://relay.mirasim.ai" || settings.AdminURL != "https://auth.mirasim.ai" || settings.ClientVersion != "0.0.336" {
		t.Fatalf("defaults = %#v", settings)
	}
}

// The official client offers only http/1.1 and sends lower-case header names,
// so matching it is the default rather than something an operator opts into.
func TestWireProfileSettingsDefaultOn(t *testing.T) {
	t.Setenv("MIRASIM_HTTP1_ONLY", "")
	t.Setenv("MIRASIM_LOWERCASE_RELAY_HEADERS", "")
	settings := Parse(nil)
	if settings.HTTP1Only == nil || !*settings.HTTP1Only {
		t.Fatalf("HTTP1Only = %v", settings.HTTP1Only)
	}
	if settings.LowercaseRelayHeaders == nil || !*settings.LowercaseRelayHeaders {
		t.Fatalf("LowercaseRelayHeaders = %v", settings.LowercaseRelayHeaders)
	}
}

func TestWireProfileSettingsCanBeTurnedOff(t *testing.T) {
	t.Setenv("MIRASIM_HTTP1_ONLY", "")
	t.Setenv("MIRASIM_LOWERCASE_RELAY_HEADERS", "")
	settings := Parse([]byte("http1-only: false\nlowercase-relay-headers: false\n"))
	if settings.HTTP1Only == nil || *settings.HTTP1Only {
		t.Fatalf("HTTP1Only = %v", settings.HTTP1Only)
	}
	if settings.LowercaseRelayHeaders == nil || *settings.LowercaseRelayHeaders {
		t.Fatalf("LowercaseRelayHeaders = %v", settings.LowercaseRelayHeaders)
	}
}

func TestExplicitWireProfileOverridesEnvironment(t *testing.T) {
	t.Setenv("MIRASIM_HTTP1_ONLY", "true")
	settings := Parse([]byte("http1-only: false\n"))
	if settings.HTTP1Only == nil || *settings.HTTP1Only {
		t.Fatalf("HTTP1Only = %v", settings.HTTP1Only)
	}
}
