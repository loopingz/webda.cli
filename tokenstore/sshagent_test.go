package tokenstore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSSHAgentStore_DeriveKeyAndRoundtrip(t *testing.T) {
	dir := t.TempDir()
	fakeSignature := []byte("fake-ssh-agent-signature-for-test-purposes-1234")
	key := sha256.Sum256(fakeSignature)

	store := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}

	ti := TokenInfo{RefreshToken: "ssh-rt", AccessToken: "ssh-at", Sequence: "7"}
	if err := store.Save("ssh-test", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists and is not plaintext
	data, _ := os.ReadFile(filepath.Join(dir, "ssh-test.tok"))
	if len(data) == 0 {
		t.Fatal("expected encrypted file to exist")
	}
	if string(data) == `{"refresh_token":"ssh-rt","access_token":"ssh-at","sequence":"7"}` {
		t.Fatal("file should be encrypted, not plaintext")
	}

	loaded, err := store.Load("ssh-test")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "ssh-rt" || loaded.AccessToken != "ssh-at" || loaded.Sequence != "7" {
		t.Fatalf("roundtrip failed: got %+v", loaded)
	}
}

func TestSSHAgentStore_Delete(t *testing.T) {
	dir := t.TempDir()
	key := sha256.Sum256([]byte("test-key"))
	store := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	store.Save("del-test", ti)

	if err := store.Delete("del-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSSHAgentStore_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	key := sha256.Sum256([]byte("test"))
	store := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}
	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error loading nonexistent")
	}
}

func TestSSHAgentStore_LoadDecryptFailure(t *testing.T) {
	dir := t.TempDir()
	// Write garbage to the token file
	os.WriteFile(filepath.Join(dir, "bad.tok"), []byte("not-encrypted"), 0o600)

	key := sha256.Sum256([]byte("test"))
	store := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}
	_, err := store.Load("bad")
	if err == nil {
		t.Fatal("expected decrypt error for garbage data")
	}
}

func TestSSHAgentStore_DeriveKeyError(t *testing.T) {
	dir := t.TempDir()
	store := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return [32]byte{}, fmt.Errorf("no agent") },
	}

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	err := store.Save("fail-test", ti)
	if err == nil {
		t.Fatal("expected error when deriveKey fails")
	}

	// Also test Load with deriveKey error (need a file to exist first)
	// Save with working key, then try to load with broken key
	key := sha256.Sum256([]byte("test"))
	goodStore := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}
	goodStore.Save("key-err-test", ti)

	store2 := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return [32]byte{}, fmt.Errorf("no agent") },
	}
	_, err = store2.Load("key-err-test")
	if err == nil {
		t.Fatal("expected error when deriveKey fails on Load")
	}
}
