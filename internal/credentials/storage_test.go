package credentials

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
)

func TestStorageJSONContainsOnlyPathAndPublicConfiguration(t *testing.T) {
	dir := t.TempDir()
	storage, errStorage := FromSettings(pluginconfig.Settings{
		CredentialDir: dir,
		RelayURL:      "https://relay.example",
		AdminURL:      "https://admin.example",
		ClientVersion: "1.2.3",
	})
	if errStorage != nil {
		t.Fatalf("FromSettings() error = %v", errStorage)
	}
	raw := storage.JSON()
	for _, forbidden := range [][]byte{[]byte("access_token"), []byte("refresh_token"), []byte("private_key")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("storage JSON contains secret field %q: %s", forbidden, raw)
		}
	}
	parsed, errParse := Parse(raw, pluginconfig.Defaults())
	if errParse != nil {
		t.Fatalf("Parse() error = %v", errParse)
	}
	if parsed == nil || parsed.CredentialDir != storage.CredentialDir {
		t.Fatalf("parsed storage = %#v", parsed)
	}
}

func TestParseIgnoresOtherProviders(t *testing.T) {
	parsed, errParse := Parse([]byte(`{"type":"other"}`), pluginconfig.Defaults())
	if errParse != nil {
		t.Fatalf("Parse() error = %v", errParse)
	}
	if parsed != nil {
		t.Fatalf("Parse() = %#v, want nil", parsed)
	}
}

func TestValidateFilesRequiresRefreshTokenAndPrivateKey(t *testing.T) {
	dir := t.TempDir()
	storage := Storage{CredentialDir: dir}
	if errValidate := storage.ValidateFiles(); errValidate == nil {
		t.Fatal("ValidateFiles() returned nil for an empty directory")
	}
	if errWrite := os.WriteFile(filepath.Join(dir, "refresh-token.txt"), []byte("refresh\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errWrite := os.WriteFile(filepath.Join(dir, "device-private-key.pem"), []byte("key\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	if errValidate := storage.ValidateFiles(); errValidate != nil {
		t.Fatalf("ValidateFiles() error = %v", errValidate)
	}
}
