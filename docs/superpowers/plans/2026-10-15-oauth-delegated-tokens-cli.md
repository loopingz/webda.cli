# Delegated OAuth Tokens: webda.cli Implementation Plan (plan 3 of 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the CLI's legacy `/auth/cli` + `/auth/refresh` sign-in with standard OAuth 2.1:
- authorization code + PKCE over a loopback redirect, with a device-code fallback;
- rotating refresh tokens and revocation on logout;
- profiles that carry a client id and a scope;
- operations from `webda_operations_uri`, with an OpenAPI fallback;
- a clear hint when a token's scope does not cover an operation.

**Architecture:**
- **`oauth/` package:** a new package holds the protocol: discovery, PKCE/state, the authorize URL, the token and revocation calls, device authorization and polling, and the loopback listener. Its HTTP client, URL opener and clock are injectable, so every step is tested against an `httptest` authorization server.
- **`main` package:** keeps the CLI wiring: profiles, `auth`/`logout`, operations.
- **`webdaclient`:** refreshes through the token endpoint with single-flight rotation.
- **`tokenstore`:** keeps its three backends and gains the new fields.

**Tech Stack:** Go 1.25, cobra, `gopkg.in/yaml.v3`, stdlib `net/http`/`crypto`, `github.com/mdp/qrterminal/v3` (terminal QR code), stdlib `testing` + `httptest`.

**Spec:** `docs/superpowers/specs/2026-10-15-oauth-delegated-tokens-design.md`, section 4. The spec is also posted on loopingz/webda.cli#14. Copy it into this repo's `docs/superpowers/specs/` in Task 1 if it is not there. The server side is `@webda/oauth`, shipped in loopingz/webda.io#928 and released in `4.0.0-beta.10`.

## Global Constraints

- Conventional commits (release-please is used). Each commit ends with a blank line and then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- CI runs:
  - `go test -race ./...`, `go vet ./...` and `golangci-lint` v2 (errcheck, staticcheck, gocritic, gofmt, goimports and others).
  - The pre-push hook runs the same checks. Every task leaves all three green.
- Defaults (spec §4):
  - `client_id`: `webda-cli`.
  - `scope`: `"* offline_access"`.
  - Loopback: listen on `127.0.0.1:0` (loopback only, never all interfaces). The redirect URI is `http://127.0.0.1:<port>/callback`.
  - Sign-in timeout: 5 minutes.
- Discovery: `GET <url>/.well-known/oauth-authorization-server`, cached per profile. A 404 gives the error `this server does not support OAuth sign-in`.
- Device flow is used when:
  - `--device` is passed;
  - the browser cannot be opened;
  - `SSH_CONNECTION` is set;
  - on Linux, when neither `DISPLAY` nor `WAYLAND_DISPLAY` is set.
- Device flow output: print `verification_uri`, the `user_code` and a terminal QR code of `verification_uri_complete`. Poll at `interval` and add 5 s on each `slow_down`.
- After the loopback callback, the browser gets a `302` to `webda_result_uri?status=success` (or `?status=error&error=<code>`). Fallback: a minimal built-in HTML page.
- `logout`: `POST revocation_endpoint` with the refresh token (form `token`, `token_type_hint=refresh_token`, `client_id`), then delete the stored token.
- Token store:
  - `TokenInfo` adds `ExpiresAt` and `Scope`.
  - A stored token that still has the legacy `Sequence` is discarded, and the user is asked to run `auth` again.
- Refresh: `grant_type=refresh_token` to `token_endpoint`. Store the rotated refresh token. Refresh before expiry, and retry once on a 401, using `expires_in`.
- Operations:
  - Fetch `webda_operations_uri`, falling back to `<url>/operations`.
  - On a 404, use the OpenAPI document at `<url>/`. It is only served when the server sets `exposeOpenAPI`. Map each `operationId` to its method and path.
  - Path parameters come from flags. For `GET`, flags become query parameters; for other methods, a JSON body.
- `403 insufficient_scope`: print `this token does not cover <operation>; run <bin> auth --scope …`. `<bin>` is the invoked binary name.
- Profiles in `~/.webdacli/config.yaml`:
  - A value is either a URL string (backward compatible) or an object `{ url, client_id?, scope? }`.
  - The special key `update` keeps working.
  - `setup` gains `--client-id` and `--scope`.

## Review Focus

1. **A token response without a new refresh token must keep the current one, and a rotated one must be saved before it is used.** If the process exits right after a refresh, the stored refresh token must be the new one. Re-saving the old token makes the next run present a rotated token, which revokes the whole family on the server. Test in Task 6.
2. **Two operations hitting 401 at the same time make one refresh, not two.** Rotated refresh tokens are single-use. Test in Task 6.
3. **The loopback listener must ignore requests that are not the authorization callback.** That covers a browser's `/favicon.ico`, a callback with the wrong `state`, and a request from another local process. It answers them with 404/400 and keeps waiting until the real callback or the timeout. Test in Task 3.
4. **An old `config.yaml` that uses only URL strings, plus `update: prompt`, must load unchanged.** Rewriting it with `setup` must keep string entries as strings. Test in Task 1.
5. **A stored legacy token (with `sequence`) must not crash or loop.** On first use it is deleted and the user is told to run `auth`. With a TTY, `auth` starts right away. It must never be sent to `/oauth/token`. Test in Task 5.

---

## File Structure

- `profile.go` (new): `Profile`, `Config`, YAML load/save with the backward-compatible format. Used by `main.go` and `startup.go`.
- `oauth/` (new package):
  - `metadata.go`: discovery and its per-profile cache.
  - `pkce.go`: PKCE verifier/challenge and the random `state`.
  - `token.go`: authorize URL, code exchange, refresh, revoke, typed errors.
  - `loopback.go`: the `127.0.0.1` callback listener.
  - `device.go`: device authorization and polling.
  - `fake_test.go`: an `httptest` authorization server shared by the package tests.
- `auth.go` (new, package main): picks the browser or device flow and builds `tokenstore.TokenInfo`. Provides the `auth`, `logout` and first-use authentication used by `startup.go`/`main.go`.
- `tokenstore/store.go` (modify): new `TokenInfo` fields and `Legacy()`.
- `webdaclient/client.go` (modify): OAuth refresh, single-flight, `ExpiresAt`. Removes the legacy exchange and the background loop.
- `main.go`, `startup.go`, `execution.go`, `cmdtree.go` (modify): wiring, operations source, OpenAPI dispatch, the `insufficient_scope` hint, `summary`.
- `README.md` (modify): rewritten "Authentication" section and the files table.

---

### Task 1: Profiles with a client id and a scope

**Files:**
- Create: `profile.go`, `profile_test.go`
- Modify: `main.go` (`loadConfig` and its callers, ~75-86, 419-421, 538), `startup.go` (`profiles` ~78-90, `setup` ~92-144)

**Interfaces:**
- Produces:

```go
const defaultClientID = "webda-cli"
const defaultScope = "* offline_access"

type Profile struct {
    URL      string `yaml:"url"`
    ClientID string `yaml:"client_id,omitempty"`
    Scope    string `yaml:"scope,omitempty"`
}
func (p Profile) EffectiveClientID() string // ClientID or defaultClientID
func (p Profile) EffectiveScope() string    // Scope or defaultScope

type Config struct {
    Profiles map[string]Profile
    Update   string // "silent" | "prompt" | ""
}
func loadConfig() (Config, error)      // reads configDir()/config.yaml
func saveConfig(c Config) error        // writes 0600, string form for URL-only profiles
func (c *Config) UnmarshalYAML(n *yaml.Node) error
func (c Config) MarshalYAML() (any, error)
```

