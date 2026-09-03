package credentials

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
)

const Provider = "mirasim"

// Storage is the provider-owned OAuth payload persisted by CLIProxyAPI in
// auth-dir.
type Storage struct {
	Type             string         `json:"type"`
	AccessToken      string         `json:"access_token,omitempty"`
	RefreshToken     string         `json:"refresh_token,omitempty"`
	DevicePrivateKey string         `json:"device_private_key,omitempty"`
	RelayURL         string         `json:"relay_url,omitempty"`
	AdminURL         string         `json:"admin_url,omitempty"`
	ClientVersion    string         `json:"client_version,omitempty"`
	Raw              map[string]any `json:"-"`
}

// FromSettings returns empty OAuth storage with public provider settings.
func FromSettings(settings pluginconfig.Settings) Storage {
	storage := Storage{
		Type:          Provider,
		RelayURL:      settings.RelayURL,
		AdminURL:      settings.AdminURL,
		ClientVersion: settings.ClientVersion,
	}
	storage.applyDefaults()
	return storage
}

// Parse recognizes and validates a self-contained Mirasim OAuth auth file.
func Parse(raw []byte, defaults pluginconfig.Settings) (*Storage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var probe map[string]any
	if errUnmarshal := json.Unmarshal(raw, &probe); errUnmarshal != nil {
		return nil, fmt.Errorf("decode Mirasim auth: %w", errUnmarshal)
	}
	if !strings.EqualFold(strings.TrimSpace(stringValue(probe["type"])), Provider) {
		return nil, nil
	}
	var storage Storage
	if errUnmarshal := json.Unmarshal(raw, &storage); errUnmarshal != nil {
		return nil, fmt.Errorf("decode Mirasim auth: %w", errUnmarshal)
	}
	storage.Raw = cloneMap(probe)
	if strings.TrimSpace(storage.RelayURL) == "" {
		storage.RelayURL = defaults.RelayURL
	}
	if strings.TrimSpace(storage.AdminURL) == "" {
		storage.AdminURL = defaults.AdminURL
	}
	if strings.TrimSpace(storage.ClientVersion) == "" {
		storage.ClientVersion = defaults.ClientVersion
	}
	storage.applyDefaults()

	if errValidate := storage.Validate(); errValidate != nil {
		return nil, errValidate
	}
	return &storage, nil
}

func (s *Storage) applyDefaults() {
	s.Type = Provider
	s.AccessToken = strings.TrimSpace(s.AccessToken)
	s.RefreshToken = strings.TrimSpace(s.RefreshToken)
	s.DevicePrivateKey = strings.TrimSpace(s.DevicePrivateKey)
	s.RelayURL = strings.TrimRight(strings.TrimSpace(s.RelayURL), "/")
	s.AdminURL = strings.TrimRight(strings.TrimSpace(s.AdminURL), "/")
	s.ClientVersion = strings.TrimSpace(s.ClientVersion)
	if s.RelayURL == "" {
		s.RelayURL = pluginconfig.DefaultRelayURL
	}
	if s.AdminURL == "" {
		s.AdminURL = pluginconfig.DefaultAdminURL
	}
	if s.ClientVersion == "" {
		s.ClientVersion = pluginconfig.DefaultClientVersion
	}
}

// Validate checks the complete in-memory credential payload without touching
// the filesystem.
func (s Storage) Validate() error {
	if _, errAccess := normalizeStoredSecret("access token", s.AccessToken); errAccess != nil {
		return errAccess
	}
	if _, errRefresh := normalizeStoredSecret("refresh token", s.RefreshToken); errRefresh != nil {
		return errRefresh
	}
	if strings.TrimSpace(s.DevicePrivateKey) == "" {
		return fmt.Errorf("Mirasim device private key is missing")
	}
	if !validDeviceKey([]byte(strings.TrimSpace(s.DevicePrivateKey))) {
		return fmt.Errorf("Mirasim device private key is not a valid Ed25519 PKCS#8 PEM")
	}
	return nil
}

func (s Storage) JSON() []byte {
	out := cloneMap(s.Raw)
	if out == nil {
		out = make(map[string]any)
	}
	delete(out, "credential_dir")
	out["type"] = Provider
	setOrDelete(out, "access_token", strings.TrimSpace(s.AccessToken))
	setOrDelete(out, "refresh_token", strings.TrimSpace(s.RefreshToken))
	setOrDelete(out, "device_private_key", strings.TrimSpace(s.DevicePrivateKey))
	setOrDelete(out, "relay_url", strings.TrimSpace(s.RelayURL))
	setOrDelete(out, "admin_url", strings.TrimSpace(s.AdminURL))
	setOrDelete(out, "client_version", strings.TrimSpace(s.ClientVersion))
	out["auth_kind"] = "oauth"
	raw, _ := json.Marshal(out)
	return raw
}

func (s Storage) Key() string {
	return strings.Join([]string{deviceFingerprint(s.DevicePrivateKey), s.RelayURL, s.AdminURL, s.ClientVersion}, "\x00")
}

func (s Storage) AuthData(id, fileName string, nextRefresh time.Time) pluginapi.AuthData {
	fileName = filepath.Base(strings.TrimSpace(fileName))
	if fileName == "" || fileName == "." {
		fileName = "mirasim.json"
	}
	if strings.TrimSpace(id) == "" {
		id = fileName
	}
	return pluginapi.AuthData{
		Provider:    Provider,
		ID:          id,
		FileName:    fileName,
		Label:       "Mirasim",
		Prefix:      strings.TrimSpace(stringValue(s.Raw["prefix"])),
		ProxyURL:    strings.TrimSpace(stringValue(s.Raw["proxy_url"])),
		Disabled:    boolValue(s.Raw["disabled"]),
		StorageJSON: s.JSON(),
		Metadata: map[string]any{
			"type":          Provider,
			"auth_kind":     "oauth",
			"access_token":  strings.TrimSpace(s.AccessToken),
			"refresh_token": strings.TrimSpace(s.RefreshToken),
		},
		Attributes: map[string]string{
			"auth_kind": "oauth",
		},
		NextRefreshAfter: nextRefresh,
	}
}

func deviceFingerprint(keyPEM string) string {
	block, _ := pem.Decode([]byte(strings.TrimSpace(keyPEM)))
	if block != nil {
		if parsed, errParse := x509.ParsePKCS8PrivateKey(block.Bytes); errParse == nil {
			if privateKey, ok := parsed.(ed25519.PrivateKey); ok {
				if publicDER, errPublic := x509.MarshalPKIXPublicKey(privateKey.Public()); errPublic == nil {
					digest := sha256.Sum256(publicDER)
					return base64.RawURLEncoding.EncodeToString(digest[:12])
				}
			}
		}
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(keyPEM)))
	return base64.RawURLEncoding.EncodeToString(digest[:12])
}

func setOrDelete(values map[string]any, key, value string) {
	if value == "" {
		delete(values, key)
		return
	}
	values[key] = value
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}
