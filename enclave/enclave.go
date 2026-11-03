// Package enclave seals a secret to a Secure Enclave key that only works after the person confirms their presence
// (Touch ID or the account password): the sealed form is useless without this Mac's Secure Enclave and that person.
//
// The key is a P-256 key agreement key created in the Secure Enclave (CryptoKit, no entitlement needed). Sealing
// needs only its public key: an ephemeral P-256 key agrees with it, HKDF-SHA-256 derives an AES-256-GCM key.
// Opening runs the agreement in the Secure Enclave, which prompts.
package enclave

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ErrUnavailable is returned without a Secure Enclave bridge (non-macOS, a build without the sebridge tag, or a Mac
// without Secure Enclave).
var ErrUnavailable = errors.New("no Secure Enclave available")

// ErrDenied is returned when the person cancels or fails the presence check.
var ErrDenied = errors.New("user presence was not confirmed")

const version = 1

var info = []byte("webda-cli enclave v1")

// Sealed is a secret sealed to a Secure Enclave key.
type Sealed struct {
	V int `json:"v"`
	// Key is the wrapped Secure Enclave key: only that Secure Enclave can use it.
	Key []byte `json:"key"`
	// Ephemeral is the X9.63 public key of the sealing side.
	Ephemeral []byte `json:"epk"`
	// Box is the AES-256-GCM nonce followed by the ciphertext.
	Box []byte `json:"box"`
}

// backend is the Secure Enclave (or a software double in tests).
type backend interface {
	available() bool
	// create returns a wrapped key and its X9.63 public key
	create() (blob, pub []byte, err error)
	// agree returns the 32-byte P-256 shared secret of the wrapped key with peer, after the presence check
	agree(blob, peer []byte, reason string) ([]byte, error)
}

// se is the platform backend (set by the sebridge build).
var se backend = unavailable{}

type unavailable struct{}

func (unavailable) available() bool { return false }
func (unavailable) create() ([]byte, []byte, error) {
	return nil, nil, ErrUnavailable
}
func (unavailable) agree([]byte, []byte, string) ([]byte, error) { return nil, ErrUnavailable }

// Available reports whether secrets can be sealed to the Secure Enclave here.
func Available() bool { return se.available() }

func aead(shared []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, shared, nil, string(info), 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal creates a Secure Enclave key and seals plain to it (no prompt); the result is a base64url token.
func Seal(plain []byte) (string, error) {
	blob, pub, err := se.create()
	if err != nil {
		return "", err
	}
	peer, err := ecdh.P256().NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	shared, err := eph.ECDH(peer)
	if err != nil {
		return "", err
	}
	gcm, err := aead(shared)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	s := Sealed{V: version, Key: blob, Ephemeral: eph.PublicKey().Bytes()}
	s.Box = gcm.Seal(nonce, nonce, plain, info)
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Open asks for the person's presence (with reason) and opens a token of Seal.
func Open(token, reason string) ([]byte, error) {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errors.New("invalid sealed secret")
	}
	var s Sealed
	if err := json.Unmarshal(data, &s); err != nil || s.V != version || len(s.Ephemeral) != 65 || len(s.Box) < 12 {
		return nil, errors.New("invalid sealed secret")
	}
	shared, err := se.agree(s.Key, s.Ephemeral, reason)
	if err != nil {
		return nil, err
	}
	gcm, err := aead(shared)
	if err != nil {
		return nil, err
	}
	n := gcm.NonceSize()
	plain, err := gcm.Open(nil, s.Box[:n], s.Box[n:], info)
	if err != nil {
		return nil, errors.New("the sealed secret does not open with this key")
	}
	return plain, nil
}
