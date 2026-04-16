package tokenstore

import (
	"github.com/zalando/go-keyring"
)

const serviceName = "webda-cli"

// KeyringStore persists tokens in the system keyring (macOS Keychain,
// Linux Secret Service, Windows Credential Manager).
type KeyringStore struct{}

// Available returns true if the system keyring is accessible.
func (s *KeyringStore) Available() bool {
	const probe = "webda-cli-probe"
	if err := keyring.Set(serviceName, probe, "test"); err != nil {
		return false
	}
	_ = keyring.Delete(serviceName, probe)
	return true
}

func (s *KeyringStore) Load(name string) (TokenInfo, error) {
	data, err := keyring.Get(serviceName, name)
	if err != nil {
		return TokenInfo{}, err
	}
	return UnmarshalTokenInfo([]byte(data))
}

func (s *KeyringStore) Save(name string, ti TokenInfo) error {
	data, err := MarshalTokenInfo(ti)
	if err != nil {
		return err
	}
	return keyring.Set(serviceName, name, string(data))
}

func (s *KeyringStore) Delete(name string) error {
	return keyring.Delete(serviceName, name)
}
