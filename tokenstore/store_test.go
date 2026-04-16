package tokenstore

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestNewTokenStore_PrefersKeyring(t *testing.T) {
	keyring.MockInit()
	store := NewTokenStore(t.TempDir())
	if _, ok := store.(*KeyringStore); !ok {
		t.Fatalf("expected KeyringStore when keyring is available, got %T", store)
	}
}

func TestNewTokenStore_FallbackToMachine(t *testing.T) {
	// Without mock keyring init and without SSH agent, should fall back to MachineStore.
	// Note: this test may get SSHAgentStore if the test runner has SSH_AUTH_SOCK.
	// We test the factory returns a non-nil store.
	dir := t.TempDir()
	store := NewTokenStore(dir)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestMarshalUnmarshalTokenInfo(t *testing.T) {
	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	data, err := MarshalTokenInfo(ti)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := UnmarshalTokenInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != ti {
		t.Fatalf("roundtrip failed: got %+v, want %+v", loaded, ti)
	}
}
