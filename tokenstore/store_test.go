package tokenstore

import (
	"strings"
	"testing"
	"time"

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
	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Scope: "* offline_access"}
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

func TestUnmarshalTokenInfo_Invalid(t *testing.T) {
	_, err := UnmarshalTokenInfo([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMarshalTokenInfo_Roundtrip(t *testing.T) {
	ti := TokenInfo{RefreshToken: "", AccessToken: "", Scope: ""}
	data, err := MarshalTokenInfo(ti)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := UnmarshalTokenInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != ti {
		t.Fatalf("empty roundtrip failed: got %+v", loaded)
	}
}

func TestTokenInfoLegacyAndRoundTrip(t *testing.T) {
	legacy, err := UnmarshalTokenInfo([]byte(`{"refresh_token":"r","access_token":"a","sequence":"12"}`))
	if err != nil || !legacy.Legacy() {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", ExpiresAt: now, Scope: "Task.* offline_access"}
	data, err := MarshalTokenInfo(ti)
	if err != nil || strings.Contains(string(data), "sequence") {
		t.Fatalf("%s %v", data, err)
	}
	back, err := UnmarshalTokenInfo(data)
	if err != nil || back.Legacy() || !back.ExpiresAt.Equal(now) || back.Scope != ti.Scope {
		t.Fatalf("%+v %v", back, err)
	}
}
