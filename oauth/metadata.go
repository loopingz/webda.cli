// Package oauth implements the client side of the Webda OAuth 2.1 authorization server.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Metadata is the authorization server discovery document.
type Metadata struct {
	Issuer                      string `json:"issuer"`
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	RevocationEndpoint          string `json:"revocation_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	ResultURI                   string `json:"webda_result_uri"`
	OperationsURI               string `json:"webda_operations_uri"`
}

// ErrNotSupported is returned when the server exposes no OAuth discovery document.
var ErrNotSupported = errors.New("this server does not support OAuth sign-in")

const maxBody = 1 << 20

// Discover fetches <baseURL>/.well-known/oauth-authorization-server; 404 gives ErrNotSupported.
func Discover(ctx context.Context, hc *http.Client, baseURL string) (*Metadata, error) {
	u := strings.TrimRight(baseURL, "/") + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotSupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth discovery failed: HTTP %d", resp.StatusCode)
	}
	var m Metadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&m); err != nil {
		return nil, fmt.Errorf("oauth discovery: invalid document: %w", err)
	}
	if m.TokenEndpoint == "" || m.AuthorizationEndpoint == "" {
		return nil, errors.New("oauth discovery: document lacks token_endpoint or authorization_endpoint")
	}
	return &m, nil
}

type metadataCache struct {
	BaseURL  string    `json:"base_url"`
	Metadata *Metadata `json:"metadata"`
}

// LoadOrDiscover reads cachePath when present and for the same base URL, else discovers and writes it (0600).
func LoadOrDiscover(ctx context.Context, hc *http.Client, baseURL, cachePath string) (*Metadata, error) {
	base := strings.TrimRight(baseURL, "/")
	if data, err := os.ReadFile(cachePath); err == nil {
		var c metadataCache
		if json.Unmarshal(data, &c) == nil && c.BaseURL == base && c.Metadata != nil &&
			c.Metadata.TokenEndpoint != "" && c.Metadata.AuthorizationEndpoint != "" {
			return c.Metadata, nil
		}
	}
	m, err := Discover(ctx, hc, base)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(metadataCache{BaseURL: base, Metadata: m}, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cachePath, data, 0o600); err != nil {
		return nil, err
	}
	return m, nil
}