- [ ] **Step 1: Write the failing tests.**

```go
// profile_test.go
package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfig_LegacyStringsAndUpdate(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("myapp: https://app.example.com\nupdate: prompt\n"), &c); err != nil {
		t.Fatal(err)
	}
	if c.Update != "prompt" {
		t.Fatalf("update = %q", c.Update)
	}
	p := c.Profiles["myapp"]
	if p.URL != "https://app.example.com" || p.EffectiveClientID() != "webda-cli" || p.EffectiveScope() != "* offline_access" {
		t.Fatalf("profile = %+v", p)
	}
	if _, ok := c.Profiles["update"]; ok {
		t.Fatal("update must not be a profile")
	}
}

func TestConfig_ObjectProfile(t *testing.T) {
	var c Config
	in := "lc:\n  url: https://my.loopingz.com\n  client_id: loopcenter-cli\n  scope: Task.* offline_access\n"
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["lc"]
	if p.URL != "https://my.loopingz.com" || p.ClientID != "loopcenter-cli" || p.Scope != "Task.* offline_access" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestConfig_RoundTripKeepsStrings(t *testing.T) {
	c := Config{Update: "silent", Profiles: map[string]Profile{
		"a": {URL: "https://a.example"},
		"b": {URL: "https://b.example", Scope: "Task.*"},
	}}
	out, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "a: https://a.example") {
		t.Fatalf("URL-only profile must stay a string:\n%s", s)
	}
	if !strings.Contains(s, "b:\n    url: https://b.example") || !strings.Contains(s, "scope: Task.*") {
		t.Fatalf("object profile:\n%s", s)
	}
	var back Config
	if err := yaml.Unmarshal(out, &back); err != nil || back.Profiles["b"].Scope != "Task.*" || back.Update != "silent" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}

func TestConfig_RejectsBadEntries(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("x:\n  - 1\n"), &c); err == nil {
		t.Fatal("a sequence is not a profile")
	}
}
```

- [ ] **Step 2: Run them to make sure they fail.**

Run: `go test -run TestConfig ./...`
Expected: FAIL (`undefined: Config`).

- [ ] **Step 3: Implement `profile.go`.**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultClientID = "webda-cli"
	defaultScope    = "* offline_access"
	updateKey       = "update"
)

// Profile is one server the CLI talks to.
type Profile struct {
	URL      string `yaml:"url"`
	ClientID string `yaml:"client_id,omitempty"`
	Scope    string `yaml:"scope,omitempty"`
}

// EffectiveClientID returns the OAuth client id, webda-cli by default.
func (p Profile) EffectiveClientID() string {
	if p.ClientID != "" {
		return p.ClientID
	}
	return defaultClientID
}

// EffectiveScope returns the requested scope, "* offline_access" by default.
func (p Profile) EffectiveScope() string {
	if p.Scope != "" {
		return p.Scope
	}
	return defaultScope
}

// Config is ~/.webdacli/config.yaml: profile name -> URL string or Profile object, plus the update mode.
type Config struct {
	Profiles map[string]Profile
	Update   string
}

// UnmarshalYAML accepts the legacy `name: url` form and the `name: {url, client_id, scope}` form.
func (c *Config) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("config: expected a mapping")
	}
	c.Profiles = map[string]Profile{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		if key == updateKey {
			c.Update = val.Value
			continue
		}
		switch val.Kind {
		case yaml.ScalarNode:
			c.Profiles[key] = Profile{URL: val.Value}
		case yaml.MappingNode:
			var p Profile
			if err := val.Decode(&p); err != nil {
				return fmt.Errorf("config: profile %q: %w", key, err)
			}
			c.Profiles[key] = p
		default:
			return fmt.Errorf("config: profile %q must be a URL or an object", key)
		}
	}
	return nil
}

// MarshalYAML writes URL-only profiles as plain strings (readable by older CLI versions).
func (c Config) MarshalYAML() (any, error) {
	out := map[string]any{}
	for name, p := range c.Profiles {
		if p.ClientID == "" && p.Scope == "" {
			out[name] = p.URL
		} else {
			out[name] = p
		}
	}
	if c.Update != "" {
		out[updateKey] = c.Update
	}
	return out, nil
}

func loadConfig() (Config, error) {
	data, err := os.ReadFile(filepath.Join(configDir(), configFileName))
	if err != nil {
		return Config{Profiles: map[string]Profile{}}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{Profiles: map[string]Profile{}}, err
	}
	return c, nil
}

func saveConfig(c Config) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir(), configFileName), data, 0o600)
}
```

- [ ] **Step 4: Switch the callers.**
  - Remove the old `loadConfig` from `main.go`. In `runCLI`, use `cfg.Profiles[profile]`. The base URL becomes `prof.URL`, and `prof` is passed down where later tasks need it.
  - The update mode becomes `cfg.Update`.
  - `profiles` prints `name\turl`.
  - `setup`:
    - Add `--client-id` and `--scope` flags, stored only when non-empty.
    - Load the existing config, set `cfg.Profiles[name] = Profile{URL, ClientID, Scope}` and call `saveConfig`.
    - Keep its validation: the name cannot be `update`, and the URL must be http(s) with a host and no userinfo, query or fragment.
  - Update every existing test that wrote or read `map[string]string` configs.

- [ ] **Step 5: Run the tests.**

Run: `go test -race ./... && go vet ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add profile.go profile_test.go main.go startup.go *_test.go docs/superpowers/specs/2026-10-15-oauth-delegated-tokens-design.md
git commit -m "feat: profiles with an OAuth client id and scope"
```

---

### Task 2: OAuth protocol package (discovery, PKCE, tokens, revocation, device flow)

**Files:**
- Create: `oauth/metadata.go`, `oauth/pkce.go`, `oauth/token.go`, `oauth/device.go`
- Test: `oauth/fake_test.go`, `oauth/oauth_test.go`

**Interfaces:**
- Produces:

```go
package oauth

type Metadata struct {
    Issuer                      string `json:"issuer"`
    AuthorizationEndpoint       string `json:"authorization_endpoint"`
    TokenEndpoint               string `json:"token_endpoint"`
    RevocationEndpoint          string `json:"revocation_endpoint"`
    DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
    ResultURI                   string `json:"webda_result_uri"`
    OperationsURI               string `json:"webda_operations_uri"`
}
var ErrNotSupported = errors.New("this server does not support OAuth sign-in")
// Discover fetches <baseURL>/.well-known/oauth-authorization-server; 404 → ErrNotSupported.
func Discover(ctx context.Context, hc *http.Client, baseURL string) (*Metadata, error)
// LoadOrDiscover reads cachePath (JSON) when present and for the same base URL, else Discover and write it (0600).
func LoadOrDiscover(ctx context.Context, hc *http.Client, baseURL, cachePath string) (*Metadata, error)

type PKCE struct{ Verifier, Challenge string }
func NewPKCE() (PKCE, error)          // 32 random bytes base64url verifier, S256 challenge
func RandomState() (string, error)    // 16 random bytes base64url

