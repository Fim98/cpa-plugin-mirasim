package credentials

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
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

func TestInstallOAuthWritesSecretsOutsideAuthJSONAndPreservesValidDeviceKey(t *testing.T) {
	dir := t.TempDir()
	storage, errStorage := FromSettings(pluginconfig.Settings{CredentialDir: dir})
	if errStorage != nil {
		t.Fatal(errStorage)
	}
	_, privateKey, errGenerate := ed25519.GenerateKey(rand.Reader)
	if errGenerate != nil {
		t.Fatal(errGenerate)
	}
	der, errMarshal := x509.MarshalPKCS8PrivateKey(privateKey)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	originalKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if errWrite := os.WriteFile(filepath.Join(dir, "device-private-key.pem"), originalKey, 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}

	if errInstall := InstallOAuth(storage, "oauth-access", "oauth-refresh"); errInstall != nil {
		t.Fatalf("InstallOAuth() error = %v", errInstall)
	}
	assertSecretFile(t, dir, "access-token.txt", "oauth-access")
	assertSecretFile(t, dir, "refresh-token.txt", "oauth-refresh")
	keyAfter, errRead := os.ReadFile(filepath.Join(dir, "device-private-key.pem"))
	if errRead != nil {
		t.Fatal(errRead)
	}
	if !bytes.Equal(keyAfter, originalKey) {
		t.Fatal("InstallOAuth replaced a valid device key")
	}
	for _, forbidden := range []string{"oauth-access", "oauth-refresh"} {
		if bytes.Contains(storage.JSON(), []byte(forbidden)) {
			t.Fatalf("auth JSON contains %q", forbidden)
		}
	}
}

func TestInstallOAuthGeneratesEd25519DeviceKey(t *testing.T) {
	storage, errStorage := FromSettings(pluginconfig.Settings{CredentialDir: t.TempDir()})
	if errStorage != nil {
		t.Fatal(errStorage)
	}
	if errInstall := InstallOAuth(storage, "access", "refresh"); errInstall != nil {
		t.Fatalf("InstallOAuth() error = %v", errInstall)
	}
	raw, errRead := os.ReadFile(filepath.Join(storage.CredentialDir, "device-private-key.pem"))
	if errRead != nil {
		t.Fatal(errRead)
	}
	if !validDeviceKey(raw) {
		t.Fatal("generated device key is not an Ed25519 PKCS#8 PEM")
	}
}

func TestInstallOAuthRejectsMissingOrMultilineTokens(t *testing.T) {
	storage := Storage{CredentialDir: t.TempDir()}
	for _, tc := range []struct {
		access  string
		refresh string
	}{
		{access: "", refresh: "refresh"},
		{access: "access", refresh: ""},
		{access: "access\nsecond", refresh: "refresh"},
	} {
		if errInstall := InstallOAuth(storage, tc.access, tc.refresh); errInstall == nil {
			t.Fatalf("InstallOAuth(%q, %q) returned nil", tc.access, tc.refresh)
		}
	}
}

func assertSecretFile(t *testing.T, dir, name, want string) {
	t.Helper()
	raw, errRead := os.ReadFile(filepath.Join(dir, name))
	if errRead != nil {
		t.Fatal(errRead)
	}
	if strings.TrimSpace(string(raw)) != want {
		t.Fatalf("%s = %q, want %q", name, raw, want)
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
