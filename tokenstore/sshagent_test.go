package tokenstore

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestSSHAgentStore_DeriveKeyAndRoundtrip(t *testing.T) {
	dir := t.TempDir()
	fakeSignature := []byte("fake-ssh-agent-signature-for-test-purposes-1234")
	key := sha256.Sum256(fakeSignature)

	store := &SSHAgentStore{
		configDir:  dir,
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
