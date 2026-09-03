package credentials

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// InstallOAuth builds the self-contained provider storage that CLIProxyAPI
// persists under auth-dir. It performs no filesystem writes.
func InstallOAuth(storage Storage, accessToken, refreshToken string) (Storage, error) {
	accessToken, errAccess := normalizeStoredSecret("access token", accessToken)
	if errAccess != nil {
		return Storage{}, errAccess
	}
	refreshToken, errRefresh := normalizeStoredSecret("refresh token", refreshToken)
	if errRefresh != nil {
		return Storage{}, errRefresh
	}
	keyPEM := strings.TrimSpace(storage.DevicePrivateKey)
	if keyPEM == "" || !validDeviceKey([]byte(keyPEM)) {
		generated, errKey := newDeviceKey()
		if errKey != nil {
			return Storage{}, errKey
		}
		keyPEM = strings.TrimSpace(string(generated))
	}
	storage.AccessToken = accessToken
	storage.RefreshToken = refreshToken
	storage.DevicePrivateKey = keyPEM
	storage.RecordTokenTiming(accessToken, 0, time.Now())
	storage.applyDefaults()
	if errValidate := storage.Validate(); errValidate != nil {
		return Storage{}, errValidate
	}
	return storage, nil
}

func normalizeStoredSecret(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("Mirasim %s is missing", label)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("Mirasim %s contains an invalid control character", label)
	}
	if len(value) > 64<<10 {
		return "", fmt.Errorf("Mirasim %s is unexpectedly large", label)
	}
	return value, nil
}

func newDeviceKey() ([]byte, error) {
	_, privateKey, errGenerate := ed25519.GenerateKey(rand.Reader)
	if errGenerate != nil {
		return nil, fmt.Errorf("generate Mirasim device private key: %w", errGenerate)
	}
	der, errMarshal := x509.MarshalPKCS8PrivateKey(privateKey)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal Mirasim device private key: %w", errMarshal)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func validDeviceKey(raw []byte) bool {
	block, rest := pem.Decode(raw)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return false
	}
	parsed, errParse := x509.ParsePKCS8PrivateKey(block.Bytes)
	if errParse != nil {
		return false
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	return ok && len(privateKey) == ed25519.PrivateKeySize
}
