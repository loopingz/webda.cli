package tokenstore

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeyringStore_SaveLoad(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	ti := TokenInfo{RefreshToken: "rt", AccessToken: "at", Sequence: "42"}
	if err := store.Save("test-app", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load("test-app")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "rt" || loaded.AccessToken != "at" || loaded.Sequence != "42" {
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

	ti := TokenInfo{RefreshToken: "rt", AccessToken: "at", Sequence: "1"}
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
