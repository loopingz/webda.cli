package tokenstore

import "encoding/json"

// TokenInfo holds the token data persisted across sessions.
type TokenInfo struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	Sequence     string `json:"sequence"`
}

// TokenStore abstracts token persistence across backends.
type TokenStore interface {
	Load(name string) (TokenInfo, error)
	Save(name string, ti TokenInfo) error
	Delete(name string) error
}

// MarshalTokenInfo serializes TokenInfo to JSON.
func MarshalTokenInfo(ti TokenInfo) ([]byte, error) {
	return json.Marshal(ti)
}

// UnmarshalTokenInfo deserializes TokenInfo from JSON.
func UnmarshalTokenInfo(data []byte) (TokenInfo, error) {
	var ti TokenInfo
	err := json.Unmarshal(data, &ti)
	return ti, err
}

// NewTokenStore returns the best available token storage backend.
// Priority: system keyring > SSH agent > machine-bound encryption.
func NewTokenStore(configDir string) TokenStore {
	// Try keyring
	ks := &KeyringStore{}
	if ks.Available() {
		return ks
	}

	// Try SSH agent
	if ss, err := NewSSHAgentStore(configDir); err == nil {
		return ss
	}

	// Fallback to machine-bound
	return NewMachineStore(configDir)
}
