package tokenstore

import (
	"encoding/json"
	"time"
)

// TokenInfo holds the token data persisted across sessions.
type TokenInfo struct {
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	// Server is the origin (scheme://host:port) of the server the token was issued for; empty for tokens saved
	// before this field existed.
	Server string `json:"server,omitempty"`
	// Sequence is only read to detect tokens of the legacy /auth/cli sign-in; never written.
	Sequence string `json:"sequence,omitempty"`
}

// Legacy reports a token of the legacy sign-in (it has a sequence), which the OAuth flow cannot refresh.
func (t TokenInfo) Legacy() bool {
	return t.Sequence != ""
}

// IssuedFor reports whether the token may be sent to the server of the given origin: a token saved for
// another server never is; a token without Server (saved before the field existed) is accepted unless legacy.
func (t TokenInfo) IssuedFor(origin string) bool {
	if t.Legacy() {
		return false
	}
	return t.Server == "" || t.Server == origin
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