type AuthorizeParams struct{ ClientID, RedirectURI, State, Challenge, Scope, Hostname, Prompt string }
func AuthorizeURL(m *Metadata, p AuthorizeParams) string

type Tokens struct {
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token,omitempty"`
    ExpiresIn    int    `json:"expires_in"`
    Scope        string `json:"scope"`
}
func (t Tokens) ExpiresAt(now time.Time) time.Time // now + ExpiresIn (300 s when <= 0)

// Error is an RFC 6749 error answer.
type Error struct{ Status int; Code, Description string }
func (e *Error) Error() string

func ExchangeCode(ctx context.Context, hc *http.Client, m *Metadata, clientID, code, verifier, redirectURI string) (*Tokens, error)
func Refresh(ctx context.Context, hc *http.Client, m *Metadata, clientID, refreshToken string) (*Tokens, error)
func Revoke(ctx context.Context, hc *http.Client, m *Metadata, clientID, token string) error

type DeviceCode struct {
    DeviceCode              string `json:"device_code"`
    UserCode                string `json:"user_code"`
    VerificationURI         string `json:"verification_uri"`
    VerificationURIComplete string `json:"verification_uri_complete"`
    ExpiresIn               int    `json:"expires_in"`
    Interval                int    `json:"interval"`
}
func StartDevice(ctx context.Context, hc *http.Client, m *Metadata, clientID, scope, hostname string) (*DeviceCode, error)
// PollDevice polls the token endpoint until tokens, a terminal error, ctx done or expiry; sleep is injectable (tests).
func PollDevice(ctx context.Context, hc *http.Client, m *Metadata, clientID string, dc *DeviceCode, sleep func(context.Context, time.Duration) error) (*Tokens, error)
```

Request formats, which must match the server (`@webda/oauth`):
- Token and revocation calls are `application/x-www-form-urlencoded` POSTs that always include `client_id`.
- Code exchange: `grant_type=authorization_code`, `code`, `code_verifier`, `redirect_uri`.
- Refresh: `grant_type=refresh_token`, `refresh_token`.
- Device token: `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`.
- Device authorization: `client_id`, `scope`, `hostname`.
- Revocation: `token`, `token_type_hint=refresh_token`; any 2xx counts as success.
- Errors are JSON `{error, error_description}`.

- [ ] **Step 1: Write the fake authorization server** (`oauth/fake_test.go`). It is reused by Tasks 3–6 through their own copies or via exported test helpers in an `oauthtest` package. Create `oauth/oauthtest/fake.go` (non-test file, so other packages can import it):

```go
// Package oauthtest provides an in-memory OAuth 2.1 authorization server for tests.
package oauthtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
)

// Server is a fake Webda OAuth server: discovery, token (code, refresh with rotation, device), revoke, device_authorization.
type Server struct {
	*httptest.Server
	mu          sync.Mutex
	Codes       map[string]string // code -> expected code_verifier
	Refresh     map[string]bool   // live refresh tokens
	Revoked     []string
	DevicePolls int32
	// DeviceAnswers is consumed in order by device token polls: "authorization_pending", "slow_down", "access_denied" or "ok"
	DeviceAnswers []string
	TokenCalls    int32
	NoOAuth       bool // discovery answers 404
	counter       int
}

func New() *Server {
	s := &Server{Codes: map[string]string{}, Refresh: map[string]bool{"r0": true}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.metadata)
	mux.HandleFunc("/oauth/token", s.token)
	mux.HandleFunc("/oauth/revoke", s.revoke)
	mux.HandleFunc("/oauth/device_authorization", s.device)
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	if s.NoOAuth {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, map[string]string{
		"issuer":                        s.URL,
		"authorization_endpoint":        s.URL + "/oauth/authorize",
		"token_endpoint":                s.URL + "/oauth/token",
		"revocation_endpoint":           s.URL + "/oauth/revoke",
		"device_authorization_endpoint": s.URL + "/oauth/device_authorization",
		"webda_result_uri":              s.URL + "/oauth/done",
		"webda_operations_uri":          s.URL + "/operations",
	})
}

