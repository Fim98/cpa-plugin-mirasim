package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	pluginconfig "github.com/router-for-me/CLIProxyAPIPlugins/mirasim/internal/config"
)

const Provider = "mirasim"

// Storage intentionally stores only paths and public endpoint configuration.
// Tokens and the device private key remain in the configured plaintext
// credential directory and never enter CLIProxyAPI's auth JSON.
type Storage struct {
	Type          string `json:"type"`
	CredentialDir string `json:"credential_dir"`
	RelayURL      string `json:"relay_url,omitempty"`
	AdminURL      string `json:"admin_url,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
}

func FromSettings(settings pluginconfig.Settings) (Storage, error) {
	dir, errDir := pluginconfig.ResolveCredentialDir(settings.CredentialDir)
	if errDir != nil {
		return Storage{}, fmt.Errorf("resolve Mirasim credential directory: %w", errDir)
	}
	storage := Storage{
		Type:          Provider,
		CredentialDir: dir,
		RelayURL:      strings.TrimRight(strings.TrimSpace(settings.RelayURL), "/"),
		AdminURL:      strings.TrimRight(strings.TrimSpace(settings.AdminURL), "/"),
		ClientVersion: strings.TrimSpace(settings.ClientVersion),
	}
	storage.applyDefaults()
	return storage, nil
}

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
	if strings.TrimSpace(storage.CredentialDir) == "" {
		storage.CredentialDir = defaults.CredentialDir
	}
	resolved, errResolve := pluginconfig.ResolveCredentialDir(storage.CredentialDir)
	if errResolve != nil {
		return nil, fmt.Errorf("resolve Mirasim credential directory: %w", errResolve)
	}
	storage.CredentialDir = resolved
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
	return &storage, nil
}

func (s *Storage) applyDefaults() {
	s.Type = Provider
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

func (s Storage) ValidateFiles() error {
	for _, name := range []string{"refresh-token.txt", "device-private-key.pem"} {
		path := filepath.Join(s.CredentialDir, name)
		info, errStat := os.Stat(path)
		if errStat != nil {
			return fmt.Errorf("required Mirasim credential %s: %w", path, errStat)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("required Mirasim credential is empty or not a regular file: %s", path)
		}
	}
	return nil
}

func (s Storage) JSON() []byte {
	raw, _ := json.Marshal(s)
	return raw
}

func (s Storage) Key() string {
	return strings.Join([]string{s.CredentialDir, s.RelayURL, s.AdminURL, s.ClientVersion}, "\x00")
}

func (s Storage) AuthData(id, fileName string, nextRefresh time.Time) pluginapi.AuthData {
	fileName = filepath.Base(strings.TrimSpace(fileName))
	if fileName == "" || fileName == "." {
		fileName = "mirasim.json"
	}
	if strings.TrimSpace(id) == "" {
		id = fileName
	}
	label := "Mirasim (" + filepath.Base(s.CredentialDir) + ")"
	return pluginapi.AuthData{
		Provider:    Provider,
		ID:          id,
		FileName:    fileName,
		Label:       label,
		StorageJSON: s.JSON(),
		Metadata: map[string]any{
			"type":            Provider,
			"credential_mode": "project-plaintext",
			"credential_dir":  s.CredentialDir,
		},
		Attributes: map[string]string{
			"credential_dir": s.CredentialDir,
		},
		NextRefreshAfter: nextRefresh,
	}
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
