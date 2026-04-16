package tokenstore

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SSHAgentStore encrypts tokens using key material derived from an SSH agent signature.
type SSHAgentStore struct {
	configDir string
	deriveKey func(name string) ([32]byte, error) // injectable for testing
}

// NewSSHAgentStore creates an SSHAgentStore if an SSH agent is available.
func NewSSHAgentStore(configDir string) (*SSHAgentStore, error) {
	s := &SSHAgentStore{configDir: configDir}
	s.deriveKey = s.deriveKeyFromAgent
	// Verify agent is reachable and has keys
	if _, err := s.getAgentKey(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SSHAgentStore) getAgentKey() (ssh.PublicKey, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to ssh agent: %w", err)
	}
	defer conn.Close()
	ag := agent.NewClient(conn)
	keys, err := ag.List()
	if err != nil {
		return nil, fmt.Errorf("cannot list agent keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys in ssh agent")
	}
	return keys[0], nil
}

func (s *SSHAgentStore) deriveKeyFromAgent(name string) ([32]byte, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return [32]byte{}, err
	}
	defer conn.Close()
	ag := agent.NewClient(conn)
	keys, err := ag.List()
	if err != nil || len(keys) == 0 {
		return [32]byte{}, fmt.Errorf("no agent keys available")
	}
	challenge := []byte("webda-cli-token-encryption-" + name)
	sig, err := ag.Sign(keys[0], challenge)
	if err != nil {
		return [32]byte{}, fmt.Errorf("agent sign failed: %w", err)
	}
	return sha256.Sum256(sig.Blob), nil
}

func (s *SSHAgentStore) tokenFile(name string) string {
	return filepath.Join(s.configDir, name+".tok")
}

func (s *SSHAgentStore) Load(name string) (TokenInfo, error) {
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

func (s *SSHAgentStore) Save(name string, ti TokenInfo) error {
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
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.tokenFile(name), ciphertext, 0o600)
}

func (s *SSHAgentStore) Delete(name string) error {
	return os.Remove(s.tokenFile(name))
}
