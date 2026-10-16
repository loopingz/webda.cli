package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
)

// PKCE holds a code verifier and its S256 challenge.
type PKCE struct{ Verifier, Challenge string }

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewPKCE generates a verifier (32 random bytes) and its S256 challenge.
func NewPKCE() (PKCE, error) {
	v, err := randomString(32)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return PKCE{Verifier: v, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// RandomState returns a 16 random bytes base64url state.
func RandomState() (string, error) { return randomString(16) }

// AuthorizeParams are the inputs of the authorization request.
type AuthorizeParams struct{ ClientID, RedirectURI, State, Challenge, Scope, Hostname, Prompt string }

// AuthorizeURL builds the browser authorization URL.
func AuthorizeURL(m *Metadata, p AuthorizeParams) string {
	u, err := url.Parse(m.AuthorizationEndpoint)
	if err != nil {
		return m.AuthorizationEndpoint
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("state", p.State)
	q.Set("code_challenge", p.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", p.Scope)
	if p.Hostname != "" {
		q.Set("hostname", p.Hostname)
	}
	if p.Prompt != "" {
		q.Set("prompt", p.Prompt)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