func (s *Server) issue() map[string]any {
	s.counter++
	n := strconv.Itoa(s.counter)
	s.Refresh["r"+n] = true
	return map[string]any{"access_token": "a" + n, "token_type": "Bearer", "expires_in": 900, "scope": "*", "refresh_token": "r" + n}
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.TokenCalls, 1)
	_ = r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Form.Get("client_id") == "" {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		verifier, ok := s.Codes[r.Form.Get("code")]
		if !ok || verifier != r.Form.Get("code_verifier") || r.Form.Get("redirect_uri") == "" {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(s.Codes, r.Form.Get("code"))
		writeJSON(w, 200, s.issue())
	case "refresh_token":
		rt := r.Form.Get("refresh_token")
		if !s.Refresh[rt] {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		delete(s.Refresh, rt) // rotation: single use
		writeJSON(w, 200, s.issue())
	case "urn:ietf:params:oauth:grant-type:device_code":
		atomic.AddInt32(&s.DevicePolls, 1)
		answer := "ok"
		if len(s.DeviceAnswers) > 0 {
			answer, s.DeviceAnswers = s.DeviceAnswers[0], s.DeviceAnswers[1:]
		}
		if answer != "ok" {
			writeJSON(w, 400, map[string]string{"error": answer})
			return
		}
		writeJSON(w, 200, s.issue())
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.mu.Lock()
	s.Revoked = append(s.Revoked, r.Form.Get("token"))
	delete(s.Refresh, r.Form.Get("token"))
	s.mu.Unlock()
	w.WriteHeader(200)
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"device_code": "BCDFGHJK.secret", "user_code": "BCDF-GHJK",
		"verification_uri": s.URL + "/oauth/device", "verification_uri_complete": s.URL + "/oauth/device?user_code=BCDF-GHJK",
		"expires_in": 600, "interval": 5,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 2: Write the failing tests** (`oauth/oauth_test.go`):

```go
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/oauth/oauthtest"
)

func TestDiscover(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	m, err := Discover(context.Background(), http.DefaultClient, s.URL)
	if err != nil || m.TokenEndpoint != s.URL+"/oauth/token" || m.ResultURI != s.URL+"/oauth/done" {
		t.Fatalf("%+v %v", m, err)
	}
	s.NoOAuth = true
	if _, err := Discover(context.Background(), http.DefaultClient, s.URL); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("want ErrNotSupported, got %v", err)
	}
}

func TestLoadOrDiscoverCaches(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	cache := filepath.Join(t.TempDir(), "p.oauth.json")
	if _, err := LoadOrDiscover(context.Background(), http.DefaultClient, s.URL, cache); err != nil {
		t.Fatal(err)
	}
	s.NoOAuth = true // second call must not hit the server
	if _, err := LoadOrDiscover(context.Background(), http.DefaultClient, s.URL, cache); err != nil {
		t.Fatalf("cached discovery: %v", err)
	}
	// another base URL invalidates the cache
	if _, err := LoadOrDiscover(context.Background(), http.DefaultClient, s.URL+"/other", cache); err == nil {
		t.Fatal("a different base URL must rediscover")
	}
}

func TestPKCE(t *testing.T) {
	p, err := NewPKCE()
	if err != nil || len(p.Verifier) < 43 || len(p.Verifier) > 128 {
		t.Fatalf("%+v %v", p, err)
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	if p.Challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("challenge is not S256(verifier)")
	}
}

func TestAuthorizeURL(t *testing.T) {
	m := &Metadata{AuthorizationEndpoint: "https://x/oauth/authorize"}
	u, _ := url.Parse(AuthorizeURL(m, AuthorizeParams{ClientID: "webda-cli", RedirectURI: "http://127.0.0.1:5/callback", State: "s", Challenge: "c", Scope: "* offline_access", Hostname: "h"}))
	q := u.Query()
	want := map[string]string{"response_type": "code", "client_id": "webda-cli", "redirect_uri": "http://127.0.0.1:5/callback", "state": "s",
		"code_challenge": "c", "code_challenge_method": "S256", "scope": "* offline_access", "hostname": "h"}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Has("prompt") {
		t.Error("prompt only when set")
	}
}

func TestExchangeRefreshRevoke(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	m, _ := Discover(context.Background(), http.DefaultClient, s.URL)
	s.Codes["c1"] = "v1"
	tok, err := ExchangeCode(context.Background(), http.DefaultClient, m, "webda-cli", "c1", "v1", "http://127.0.0.1:5/callback")
	if err != nil || tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("%+v %v", tok, err)
	}
	if _, err := ExchangeCode(context.Background(), http.DefaultClient, m, "webda-cli", "c1", "v1", "http://127.0.0.1:5/callback"); err == nil {
		t.Fatal("code reuse must fail")
	} else {
		var oe *Error
		if !errors.As(err, &oe) || oe.Code != "invalid_grant" || oe.Status != 400 {
			t.Fatalf("want invalid_grant, got %v", err)
		}
	}
	next, err := Refresh(context.Background(), http.DefaultClient, m, "webda-cli", tok.RefreshToken)
	if err != nil || next.RefreshToken == tok.RefreshToken {
		t.Fatalf("rotation: %+v %v", next, err)
	}
	if err := Revoke(context.Background(), http.DefaultClient, m, "webda-cli", next.RefreshToken); err != nil || len(s.Revoked) != 1 {
		t.Fatalf("revoke: %v %v", s.Revoked, err)
	}
	now := time.Unix(1000, 0)
	if !next.ExpiresAt(now).Equal(now.Add(900 * time.Second)) {
		t.Fatal("ExpiresAt")
	}
	if !(Tokens{}).ExpiresAt(now).Equal(now.Add(300 * time.Second)) {
		t.Fatal("default expiry 300 s")
	}
}

func TestDevicePolling(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	m, _ := Discover(context.Background(), http.DefaultClient, s.URL)
	dc, err := StartDevice(context.Background(), http.DefaultClient, m, "webda-cli", "* offline_access", "host")
	if err != nil || dc.UserCode != "BCDF-GHJK" {
		t.Fatalf("%+v %v", dc, err)
	}
	s.DeviceAnswers = []string{"authorization_pending", "slow_down", "authorization_pending", "ok"}
	var waits []time.Duration
	sleep := func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	tok, err := PollDevice(context.Background(), http.DefaultClient, m, "webda-cli", dc, sleep)
	if err != nil || tok.AccessToken == "" {
		t.Fatalf("%+v %v", tok, err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(waits) != len(want) {
		t.Fatalf("waits %v", waits)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits %v, want %v", waits, want)
		}
	}
	s.DeviceAnswers = []string{"access_denied"}
	if _, err := PollDevice(context.Background(), http.DefaultClient, m, "webda-cli", dc, sleep); err == nil {
		t.Fatal("access_denied must stop polling")
	}
}
```

- [ ] **Step 3: Run them to make sure they fail.**

Run: `go test ./oauth/...`
Expected: FAIL (undefined symbols).

- [ ] **Step 4: Implement the package.** Notes that pin the behaviour:
  - Every function takes the `*http.Client` it is given and never `http.DefaultClient` directly. All requests use the `ctx`.
  - `Discover` trims a trailing `/` from `baseURL`. On a non-200 other than 404 it returns an error that includes the status. It decodes into `Metadata` and requires `token_endpoint` and `authorization_endpoint` to be non-empty.
  - `LoadOrDiscover`:
    - The cache file is JSON `{ "base_url": "...", "metadata": {...} }`.
    - Use the cache only when `base_url` matches. Otherwise discover again and rewrite the file with mode 0600.
    - A corrupt cache file is ignored.
  - `NewPKCE`: `crypto/rand` 32 bytes, `base64.RawURLEncoding` verifier, challenge = `RawURLEncoding(sha256(verifier))`. `RandomState`: 16 bytes, same encoding.
  - `AuthorizeURL`: start from `m.AuthorizationEndpoint`. Set `response_type=code`, `client_id`, `redirect_uri`, `state`, `code_challenge`, `code_challenge_method=S256` and `scope`. Add `hostname` and `prompt` only when they are non-empty. Merge with any query the endpoint already has.
  - `postForm(ctx, hc, endpoint, url.Values)`: sets `Content-Type: application/x-www-form-urlencoded` and `Accept: application/json`. It reads at most 1 MiB of body. On a non-2xx it decodes `{error, error_description}` into `*Error{Status, Code, Description}`; when the body is not JSON, `Code` is `"http_<status>"`.
  - Token responses: an `AccessToken` that is not a non-empty string is an `*Error{Code: "invalid_response"}`.
  - `ExpiresAt`: when `ExpiresIn <= 0`, use 300 s.
  - `PollDevice`:
    - Before each request, wait `interval` (minimum 5 s when the server sends 0) with `sleep`. `sleep(ctx, d)` returns `ctx.Err()` when the context ends.
    - `authorization_pending`: continue.
    - `slow_down`: add 5 s to the interval, then continue.
    - `access_denied`, `expired_token` or any other error: return it.
    - Stop with `*Error{Code: "expired_token"}` when the time slept so far exceeds `dc.ExpiresIn`.
    - The default `sleep` used by callers is a `select` on `time.After(d)` and `ctx.Done()`. Export it as `oauth.Sleep`.

- [ ] **Step 5: Run the tests.**

Run: `go test -race ./oauth/... && go vet ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add oauth
git commit -m "feat(oauth): discovery, PKCE, token, revocation and device flow client"
```

---

### Task 3: Loopback redirect listener

**Files:**
- Create: `oauth/loopback.go`
- Test: `oauth/loopback_test.go`

**Interfaces:**
- Produces:

```go
// Loopback is a one-shot authorization callback listener on 127.0.0.1.
type Loopback struct {
    RedirectURI string // http://127.0.0.1:<port>/callback
    // unexported: listener, server, result channel
}
// ListenLoopback binds 127.0.0.1:0 and serves GET /callback.
func ListenLoopback() (*Loopback, error)
// Wait blocks until the callback with the given state arrives (returns the code), an authorization error
// (returns *Error{Code}), ctx is done, or timeout. The browser is answered with a 302 to resultURL?status=…,
// or a built-in page when resultURL is empty. Stray requests never end the wait.
func (l *Loopback) Wait(ctx context.Context, state, resultURL string, timeout time.Duration) (string, error)
func (l *Loopback) Close() error
```

- [ ] **Step 1: Write the failing tests.**

```go
package oauth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func TestLoopbackBindsLoopbackOnly(t *testing.T) {
	l, err := ListenLoopback()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !strings.HasPrefix(l.RedirectURI, "http://127.0.0.1:") || !strings.HasSuffix(l.RedirectURI, "/callback") {
		t.Fatal(l.RedirectURI)
	}
}

func TestLoopbackIgnoresStraysAndReturnsTheCode(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	base := strings.TrimSuffix(l.RedirectURI, "/callback")
	done := make(chan struct{})
	var code string
	var werr error
	go func() {
		code, werr = l.Wait(context.Background(), "s1", "https://srv/oauth/done", 5*time.Second)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if r, _ := noRedirect.Get(base + "/favicon.ico"); r == nil || r.StatusCode != 404 {
		t.Fatalf("favicon: %v", r)
	}
	if r, _ := noRedirect.Get(l.RedirectURI + "?code=x&state=WRONG"); r == nil || r.StatusCode != 400 {
		t.Fatalf("wrong state: %v", r)
	}
	r, err := noRedirect.Get(l.RedirectURI + "?code=c1&state=s1")
	if err != nil || r.StatusCode != 302 || r.Header.Get("Location") != "https://srv/oauth/done?status=success" {
		t.Fatalf("callback: %v %v", r, err)
	}
	<-done
	if werr != nil || code != "c1" {
		t.Fatalf("code %q err %v", code, werr)
	}
}

func TestLoopbackReportsAuthorizationErrors(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		r, _ := noRedirect.Get(l.RedirectURI + "?error=access_denied&state=s1")
		if r == nil || r.Header.Get("Location") != "https://srv/oauth/done?status=error&error=access_denied" {
			t.Errorf("error redirect: %v", r)
		}
	}()
	_, err := l.Wait(context.Background(), "s1", "https://srv/oauth/done", 5*time.Second)
	var oe *Error
	if err == nil || !asError(err, &oe) || oe.Code != "access_denied" {
		t.Fatalf("want access_denied, got %v", err)
	}
}

func TestLoopbackTimesOut(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	if _, err := l.Wait(context.Background(), "s1", "", 100*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestLoopbackBuiltInPageWithoutResultURL(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	go func() { _, _ = l.Wait(context.Background(), "s1", "", 5*time.Second) }()
	time.Sleep(50 * time.Millisecond)
	r, err := noRedirect.Get(l.RedirectURI + "?code=c&state=s1")
	if err != nil || r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("%v %v", r, err)
	}
}
```

Define the small helper `asError(err error, target **Error) bool { return errors.As(err, target) }` in the test file.

- [ ] **Step 2: Run them to make sure they fail.**

Run: `go test ./oauth/ -run Loopback`
Expected: FAIL.

- [ ] **Step 3: Implement.**
  - `ListenLoopback`: `net.Listen("tcp", "127.0.0.1:0")` and an `http.Server{ReadHeaderTimeout: 5 * time.Second}` with one handler.
  - The handler answers `404` for any path other than `/callback` and any method other than GET.
  - Before `Wait` sets the expected state, the handler answers `400`.
  - With the expected state set:
    - Answer `400` when `state` is missing or does not match, and keep waiting.
    - On `error`: redirect to `resultURL?status=error&error=<url-escaped code>` and send `*Error{Code}`.
    - On a non-empty `code`: redirect to `resultURL?status=success` and send the code.
    - With no `resultURL`, write a minimal `text/html` page with a static, escaped message ("Signed in. You can close this window." or "Sign-in failed").
    - After the first result, ignore further callbacks with `410 Gone`.
  - `Wait` uses `select` on the result, `ctx.Done()` and `time.After(timeout)`; on timeout it returns an error containing `timed out (5 minutes)` when `timeout` is 5 minutes.
  - `Close` shuts the server down.

- [ ] **Step 4: Run the tests.**

Run: `go test -race ./oauth/... && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add oauth/loopback.go oauth/loopback_test.go
git commit -m "feat(oauth): loopback redirect listener"
```

---

### Task 4: Token store fields

**Files:**
- Modify: `tokenstore/store.go`
- Test: `tokenstore/store_test.go`

**Interfaces:**
- Produces:

```go
type TokenInfo struct {
    RefreshToken string    `json:"refresh_token"`
    AccessToken  string    `json:"access_token"`
    ExpiresAt    time.Time `json:"expires_at,omitempty"`
    Scope        string    `json:"scope,omitempty"`
    // Sequence is only read to detect tokens of the legacy /auth/cli sign-in; never written.
    Sequence string `json:"sequence,omitempty"`
}
// Legacy reports a token of the legacy sign-in (it has a sequence), which the OAuth flow cannot refresh.
func (t TokenInfo) Legacy() bool
```

- [ ] **Step 1: Write the failing test.**

```go
func TestTokenInfoLegacyAndRoundTrip(t *testing.T) {
	legacy, err := UnmarshalTokenInfo([]byte(`{"refresh_token":"r","access_token":"a","sequence":"12"}`))
	if err != nil || !legacy.Legacy() {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", ExpiresAt: now, Scope: "Task.* offline_access"}
	data, err := MarshalTokenInfo(ti)
	if err != nil || strings.Contains(string(data), "sequence") {
		t.Fatalf("%s %v", data, err)
	}
	back, err := UnmarshalTokenInfo(data)
	if err != nil || back.Legacy() || !back.ExpiresAt.Equal(now) || back.Scope != ti.Scope {
		t.Fatalf("%+v %v", back, err)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails.**

Run: `go test ./tokenstore/ -run TestTokenInfoLegacy`
Expected: FAIL.

- [ ] **Step 3: Implement.** Add the fields and `func (t TokenInfo) Legacy() bool { return t.Sequence != "" }`. Keep `MarshalTokenInfo`/`UnmarshalTokenInfo`. Update the existing tokenstore tests that build `TokenInfo{… Sequence …}` so they use the new fields, apart from the legacy test. The three backends store the JSON as is, so they need no other change.

- [ ] **Step 4: Run the tests.**

Run: `go test -race ./tokenstore/... && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add tokenstore
git commit -m "feat(tokenstore): expiry and scope in stored tokens"
```

---

### Task 5: `auth`, `logout` and first-use sign-in

**Files:**
- Create: `auth.go`, `auth_test.go`
- Modify: `main.go` (replace `acquireToken`, `extractToken`, `exchangeToken`, `callbackPort`; the `auth` command ~454-477 and its stub ~557; add `logout`), `startup.go` (`authenticate` ~191-200; local-invocation list adds `logout` as a network command), `go.mod`/`go.sum` (`github.com/mdp/qrterminal/v3`)
- Modify tests: `main_test.go` (remove `TestExtractToken_*`, `TestExchangeToken_*`; keep the existing-token test adapted), `testutil_test.go` (mock server gains OAuth endpoints or uses `oauthtest`)

**Interfaces:**
- Consumes: Task 1 `Profile` (`EffectiveClientID`, `EffectiveScope`); Task 2 `oauth.*`; Task 3 `oauth.ListenLoopback`; Task 4 `TokenInfo`, `Legacy()`.
- Produces:

```go
// authEnv holds the seams of the sign-in (tests replace them).
type authEnv struct {
    HTTP     *http.Client
    Open     func(url string) error           // browser.OpenURL
    Getenv   func(string) string
    GOOS     string
    Hostname func() (string, error)
    Out      io.Writer                         // stderr
    Sleep    func(context.Context, time.Duration) error
    Timeout  time.Duration                     // 5 * time.Minute
}
func defaultAuthEnv() authEnv
type authOptions struct{ Scope, Prompt string; Device bool }
// metadataFor returns the cached or discovered metadata of a profile (cache: configDir()/<profile>.oauth.json).
func metadataFor(ctx context.Context, env authEnv, profile string, prof Profile) (*oauth.Metadata, error)
// useDeviceFlow reports whether the device flow must be used (flag, SSH, Linux without display).
func useDeviceFlow(env authEnv, opts authOptions) bool
// signIn runs the browser flow (falling back to the device flow when the browser cannot be opened) and stores the tokens.
func signIn(ctx context.Context, env authEnv, store tokenstore.TokenStore, profile string, prof Profile, opts authOptions) (tokenstore.TokenInfo, error)
// signOut revokes the refresh token (best effort) and deletes the stored token.
func signOut(ctx context.Context, env authEnv, store tokenstore.TokenStore, profile string, prof Profile) error
```

- [ ] **Step 1: Write the failing tests** (`auth_test.go`). Use `oauthtest.New()`, an in-memory `tokenstore.TokenStore` (one probably exists in the tests already; otherwise add a small `memStore` test type), and an `env` whose `Open` drives the callback:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/oauth/oauthtest"
	"github.com/loopingz/webda.cli/tokenstore"
)

func testEnv(t *testing.T, open func(string) error) (authEnv, *bytes.Buffer) {
	out := &bytes.Buffer{}
	t.Setenv("HOME", t.TempDir())
	return authEnv{
		HTTP: http.DefaultClient, Open: open, Getenv: func(string) string { return "" }, GOOS: "darwin",
		Hostname: func() (string, error) { return "box", nil }, Out: out,
		Sleep: func(context.Context, time.Duration) error { return nil }, Timeout: 5 * time.Second,
	}, out
}

// browserThatConsents simulates the user approving: it reads the authorize URL, registers the code, then calls back.
func browserThatConsents(s *oauthtest.Server, code string) func(string) error {
	return func(authorize string) error {
		u, _ := url.Parse(authorize)
		q := u.Query()
		// the server "issues" this code; with AcceptAnyVerifier the fake accepts any non-empty code_verifier for it
		s.Codes[code] = "any"
		go func() {
			time.Sleep(50 * time.Millisecond)
			cb := q.Get("redirect_uri") + "?code=" + code + "&state=" + url.QueryEscape(q.Get("state"))
			resp, err := noRedirectClient.Get(cb)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestSignInBrowserFlowStoresTokens(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.AcceptAnyVerifier = true
	env, _ := testEnv(t, browserThatConsents(s, "c1"))
	store := newMemStore()
	ti, err := signIn(context.Background(), env, store, "p", Profile{URL: s.URL}, authOptions{})
	if err != nil || ti.AccessToken == "" || ti.RefreshToken == "" || ti.ExpiresAt.IsZero() {
		t.Fatalf("%+v %v", ti, err)
	}
	saved, _ := store.Load("p")
	if saved.RefreshToken != ti.RefreshToken || saved.Legacy() {
		t.Fatalf("saved %+v", saved)
	}
}

func TestSignInFallsBackToDeviceWhenTheBrowserFails(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, out := testEnv(t, func(string) error { return errors.New("no browser") })
	ti, err := signIn(context.Background(), env, newMemStore(), "p", Profile{URL: s.URL}, authOptions{})
	if err != nil || ti.AccessToken == "" {
		t.Fatalf("%+v %v", ti, err)
	}
	if !strings.Contains(out.String(), "BCDF-GHJK") || !strings.Contains(out.String(), s.URL+"/oauth/device") {
		t.Fatalf("device instructions missing:\n%s", out.String())
	}
}

func TestUseDeviceFlow(t *testing.T) {
	env, _ := testEnv(t, nil)
	if useDeviceFlow(env, authOptions{}) {
		t.Fatal("desktop default is the browser")
	}
	if !useDeviceFlow(env, authOptions{Device: true}) {
		t.Fatal("--device")
	}
	env.Getenv = func(k string) string {
		if k == "SSH_CONNECTION" {
			return "1.2.3.4 22 5.6.7.8 22"
		}
		return ""
	}
	if !useDeviceFlow(env, authOptions{}) {
		t.Fatal("SSH session")
	}
	env.Getenv = func(string) string { return "" }
	env.GOOS = "linux"
	if !useDeviceFlow(env, authOptions{}) {
		t.Fatal("Linux without DISPLAY/WAYLAND_DISPLAY")
	}
}

func TestSignInWithoutOAuthServer(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.NoOAuth = true
	env, _ := testEnv(t, nil)
	_, err := signIn(context.Background(), env, newMemStore(), "p", Profile{URL: s.URL}, authOptions{})
	if err == nil || !strings.Contains(err.Error(), "does not support OAuth sign-in") {
		t.Fatalf("got %v", err)
	}
}

func TestSignOutRevokesAndDeletes(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, nil)
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(time.Hour)})
	if err := signOut(context.Background(), env, store, "p", Profile{URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	if len(s.Revoked) != 1 || s.Revoked[0] != "r0" {
		t.Fatalf("revoked %v", s.Revoked)
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("token must be deleted")
	}
}

func TestLegacyTokenIsDiscarded(t *testing.T) {
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r", Sequence: "3"})
	ti, ok := usableToken(store, "p")
	if ok || ti.AccessToken != "" {
		t.Fatal("a legacy token is never used")
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("a legacy token is deleted")
	}
}
```

Add `AcceptAnyVerifier bool` to `oauthtest.Server`. When it is set, the `authorization_code` branch accepts a registered code with any non-empty `code_verifier`: `ok && (s.AcceptAnyVerifier ? r.Form.Get("code_verifier") != "" : verifier == r.Form.Get("code_verifier"))`. The browser-flow test uses it, because the verifier is generated inside `signIn`. Also add a `noRedirectClient` test var. `usableToken(store, profile) (tokenstore.TokenInfo, bool)` loads the token, deletes it and returns false if it is `Legacy()`, and returns it otherwise. It belongs to this task's production code.

- [ ] **Step 2: Run them to make sure they fail.**

Run: `go test -run 'SignIn|SignOut|UseDeviceFlow|LegacyToken' .`
Expected: FAIL.

- [ ] **Step 3: Implement `auth.go`.**
  - `defaultAuthEnv()`: `&http.Client{Timeout: 30 * time.Second}`, `browser.OpenURL`, `os.Getenv`, `runtime.GOOS`, `os.Hostname`, `os.Stderr`, `oauth.Sleep`, and a 5-minute timeout.
  - `useDeviceFlow`: true when any of these holds:
    - `opts.Device`;
    - `SSH_CONNECTION` is non-empty;
    - `GOOS == "linux"` and both `DISPLAY` and `WAYLAND_DISPLAY` are empty.
  - `signIn`:
    1. `m, err := metadataFor(...)`. On `oauth.ErrNotSupported`, return it with the profile URL.
    2. Scope = `opts.Scope`, else `prof.EffectiveScope()`. Hostname comes from `env.Hostname` (fall back to `"unknown"`).
    3. If `!useDeviceFlow`:
       - Run the browser flow: `ListenLoopback`, then `NewPKCE`, `RandomState`, `AuthorizeURL` (with `opts.Prompt`).
       - Print `Opening your browser to sign in: <url>` to `env.Out`, then call `env.Open(url)`.
       - If `Open` fails, close the loopback, print `Could not open a browser, using the device code instead.` and continue with the device flow.
       - Otherwise call `l.Wait(ctx, state, m.ResultURI, env.Timeout)`, then `oauth.ExchangeCode(..., l.RedirectURI)`.
    4. Device flow:
       - Call `oauth.StartDevice`.
       - Print `To sign in, open <verification_uri> and enter the code <user_code>`, then the QR code of `verification_uri_complete` (`qrterminal.GenerateWithConfig`, half-blocks, writer `env.Out`).
       - Then call `oauth.PollDevice(..., env.Sleep)`.
    5. Build `TokenInfo{AccessToken, RefreshToken, ExpiresAt: tokens.ExpiresAt(time.Now()), Scope: tokens.Scope}`, call `store.Save` (return its error) and return it.
  - `signOut`:
    - Load the token. If it has a refresh token and metadata can be obtained, call `oauth.Revoke`. A revoke error is printed as a warning and does not stop the logout.
    - Then `store.Delete(profile)` (an already-missing token is not an error), and remove the `<profile>.oauth.json` cache.
  - Wiring in `main.go`:
    - `auth` gets flags `--scope`, `--device` and `--prompt` (`login`, `consent` or `none`). The `--device` flow does not require a TTY: the user only needs to read the code. The browser flow keeps the TTY requirement. `auth` deletes the existing token, then calls `signIn`.
    - New `logout` command, which calls `signOut`.
    - `authenticate()` (startup.go):
      - `usableToken` → done.
      - Otherwise, with a TTY or when `useDeviceFlow`, call `signIn`.
      - Otherwise fail with `profile <p> needs authentication; run <bin> auth`.
      - When the legacy token was discarded, first print `Your saved sign-in is from an older version; signing in again.`
    - Remove `acquireToken`, `extractToken`, `exchangeToken`, `callbackPort` and their tests.
  - Add the dependency: `go get github.com/mdp/qrterminal/v3@latest`, then `go mod tidy`.

- [ ] **Step 4: Run the tests.**

Run: `go test -race ./... && go vet ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add auth.go auth_test.go main.go startup.go main_test.go testutil_test.go oauth/oauthtest go.mod go.sum
git commit -m "feat: OAuth sign-in with browser and device flows, and logout"
```

---

### Task 6: Refresh with rotation in `webdaclient`

**Files:**
- Modify: `webdaclient/client.go`
- Test: `webdaclient/client_test.go`

**Interfaces:**
- Consumes: Task 2 `oauth.Refresh`, `oauth.Error`, `Metadata`; Task 4 `TokenInfo`.
- Produces:

```go
type Client struct { BaseURL, Name string /* + unexported */ }
// New loads the stored token; refreshing uses the metadata's token endpoint and clientID.
func New(name, baseURL string, store tokenstore.TokenStore, meta *oauth.Metadata, clientID string, hc *http.Client) (*Client, error)
// Do attaches the bearer (refreshing first when it expires within 60 s), and on a 401 refreshes once and retries.
func (c *Client) Do(req *http.Request) (*http.Response, error)
func (c *Client) Request(method, path string, body io.Reader) (*http.Response, error)
// ErrSignedOut is returned when the refresh token is refused: the stored token is deleted, the user must sign in.
var ErrSignedOut = errors.New("signed out: run auth to sign in again")
```

`Close()` and the background loop are removed. Update callers. Refresh is single-flight: concurrent callers wait for the same refresh. `Do` only retries when the request body can be replayed (`req.GetBody != nil` or no body).

- [ ] **Step 1: Write the failing tests** against `oauthtest.Server`, plus a small API handler on the same server that returns 401 for any bearer other than the current access token:

```go
func TestRotatedRefreshTokenIsSavedBeforeUse(t *testing.T) { /* stored r0 expiring now → Do → token endpoint called once → store holds r1 (not r0) and a1; the request carried a1 */ }
func TestConcurrent401sShareOneRefresh(t *testing.T) { /* 10 goroutines Do with an expired-at-server access token → s.TokenCalls == 1 → all succeed */ }
func TestKeepsRefreshTokenWhenNoneIsReturned(t *testing.T) { /* fake returns no refresh_token → store keeps r0 */ }
func TestRefusedRefreshSignsOut(t *testing.T) { /* refresh token unknown → Do returns ErrSignedOut and the store no longer has the profile */ }
func TestTransientRefreshErrorKeepsTheToken(t *testing.T) { /* token endpoint 503 → Do returns an error, store unchanged */ }
func TestNoRetryForUnreplayableBody(t *testing.T) { /* 401 with req.Body set and GetBody nil → returns the 401 response, no refresh */ }
```

Write each test body fully. For the first test: store `TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()}`, create the client, `Do(GET /api/x)`, then assert `s.TokenCalls == 1`, `store.Load("p").RefreshToken == "r1"` and that the API saw `Bearer a1`. For the concurrent test, the API handler accepts only the newest access token, and every stored token starts expired at the server (`ExpiresAt` far in the future, so only the 401 path triggers the refresh). Use a `sync.WaitGroup` over 10 goroutines. Add `TokenCalls` reads with `atomic.LoadInt32`. Add an `/api/x` handler to `oauthtest.Server` through an exported `Handle(pattern, handler)` method that registers on its mux.

- [ ] **Step 2: Run them to make sure they fail.**

Run: `go test ./webdaclient/...`
Expected: FAIL.

- [ ] **Step 3: Implement.**
  - **State:** keep the token in memory, guarded by a mutex.
  - **`accessToken(ctx)`:** if `ExpiresAt` is non-zero and within 60 s, call `refresh(ctx, "")`.
  - **`refresh(ctx, failed string)`:**
    - Single-flight (an `inflight *call` with a `done` channel).
    - If another caller has already rotated (the current access token is not `failed` and has not expired), return it.
    - Otherwise call `oauth.Refresh`.
    - On success, **save first**: build `TokenInfo{AccessToken, RefreshToken: new or kept old, ExpiresAt, Scope}` and call `store.Save`. If the save fails, keep the new tokens in memory, write a warning to stderr, and carry on.
    - On `*oauth.Error` with status 400 or 401 and `Code` in {`invalid_grant`, `invalid_client`, `unauthorized_client`}: `store.Delete(name)` and return `ErrSignedOut`. Any other error is returned and the token kept.
  - **`Do`:**
    - Attach `Authorization: Bearer <token>` unless the caller set one.
    - Send through `c.hc`.
    - On a 401: if the body is not replayable, return the response. Otherwise close it, call `refresh(ctx, sentToken)`, clone the request with `GetBody` and retry once.
  - Remove `exchangeTokenWithTTL`, `Sequence`, `backgroundLoop`, `stopCh`, `refreshedCh` and `Close`.

- [ ] **Step 4: Update the callers.**
  - `main.go`'s `webdaclient.New(...)` call gets the metadata from `metadataFor` and `prof.EffectiveClientID()`. Callers stop calling `Close`.
  - Any `ErrSignedOut` returned from an operation prints `Your sign-in expired or was revoked; run <bin> auth.` and exits non-zero.

- [ ] **Step 5: Run the tests.**

Run: `go test -race ./... && go vet ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add webdaclient main.go startup.go oauth/oauthtest
git commit -m "feat(webdaclient): OAuth refresh with single-flight rotation"
```

---

### Task 7: Operations from `webda_operations_uri`, OpenAPI fallback, scope hint

**Files:**
- Modify: `main.go` (`fetchOperations` ~246, `parseOperations` ~271-365, `Operation` ~40-50), `execution.go` (`executeOperation` ~44-90, error hints), `cmdtree.go` (description uses `summary`)
- Test: `main_test.go`, `execution_test.go`, `cmdtree_test.go`

**Interfaces:**
- Consumes: Task 2 `Metadata.OperationsURI`; Task 6 `Client`.
- Produces:
  - `Operation` gains `Summary string` and `Source string` (`"operations"` or `"openapi"`). For OpenAPI operations, `Method` and `Path` hold the HTTP method and path template (for example `/tasks/{uuid}`), and `Input` is built from the parameters and the JSON request body schema.
  - `fetchOperations(ctx, profile, meta)`:
    - GETs `meta.OperationsURI`, or `<url>/operations` when that is empty.
    - On a 404 it GETs `<url>/` with `Accept: application/json` and parses an OpenAPI document (`openapi` key plus a `paths` map).
    - Writes the cache as before. The cache stores the raw document, and parsing detects the shape.
  - `executeOperation` dispatches on `op.Source`:
    - `"operations"`: `POST <base>/operations/<id>` with a JSON body (unchanged).
    - `"openapi"`:
      - Fill `{name}` path parameters from the input. A missing required path parameter is an error.
      - For `GET`/`DELETE`/`HEAD`, put the remaining input in the query string (scalars as strings, arrays repeated).
      - For other methods, send a JSON body.
  - On a `403` whose JSON body has `"error": "insufficient_scope"`, print `this token does not cover <operation>; run <bin> auth --scope "<operation> offline_access"` (`<bin>` = `filepath.Base(os.Args[0])`). Other 403s keep the existing hint.

- [ ] **Step 1: Write the failing tests.**
  - `TestParseOperationsSummaryAndDestructive`: `{operations: {"Task.Get": {summary: "Read a task", destructive: true}}}` gives `Summary` "Read a task", `Destructive` true and `Source` "operations".
  - `TestParseOpenAPI`: a document with these paths:
    - `/tasks/{uuid}` get, `operationId: Task.Get`, path param `uuid` required;
    - `/tasks` post, `operationId: Task.Create`, JSON requestBody `{title: string}`;
    - `/tasks` get with query param `q`.

    It yields three operations with `Source` "openapi", methods and paths filled, and an `Input` schema whose properties are `uuid` (required), `title` and `q`. An operation without an `operationId` uses the existing `deriveOpName(method, path)`.
  - `TestExecuteOpenAPIGet`: an httptest server asserts it receives `GET /tasks/abc?q=x` for input `{uuid: "abc", q: "x"}`.
  - `TestExecuteOpenAPIPost`: the server asserts `POST /tasks` with body `{"title":"t"}`.
  - `TestFetchOperationsFallsBackToOpenAPI`: `/operations` answers 404 and `/` serves the OpenAPI JSON, so the operations come back from OpenAPI.
  - `TestFetchOperationsUsesMetadataURI`: when `OperationsURI` points to `/custom/ops`, that URL is used.
  - `TestInsufficientScopeHint`: the server answers 403 with `{"error":"insufficient_scope","operation":"Task.Delete"}`. The error message contains `this token does not cover Task.Delete` and `auth --scope`.

  Write each test fully, following the existing `execution_test.go` style with `httptest.NewServer` and a `webdaclient` built over an in-memory store holding a valid token.

- [ ] **Step 2: Run them to make sure they fail.**

Run: `go test -run 'ParseOperations|ParseOpenAPI|ExecuteOpenAPI|FetchOperations|InsufficientScope' .`
Expected: FAIL.

- [ ] **Step 3: Implement.**
  - Extend `parseOperations` for the OpenAPI shape:
    - Iterate `paths`, then the methods `get post put patch delete`.
    - Name = `operationId`, or `deriveOpName`. Description = `description`, falling back to `summary`.
    - Input properties come from `parameters` (in `path` or `query`, schema or a string default; `required` is propagated) merged with `requestBody.content["application/json"].schema.properties`. Resolve local `$ref`s under `#/components/schemas/` one level deep, and leave unresolved refs as string flags.
  - Sort the operations by name, so the output does not depend on map iteration order.
  - In the `operations` shape, read `summary` and `destructive` into the struct.
  - `cmdtree.go` uses `Summary` as `Short` when it is present, otherwise the first line of `Description`.

- [ ] **Step 4: Run the tests.**

Run: `go test -race ./... && go vet ./... && golangci-lint run`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add main.go execution.go cmdtree.go *_test.go
git commit -m "feat: operations from the OAuth metadata, OpenAPI fallback and scope hint"
```

---

### Task 8: README and full verification

**Files:**
- Modify: `README.md` ("Authentication", "Configuration" profile format, "Files" table)

- [ ] **Step 1: Rewrite the README sections.**
  - **Authentication:**
    - On first use, or with `<bin> auth`, the CLI opens the browser to sign in and asks you to choose which operations it may call.
    - Options: `--scope`, `--device` (and when the device flow is used automatically), `--prompt consent`.
    - `<bin> logout` revokes the sign-in.
    - Tokens are refreshed automatically.
    - Connected apps can be revoked in the web app.
    - When a token does not cover an operation, the CLI tells you how to request it.
  - **Configuration:** show both profile forms:

    ```yaml
    myapp: https://app.example.com
    lc:
      url: https://my.loopingz.com
      client_id: webda-cli
      scope: "* offline_access"
    update: prompt
    ```

    Also document `setup --client-id --scope`.
  - **Storage:** tokens go to the OS keychain (service `webda-cli`, one entry per profile). Without a keychain they go to an encrypted file under `~/.webdacli/<profile>.tok`, keyed with the SSH agent or the machine.
  - **Files table:** add `<profile>.oauth.json` (cached server metadata) and correct the `.tok` row.
  - **Server requirements:** OAuth sign-in needs a Webda server with `@webda/oauth` (`OAuthServer`). Operations come from `/operations` (`Webda/OperationsTransport`), or from the REST OpenAPI document when `exposeOpenAPI` is enabled.

- [ ] **Step 2: Run the full verification.**

Run: `go test -race -count=1 ./... && go vet ./... && golangci-lint run && go build ./...`
Expected: all PASS.

Then run a manual check against a dev Webda server with `@webda/oauth` (for example the webda.io `sample-app` with `OAuthServer`, `OperationsTransport` and a login), recording the result in the commit body:
1. `setup --name dev --url http://localhost:18080`
2. `dev auth`: the browser opens, consent, then the CLI prints success.
3. `dev whoami`.
4. Run any operation.
5. `dev auth --device`: the code and QR code are shown, and approving on the web finishes the sign-in.
6. `dev logout`. The next command asks to sign in again.

- [ ] **Step 3: Commit.**

```bash
git add README.md
git commit -m "docs: OAuth sign-in, profiles and token storage"
```

---

## Rollout notes (not tasks)

- **Server support:** the CLI only works against servers that run `@webda/oauth`. Against an older server, `auth` stops with `this server does not support OAuth sign-in`. No legacy fallback is kept, per spec §4: the legacy endpoints are removed in loopcenter rollout step 4.
- **Existing users:** saved sign-ins (with `sequence`) are discarded once, and the user signs in again.
- **Release:** release-please picks up the `feat:` commits, which means a minor release.
