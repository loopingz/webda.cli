package tokenstore

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"refresh_token":"abc","access_token":"def","sequence":"1"}`)

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext should differ from plaintext")
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("roundtrip failed: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ciphertext, err := Encrypt(key1, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Decrypt(key2, ciphertext)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestEncryptInvalidKeyLength(t *testing.T) {
	_, err := Encrypt([]byte("short"), []byte("data"))
	if err == nil {
		t.Fatal("expected error for invalid key length")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	_, err := Decrypt(key, []byte("short"))
	if err == nil {
		t.Fatal("expected error for too-short ciphertext")
	}
}

func TestDecryptInvalidKeyLength(t *testing.T) {
	_, err := Decrypt([]byte("short"), []byte("some data that is long enough"))
	if err == nil {
		t.Fatal("expected error for invalid key length")
	}
}
