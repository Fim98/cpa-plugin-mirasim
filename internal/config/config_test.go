package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMirasimConfig(t *testing.T) {
	t.Setenv("MIRASIM_CREDENTIAL_DIR", "env-credentials")
	t.Setenv("MIRASIM_RELAY_URL", "https://env-relay.example/")
	t.Setenv("MIRASIM_ADMIN_URL", "https://env-admin.example/")
	t.Setenv("MIRASIM_CLIENT_VERSION", "env-version")

	settings := Parse([]byte(`
plugins:
  configs:
    mirasim:
      credential-dir: ./project-credentials
      relay-url: https://relay.example/
      admin-url: https://admin.example/
      client-version: 1.2.3
`))
	if settings.CredentialDir != "./project-credentials" {
		t.Fatalf("CredentialDir = %q", settings.CredentialDir)
	}
	if settings.RelayURL != "https://relay.example" {
		t.Fatalf("RelayURL = %q", settings.RelayURL)
	}
	if settings.AdminURL != "https://admin.example" {
		t.Fatalf("AdminURL = %q", settings.AdminURL)
	}
	if settings.ClientVersion != "1.2.3" {
		t.Fatalf("ClientVersion = %q", settings.ClientVersion)
	}
}

func TestResolveCredentialDirAcceptsBothTildeSeparators(t *testing.T) {
	home, errHome := os.UserHomeDir()
	if errHome != nil {
		t.Fatal(errHome)
	}
	for _, input := range []string{"~/mirasim-creds", `~\mirasim-creds`} {
		resolved, errResolve := ResolveCredentialDir(input)
		if errResolve != nil {
			t.Fatalf("ResolveCredentialDir(%q) error = %v", input, errResolve)
		}
		if resolved != filepath.Join(home, "mirasim-creds") {
			t.Fatalf("ResolveCredentialDir(%q) = %q", input, resolved)
		}
	}
}

func TestParseInvalidConfigFallsBackToDefaults(t *testing.T) {
	t.Setenv("MIRASIM_CREDENTIAL_DIR", "env-credentials")
	t.Setenv("MIRASIM_RELAY_URL", "https://env-relay.example/")
	t.Setenv("MIRASIM_ADMIN_URL", "https://env-admin.example/")
	t.Setenv("MIRASIM_CLIENT_VERSION", "env-version")

	settings := Parse([]byte("plugins: ["))
	if settings.CredentialDir != "env-credentials" || settings.RelayURL != "https://env-relay.example" {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestDefaultsUseMirasimProtocolVersion260(t *testing.T) {
	t.Setenv("MIRASIM_CLIENT_VERSION", "")
	if got := Defaults().ClientVersion; got != "0.0.260" {
		t.Fatalf("ClientVersion = %q, want 0.0.260", got)
	}
}

func TestResolveCredentialDirReturnsAbsoluteCleanPath(t *testing.T) {
	resolved, errResolve := ResolveCredentialDir(filepath.Join(t.TempDir(), "nested", "..", "credentials"))
	if errResolve != nil {
		t.Fatalf("ResolveCredentialDir() error = %v", errResolve)
	}
	if !filepath.IsAbs(resolved) || filepath.Base(resolved) != "credentials" {
		t.Fatalf("resolved path = %q", resolved)
	}
}
