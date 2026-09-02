package credentials

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	accessTokenFile  = "access-token.txt"
	refreshTokenFile = "refresh-token.txt"
	deviceKeyFile    = "device-private-key.pem"
)

// InstallOAuth writes OAuth material to the configured credential directory.
// Storage JSON continues to contain only the directory path and public
// endpoint settings; bearer tokens and the device private key stay here.
func InstallOAuth(storage Storage, accessToken, refreshToken string) error {
	accessToken, errAccess := normalizeSecret("access token", accessToken)
	if errAccess != nil {
		return errAccess
	}
	refreshToken, errRefresh := normalizeSecret("refresh token", refreshToken)
	if errRefresh != nil {
		return errRefresh
	}
	if errMkdir := os.MkdirAll(storage.CredentialDir, 0o700); errMkdir != nil {
		return fmt.Errorf("create Mirasim credential directory: %w", errMkdir)
	}
	if errChmod := os.Chmod(storage.CredentialDir, 0o700); errChmod != nil && os.PathSeparator != '\\' {
		return fmt.Errorf("set Mirasim credential directory permissions: %w", errChmod)
	}

	keyPEM, errKey := existingOrNewDeviceKey(storage.CredentialDir)
	if errKey != nil {
		return errKey
	}
	for _, secret := range []struct {
		name  string
		value []byte
	}{
		{name: accessTokenFile, value: []byte(accessToken + "\n")},
		{name: refreshTokenFile, value: []byte(refreshToken + "\n")},
		{name: deviceKeyFile, value: keyPEM},
	} {
		if errWrite := writeSecretAtomically(storage.CredentialDir, secret.name, secret.value); errWrite != nil {
			return errWrite
		}
	}
	return nil
}

func normalizeSecret(label, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("Mirasim OAuth callback is missing %s", label)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("Mirasim OAuth %s contains an invalid control character", label)
	}
	if len(value) > 64<<10 {
		return "", fmt.Errorf("Mirasim OAuth %s is unexpectedly large", label)
	}
	return value, nil
}

func existingOrNewDeviceKey(dir string) ([]byte, error) {
	path := filepath.Join(dir, deviceKeyFile)
	if info, errStat := os.Lstat(path); errStat == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Mirasim credential target is not a regular file: %s", path)
		}
		raw, errRead := os.ReadFile(path)
		if errRead != nil {
			return nil, fmt.Errorf("read Mirasim device private key: %w", errRead)
		}
		if validDeviceKey(raw) {
			return append([]byte(nil), raw...), nil
		}
	} else if !os.IsNotExist(errStat) {
		return nil, fmt.Errorf("inspect Mirasim device private key: %w", errStat)
	}

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

func writeSecretAtomically(dir, name string, value []byte) (err error) {
	path := filepath.Join(dir, name)
	if info, errStat := os.Lstat(path); errStat == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("Mirasim credential target is not a regular file: %s", path)
		}
	} else if !os.IsNotExist(errStat) {
		return fmt.Errorf("inspect Mirasim credential target %s: %w", path, errStat)
	}

	tmp, errCreate := os.CreateTemp(dir, "."+name+"-*")
	if errCreate != nil {
		return fmt.Errorf("create temporary Mirasim credential %s: %w", path, errCreate)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if errChmod := tmp.Chmod(0o600); errChmod != nil && os.PathSeparator != '\\' {
		return fmt.Errorf("set temporary Mirasim credential permissions: %w", errChmod)
	}
	if _, errWrite := tmp.Write(value); errWrite != nil {
		return fmt.Errorf("write temporary Mirasim credential %s: %w", path, errWrite)
	}
	if errSync := tmp.Sync(); errSync != nil {
		return fmt.Errorf("sync temporary Mirasim credential %s: %w", path, errSync)
	}
	if errClose := tmp.Close(); errClose != nil {
		return fmt.Errorf("close temporary Mirasim credential %s: %w", path, errClose)
	}
	if errRename := os.Rename(tmpPath, path); errRename != nil {
		return fmt.Errorf("install Mirasim credential %s: %w", path, errRename)
	}
	if errChmod := os.Chmod(path, 0o600); errChmod != nil && os.PathSeparator != '\\' {
		return fmt.Errorf("set Mirasim credential permissions %s: %w", path, errChmod)
	}
	return nil
}
