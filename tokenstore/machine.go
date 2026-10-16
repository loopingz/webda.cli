package tokenstore

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// MachineStore encrypts tokens with a key derived from machine identity + random salt.
type MachineStore struct {
	configDir string
}

// NewMachineStore creates a MachineStore.
func NewMachineStore(configDir string) *MachineStore {
	return &MachineStore{configDir: configDir}
}

func (s *MachineStore) tokenFile(name string) string {
	return filepath.Join(s.configDir, name+".tok")
}

func (s *MachineStore) saltFile(name string) string {
	return filepath.Join(s.configDir, name+".tok.salt")
}

func (s *MachineStore) deriveKey(name string) ([32]byte, error) {
	salt, err := s.loadOrCreateSalt(name)
	if err != nil {
		return [32]byte{}, err
	}
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME") // Windows
	}
	material := fmt.Sprintf("%s:%s:%s", hostname, username, salt)
	return sha256.Sum256([]byte(material)), nil
}

func (s *MachineStore) loadOrCreateSalt(name string) ([]byte, error) {
	path := s.saltFile(name)
	if data, err := os.ReadFile(path); err == nil && len(data) == 32 {
		return data, nil
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if err := writePrivate(s.configDir, path, salt); err != nil {
		return nil, err
	}
	return salt, nil
}

func (s *MachineStore) Load(name string) (TokenInfo, error) {
	data, err := os.ReadFile(s.tokenFile(name))
	if err != nil {
		return TokenInfo{}, err
	}
	key, err := s.deriveKey(name)
	if err != nil {
		return TokenInfo{}, err
	}
	plaintext, err := Decrypt(key[:], data)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("decrypt failed: %w", err)
	}
	return UnmarshalTokenInfo(plaintext)
}

func (s *MachineStore) Save(name string, ti TokenInfo) error {
	plaintext, err := MarshalTokenInfo(ti)
	if err != nil {
		return err
	}
	key, err := s.deriveKey(name)
	if err != nil {
		return err
	}
	ciphertext, err := Encrypt(key[:], plaintext)
	if err != nil {
		return err
	}
	return writePrivate(s.configDir, s.tokenFile(name), ciphertext)
}

func (s *MachineStore) Delete(name string) error {
	_ = os.Remove(s.saltFile(name)) // best-effort
	return os.Remove(s.tokenFile(name))
}
