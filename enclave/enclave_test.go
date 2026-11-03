package enclave

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

// soft is a software double of the Secure Enclave: its "wrapped key" is the private key itself.
type soft struct {
	deny   bool
	agreed int
}

func (*soft) available() bool { return true }
func (*soft) create() ([]byte, []byte, error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return k.Bytes(), k.PublicKey().Bytes(), nil
}
func (s *soft) agree(blob, peer []byte, _ string) ([]byte, error) {
	s.agreed++
	if s.deny {
		return nil, ErrDenied
	}
	k, err := ecdh.P256().NewPrivateKey(blob)
	if err != nil {
		return nil, err
	}
	p, err := ecdh.P256().NewPublicKey(peer)
	if err != nil {
		return nil, err
	}
	return k.ECDH(p)
}

func withSoft(t *testing.T) *soft {
	t.Helper()
	old := se
	t.Cleanup(func() { se = old })
	s := &soft{}
	se = s
	return s
}

func TestSealOpen(t *testing.T) {
	s := withSoft(t)
	token, err := Seal([]byte("session.key"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, "session") || s.agreed != 0 {
		t.Fatal("sealing must neither reveal the secret nor prompt")
	}
	plain, err := Open(token, "why")
	if err != nil || string(plain) != "session.key" || s.agreed != 1 {
		t.Fatalf("%q %v", plain, err)
	}
	s.deny = true
	if _, err := Open(token, "why"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}

func TestOpenRefusesTamperedTokens(t *testing.T) {
	withSoft(t)
	token, _ := Seal([]byte("x"))
	other, _ := Seal([]byte("y"))
	for _, bad := range []string{"", "!!", token[:len(token)-4] + "AAAA", "e30"} {
		if _, err := Open(bad, "why"); err == nil {
			t.Errorf("%q opened", bad)
		}
	}
	if token == other {
		t.Fatal("each seal must use its own key")
	}
}

func TestUnavailableByDefaultWithoutTheBridge(t *testing.T) {
	if _, ok := se.(unavailable); ok {
		if Available() {
			t.Fatal("available without bridge")
		}
		if _, err := Seal([]byte("x")); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
}

type failing struct{ noKey bool }

func (failing) available() bool { return true }
func (f failing) create() ([]byte, []byte, error) {
	if f.noKey {
		return nil, nil, errors.New("no key")
	}
	return []byte("blob"), []byte("not a point"), nil
}
func (failing) agree([]byte, []byte, string) ([]byte, error) { return []byte("short"), nil }

func TestSealAndOpenErrors(t *testing.T) {
	old := se
	t.Cleanup(func() { se = old })
	se = failing{noKey: true}
	if _, err := Seal([]byte("x")); err == nil {
		t.Fatal("key creation failure must fail")
	}
	se = failing{}
	if _, err := Seal([]byte("x")); err == nil {
		t.Fatal("an invalid public key must fail")
	}
	s := withSoft(t)
	token, _ := Seal([]byte("x"))
	se = failing{}
	if _, err := Open(token, "why"); err == nil {
		t.Fatal("a bad shared secret must not open")
	}
	_ = s
	if _, err := (unavailable{}).agree(nil, nil, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
