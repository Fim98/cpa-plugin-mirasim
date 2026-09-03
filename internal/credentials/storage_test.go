package credentials

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
)

func TestInstallOAuthReturnsSelfContainedAuthStorage(t *testing.T) {
	base := FromSettings(pluginconfig.Settings{
		RelayURL:      "https://relay.example",
		AdminURL:      "https://admin.example",
		ClientVersion: "1.2.3",
	})
	storage, errInstall := InstallOAuth(base, "oauth-access", "oauth-refresh")
	if errInstall != nil {
		t.Fatalf("InstallOAuth() error = %v", errInstall)
	}
	if storage.AccessToken != "oauth-access" || storage.RefreshToken != "oauth-refresh" || !validDeviceKey([]byte(storage.DevicePrivateKey)) {
		t.Fatal("InstallOAuth() did not return complete self-contained storage")
	}
	var payload map[string]any
	if errJSON := json.Unmarshal(storage.JSON(), &payload); errJSON != nil {
		t.Fatal(errJSON)
	}
	if payload["access_token"] != "oauth-access" || payload["refresh_token"] != "oauth-refresh" || payload["device_private_key"] == "" || payload["auth_kind"] != "oauth" {
		t.Fatal("auth JSON is missing self-contained credential fields")
	}
	if _, present := payload["credential_dir"]; present {
		t.Fatal("auth JSON retained an obsolete credential directory")
	}
}

func TestInstallOAuthPreservesValidEmbeddedDeviceKey(t *testing.T) {
	keyPEM := testDeviceKey(t)
	storage, errInstall := InstallOAuth(Storage{DevicePrivateKey: keyPEM}, "access", "refresh")
	if errInstall != nil {
		t.Fatal(errInstall)
	}
	if storage.DevicePrivateKey != keyPEM {
		t.Fatal("InstallOAuth() replaced a valid embedded device key")
	}
}

func TestInstallOAuthRejectsMissingOrMultilineTokens(t *testing.T) {
	for _, tc := range []struct {
		access  string
		refresh string
	}{
		{access: "", refresh: "refresh"},
		{access: "access", refresh: ""},
		{access: "access\nsecond", refresh: "refresh"},
	} {
		if _, errInstall := InstallOAuth(Storage{}, tc.access, tc.refresh); errInstall == nil {
			t.Fatal("InstallOAuth() accepted invalid token material")
		}
	}
}

func TestParseSelfContainedStoragePreservesHostFields(t *testing.T) {
	storage, errInstall := InstallOAuth(Storage{
		RelayURL:      "https://relay.example",
		AdminURL:      "https://admin.example",
		ClientVersion: "1.2.3",
		Raw:           map[string]any{"disabled": true, "proxy_url": "http://proxy.example", "credential_dir": "ignored"},
	}, "access", "refresh")
	if errInstall != nil {
		t.Fatal(errInstall)
	}
	parsed, errParse := Parse(storage.JSON(), pluginconfig.Defaults())
	if errParse != nil {
		t.Fatalf("Parse() error = %v", errParse)
	}
	if parsed == nil || parsed.AccessToken != "access" || parsed.RefreshToken != "refresh" {
		t.Fatal("Parse() did not restore self-contained credentials")
	}
	auth := parsed.AuthData("mirasim.json", "mirasim.json", time.Time{})
	if !auth.Disabled || auth.ProxyURL != "http://proxy.example" {
		t.Fatalf("host fields were not preserved: disabled=%t proxy=%q", auth.Disabled, auth.ProxyURL)
	}
	if auth.Metadata["access_token"] != "access" || auth.Metadata["refresh_token"] != "refresh" {
		t.Fatal("runtime metadata does not expose credentials to CPA's refresh coordinator")
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

func TestParseRejectsPathOnlyAuth(t *testing.T) {
	if _, errParse := Parse([]byte(`{"type":"mirasim","credential_dir":"ignored"}`), pluginconfig.Defaults()); errParse == nil {
		t.Fatal("Parse() accepted a path-only auth record")
	}
}

func TestValidateRequiresRefreshTokenAndValidPrivateKey(t *testing.T) {
	if errValidate := (Storage{}).Validate(); errValidate == nil {
		t.Fatal("Validate() accepted empty storage")
	}
	if errValidate := (Storage{AccessToken: "access", RefreshToken: "refresh", DevicePrivateKey: "not-a-key"}).Validate(); errValidate == nil {
		t.Fatal("Validate() accepted an invalid device key")
	}
	if errValidate := (Storage{AccessToken: "access", RefreshToken: "refresh", DevicePrivateKey: testDeviceKey(t)}).Validate(); errValidate != nil {
		t.Fatalf("Validate() error = %v", errValidate)
	}
}

func testDeviceKey(t *testing.T) string {
	t.Helper()
	_, privateKey, errGenerate := ed25519.GenerateKey(rand.Reader)
	if errGenerate != nil {
		t.Fatal(errGenerate)
	}
	der, errMarshal := x509.MarshalPKCS8PrivateKey(privateKey)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	return string(bytes.TrimSpace(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
}
