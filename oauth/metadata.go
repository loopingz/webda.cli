// Package oauth implements the client side of the Webda OAuth 2.1 authorization server.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	hc = noRedirect(hc)
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
	if err := validate(baseURL, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// noRedirect returns a shallow copy of hc that never follows redirects.
func noRedirect(hc *http.Client) *http.Client {
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// Origin returns scheme://host:port of an absolute URL, with the default port made explicit
// ("" when raw is not an absolute URL with a host).
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Hostname() == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// validate checks the required endpoints are present and every URL shares the base URL origin.
// A webda_result_uri on another origin (a result page hosted elsewhere) is dropped: the built-in page is used.
func validate(baseURL string, m *Metadata) error {
	want := Origin(baseURL)
	if want == "" {
		return fmt.Errorf("oauth discovery: invalid base URL %q", baseURL)
	}
	if m.ResultURI != "" && Origin(m.ResultURI) != want {
		m.ResultURI = ""
	}
	fields := []struct {
		name, value string
		required    bool
	}{
		{"issuer", m.Issuer, true},
		{"token_endpoint", m.TokenEndpoint, true},
		{"authorization_endpoint", m.AuthorizationEndpoint, true},
		{"revocation_endpoint", m.RevocationEndpoint, false},
		{"device_authorization_endpoint", m.DeviceAuthorizationEndpoint, false},
		{"webda_operations_uri", m.OperationsURI, false},
	}
	for _, f := range fields {
		if f.value == "" {
			if f.required {
				return fmt.Errorf("oauth discovery: document lacks %s", f.name)
			}
			continue
		}
		if Origin(f.value) != want {
			return fmt.Errorf("oauth discovery: %s %q is not on the server origin", f.name, f.value)
		}
	}
	return nil
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
			validate(base, c.Metadata) == nil {
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
	if err := writeAtomic(cachePath, data); err != nil {
		return nil, err
	}
	return m, nil
}

// writeAtomic writes data to a 0600 temp file next to path then renames it over path.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".oauth-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(name, 0o600)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
