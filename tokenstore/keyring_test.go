package tokenstore

import (
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestKeyringStore_SaveLoad(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	expires := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	ti := TokenInfo{RefreshToken: "rt", AccessToken: "at", ExpiresAt: expires, Scope: "* offline_access"}
	if err := store.Save("test-app", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load("test-app")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "rt" || loaded.AccessToken != "at" || !loaded.ExpiresAt.Equal(expires) || loaded.Scope != "* offline_access" || loaded.Legacy() {
		t.Fatalf("roundtrip failed: got %+v", loaded)
	}
}

func TestKeyringStore_LoadMissing(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestKeyringStore_Delete(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	ti := TokenInfo{RefreshToken: "rt", AccessToken: "at", Scope: "s1"}
	store.Save("del-test", ti)

	if err := store.Delete("del-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestKeyringStore_Available(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}
	if !store.Available() {
		t.Fatal("expected mock keyring to be available")
	}
}

func TestKeyringStore_SaveLoadOverwrite(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	ti := TokenInfo{RefreshToken: "r1", AccessToken: "a1", Scope: "s1"}
	store.Save("overwrite-test", ti)

	ti2 := TokenInfo{RefreshToken: "r2", AccessToken: "a2", Scope: "s2"}
	store.Save("overwrite-test", ti2)

	loaded, err := store.Load("overwrite-test")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RefreshToken != "r2" {
		t.Fatalf("expected overwritten value, got %+v", loaded)
	}
}
