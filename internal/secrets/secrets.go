// Package secrets protects secrets at rest (Cloudflare tokens, Controller signing key, JWT key).
//
// A 32-byte master key is resolved from, in order: the HOMECORE_MASTER_KEY environment variable
// (standard base64), a host-provided key (Android Keystore via the mobile bindings), or
// <data_dir>/master.key (created with mode 0600 on first run). Values are sealed with
// AES-256-GCM; the caller supplies associated data (e.g. "ddns_configs.api_token") so a sealed
// value cannot be moved to another column undetected.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// KeySize is the master key length in bytes.
const KeySize = 32

// EnvMasterKey is the environment variable holding a base64 master key.
const EnvMasterKey = "HOMECORE_MASTER_KEY"

// LoadOrCreateMasterKey resolves the master key as described in the package doc.
// hostKey may be nil.
func LoadOrCreateMasterKey(dataDir string, hostKey []byte) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(EnvMasterKey)); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != KeySize {
			return nil, fmt.Errorf("%s: must be base64 of %d bytes", EnvMasterKey, KeySize)
		}
		return k, nil
	}
	if hostKey != nil {
		if len(hostKey) != KeySize {
			return nil, fmt.Errorf("host master key: must be %d bytes", KeySize)
		}
		return hostKey, nil
	}

	path := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != KeySize {
			return nil, fmt.Errorf("%s: corrupt master key", path)
		}
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}

	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create master key: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(k) + "\n"); err != nil {
		return nil, err
	}
	return k, f.Sync()
}

// Sealer encrypts and decrypts small secrets with AES-256-GCM.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer creates a Sealer from a 32-byte master key.
func NewSealer(masterKey []byte) (*Sealer, error) {
	if len(masterKey) != KeySize {
		return nil, fmt.Errorf("master key must be %d bytes", KeySize)
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal returns nonce||ciphertext. aad binds the value to its purpose.
func (s *Sealer) Seal(plaintext []byte, aad string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, []byte(aad)), nil
}

// Open reverses Seal; it fails if the value was tampered with or aad differs.
func (s *Sealer) Open(sealed []byte, aad string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return nil, errors.New("sealed value too short")
	}
	pt, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
	if err != nil {
		return nil, errors.New("cannot open sealed value (wrong key or tampered)")
	}
	return pt, nil
}
