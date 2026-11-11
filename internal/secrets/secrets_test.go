package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMasterKeyPersistsWithStrictPermissions(t *testing.T) {
	t.Setenv(EnvMasterKey, "")
	dir := t.TempDir()
	k1, err := LoadOrCreateMasterKey(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadOrCreateMasterKey(dir, nil)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("key must be stable across loads: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, "master.key"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("master.key perm = %v, err = %v", st.Mode().Perm(), err)
	}
}

func TestSealOpenBindsAssociatedData(t *testing.T) {
	s, err := NewSealer(bytes.Repeat([]byte{7}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal([]byte("cf-token"), "ddns_configs.api_token")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := s.Open(sealed, "ddns_configs.api_token")
	if err != nil || string(pt) != "cf-token" {
		t.Fatalf("round trip failed: %q %v", pt, err)
	}
	if _, err := s.Open(sealed, "controller_keys.private_key"); err == nil {
		t.Fatal("open with different aad must fail")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.Open(sealed, "ddns_configs.api_token"); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}
