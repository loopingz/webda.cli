package tokenstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMachineStore_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := NewMachineStore(dir)

	ti := TokenInfo{RefreshToken: "mrt", AccessToken: "mat", Sequence: "99"}
	if err := store.Save("machine-test", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Salt file should exist
	if _, err := os.Stat(filepath.Join(dir, "machine-test.tok.salt")); err != nil {
		t.Fatalf("expected salt file: %v", err)
	}

	// Token file should be encrypted (not plaintext JSON)
	data, _ := os.ReadFile(filepath.Join(dir, "machine-test.tok"))
	if string(data) == `{"refresh_token":"mrt","access_token":"mat","sequence":"99"}` {
		t.Fatal("file should be encrypted, not plaintext")
	}

	loaded, err := store.Load("machine-test")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "mrt" || loaded.AccessToken != "mat" || loaded.Sequence != "99" {
		t.Fatalf("roundtrip failed: got %+v", loaded)
	}
}

func TestMachineStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewMachineStore(dir)

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	store.Save("del-test", ti)

	if err := store.Delete("del-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
	// Salt file should also be removed
	if _, err := os.Stat(filepath.Join(dir, "del-test.tok.salt")); err == nil {
		t.Fatal("expected salt file to be removed")
	}
}

func TestMachineStore_SaltReuse(t *testing.T) {
	dir := t.TempDir()
	store := NewMachineStore(dir)

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	store.Save("reuse-test", ti)

	salt1, _ := os.ReadFile(filepath.Join(dir, "reuse-test.tok.salt"))

	// Save again — salt should be reused
	ti.AccessToken = "b"
	store.Save("reuse-test", ti)

	salt2, _ := os.ReadFile(filepath.Join(dir, "reuse-test.tok.salt"))
	if string(salt1) != string(salt2) {
		t.Fatal("salt should be reused across saves")
	}

	loaded, _ := store.Load("reuse-test")
	if loaded.AccessToken != "b" {
		t.Fatalf("expected updated token, got %+v", loaded)
	}
}
