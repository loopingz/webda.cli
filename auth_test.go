package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/oauth/oauthtest"
	"github.com/loopingz/webda.cli/tokenstore"
)

// noRedirectClient lets the simulated browser see the loopback 302 instead of following it.
var noRedirectClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// memStore is an in-memory tokenstore.TokenStore.
type memStore struct {
	mu     sync.Mutex
	tokens map[string]tokenstore.TokenInfo
}

func newMemStore() *memStore { return &memStore{tokens: map[string]tokenstore.TokenInfo{}} }

func (m *memStore) Load(name string) (tokenstore.TokenInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ti, ok := m.tokens[name]
	if !ok {
		return tokenstore.TokenInfo{}, os.ErrNotExist
	}
	return ti, nil
}

func (m *memStore) Save(name string, ti tokenstore.TokenInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[name] = ti
	return nil
}

func (m *memStore) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tokens[name]; !ok {
		return os.ErrNotExist
	}
	delete(m.tokens, name)
	return nil
}

func testEnv(t *testing.T, open func(string) error) (authEnv, *bytes.Buffer) {
	out := &bytes.Buffer{}
	t.Setenv("HOME", t.TempDir())
	return authEnv{
		HTTP: http.DefaultClient, Open: open, Getenv: func(string) string { return "" }, GOOS: "darwin",
		Hostname: func() (string, error) { return "box", nil }, Out: out,
		Sleep: func(context.Context, time.Duration) error { return nil }, Timeout: 5 * time.Second,
	}, out
}

// browserThatConsents simulates a remembered consent: inside Open, it registers the code and calls back
// synchronously, before Open returns.
func browserThatConsents(s *oauthtest.Server, code string) func(string) error {
	return func(authorize string) error {
		u, _ := url.Parse(authorize)
		q := u.Query()
		// the server "issues" this code; with AcceptAnyVerifier the fake accepts any non-empty code_verifier for it
		s.Codes[code] = "any"
		cb := q.Get("redirect_uri") + "?code=" + code + "&state=" + url.QueryEscape(q.Get("state"))
		resp, err := noRedirectClient.Get(cb)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusOK {
			return errors.New("callback refused: " + resp.Status)
		}
		return nil
	}
}

func TestSignInBrowserFlowStoresTokens(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.AcceptAnyVerifier = true
	var authorize string
	consent := browserThatConsents(s, "c1")
	env, out := testEnv(t, func(u string) error { authorize = u; return consent(u) })
	store := newMemStore()
	ti, err := signIn(context.Background(), env, store, "p", Profile{URL: s.URL}, authOptions{Prompt: "consent"})
	if err != nil || ti.AccessToken == "" || ti.RefreshToken == "" || ti.ExpiresAt.IsZero() {
		t.Fatalf("%+v %v", ti, err)
	}
	saved, _ := store.Load("p")
	if saved.RefreshToken != ti.RefreshToken || saved.Legacy() || saved.Server != oauthOrigin(s.URL) {
		t.Fatalf("saved %+v", saved)
	}
	q, _ := url.Parse(authorize)
	params := q.Query()
	if params.Get("client_id") != defaultClientID || params.Get("scope") != defaultScope || params.Get("prompt") != "consent" ||
		params.Get("hostname") != "box" || params.Get("code_challenge_method") != "S256" ||
		!strings.HasPrefix(params.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Fatalf("authorize URL %s", authorize)
	}
	if !strings.Contains(out.String(), "Sign in at: "+authorize) {
		t.Fatalf("output:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(configDir(), "p.oauth.json")); err != nil {
		t.Fatalf("metadata cache: %v", err)
	}
}

func oauthOrigin(u string) string { return oauth.Origin(u) }

// A launcher that blocks until the browser exits (pkg/browser on Linux) while the remembered consent calls back
// immediately: the sign-in completes without waiting for Open to return.
func TestSignInCallbackDuringABlockingOpen(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.AcceptAnyVerifier = true
	consent := browserThatConsents(s, "c-block")
	release := make(chan struct{})
	defer close(release)
	env, _ := testEnv(t, func(u string) error {
		if err := consent(u); err != nil {
			return err
		}
		<-release // the browser process is still running
		return nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := signIn(context.Background(), env, newMemStore(), "p", Profile{URL: s.URL}, authOptions{})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in waited for the browser launcher to exit")
	}
}

func TestSignInUsesTheRequestedScopeAndClient(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.AcceptAnyVerifier = true
	var authorize string
	consent := browserThatConsents(s, "c2")
	env, _ := testEnv(t, func(u string) error { authorize = u; return consent(u) })
	prof := Profile{URL: s.URL, ClientID: "my-cli", Scope: "Task.read offline_access"}
	if _, err := signIn(context.Background(), env, newMemStore(), "p", prof, authOptions{Scope: "Task.* offline_access"}); err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(authorize)
	if q.Query().Get("client_id") != "my-cli" || q.Query().Get("scope") != "Task.* offline_access" {
		t.Fatalf("authorize URL %s", authorize)
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
	if !strings.Contains(out.String(), "Could not open a browser, using the device code instead.") ||
		!strings.Contains(out.String(), "To sign in, open "+s.URL+"/oauth/device and enter the code BCDF-GHJK") {
		t.Fatalf("output:\n%s", out.String())
	}
	if !strings.ContainsAny(out.String(), "▀▄█") {
		t.Fatalf("QR code missing:\n%s", out.String())
	}
}

func TestSignInDeviceFlowNeverOpensTheBrowser(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, func(string) error { t.Fatal("browser opened"); return nil })
	if _, err := signIn(context.Background(), env, newMemStore(), "p", Profile{URL: s.URL}, authOptions{Device: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSignInDeviceDenied(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.DeviceAnswers = []string{"authorization_pending", "access_denied"}
	env, _ := testEnv(t, nil)
	store := newMemStore()
	_, err := signIn(context.Background(), env, store, "p", Profile{URL: s.URL}, authOptions{Device: true})
	if err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("got %v", err)
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("nothing is stored on failure")
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
	env.Getenv = func(k string) string {
		if k == "WAYLAND_DISPLAY" {
			return "wayland-0"
		}
		return ""
	}
	if useDeviceFlow(env, authOptions{}) {
		t.Fatal("Linux with a Wayland display uses the browser")
	}
}

func TestSignInWithoutOAuthServer(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.NoOAuth = true
	env, _ := testEnv(t, nil)
	_, err := signIn(context.Background(), env, newMemStore(), "p", Profile{URL: s.URL}, authOptions{})
	if err == nil || !strings.Contains(err.Error(), "does not support OAuth sign-in") || !strings.Contains(err.Error(), s.URL) {
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
	if _, err := os.Stat(filepath.Join(configDir(), "p.oauth.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata cache must be removed: %v", err)
	}
}

func TestSignOutWithoutRevocationEndpoint(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.NoRevocation = true
	env, out := testEnv(t, nil)
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0"})
	if err := signOut(context.Background(), env, store, "p", Profile{URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	if len(s.Revoked) != 0 || strings.Contains(strings.ToLower(out.String()), "warning") {
		t.Fatalf("revoked %v, output %q", s.Revoked, out.String())
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("token must be deleted")
	}
}

func TestSignOutWarnsWhenTheServerIsUnreachable(t *testing.T) {
	s := oauthtest.New()
	base := s.URL
	s.Close()
	env, out := testEnv(t, nil)
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0"})
	if err := signOut(context.Background(), env, store, "p", Profile{URL: base}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Warning") {
		t.Fatalf("output %q", out.String())
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("token must be deleted")
	}
}

func TestSignOutWithoutTokenAndWithLegacyToken(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, nil)
	store := newMemStore()
	if err := signOut(context.Background(), env, store, "p", Profile{URL: s.URL}); err != nil {
		t.Fatalf("missing token is not an error: %v", err)
	}
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "legacy", Sequence: "3"})
	if err := signOut(context.Background(), env, store, "p", Profile{URL: s.URL}); err != nil {
		t.Fatal(err)
	}
	if len(s.Revoked) != 0 {
		t.Fatalf("a legacy token is never sent to the OAuth server: %v", s.Revoked)
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("token must be deleted")
	}
}

func TestLegacyTokenIsDiscarded(t *testing.T) {
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r", Sequence: "3"})
	ti, ok, legacy := loadToken(store, "p", Profile{URL: "https://h"})
	if ok || !legacy || ti.AccessToken != "" {
		t.Fatal("a legacy token is never used")
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("a legacy token is deleted")
	}
}

func TestLoadToken(t *testing.T) {
	store := newMemStore()
	prof := Profile{URL: "https://h.example.com/api"}
	if _, ok, _ := loadToken(store, "p", prof); ok {
		t.Fatal("no token")
	}
	// a token saved before the server field existed is accepted
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r"})
	if ti, ok, _ := loadToken(store, "p", prof); !ok || ti.RefreshToken != "r" {
		t.Fatalf("%+v %v", ti, ok)
	}
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r", Server: "https://h.example.com:443"})
	if _, ok, _ := loadToken(store, "p", prof); !ok {
		t.Fatal("a token of the profile server is used")
	}
}

func TestLoadTokenDiscardsATokenOfAnotherServer(t *testing.T) {
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r", Server: "https://old.example.com:443"})
	ti, ok, legacy := loadToken(store, "p", Profile{URL: "https://new.example.com"})
	if ok || legacy || ti.AccessToken != "" {
		t.Fatalf("a token of another server is never used: %+v %v %v", ti, ok, legacy)
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("a token of another server is deleted")
	}
}

func TestAuthenticateKeepsAnExistingToken(t *testing.T) {
	env, _ := testEnv(t, func(string) error { t.Fatal("no sign-in expected"); return nil })
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "existing-access", RefreshToken: "r"})
	if err := authenticate(context.Background(), env, store, "bin", "p", Profile{URL: "https://unused.example.com"}, false); err != nil {
		t.Fatal(err)
	}
	if ti, _ := store.Load("p"); ti.AccessToken != "existing-access" {
		t.Fatalf("%+v", ti)
	}
}

func TestAuthenticateWithoutTerminalAsksToRunAuth(t *testing.T) {
	env, _ := testEnv(t, nil)
	err := authenticate(context.Background(), env, newMemStore(), "bin", "p", Profile{URL: "https://unused.example.com"}, false)
	if err == nil || err.Error() != "profile p needs authentication; run bin auth" {
		t.Fatalf("got %v", err)
	}
}

func TestAuthenticateLegacyTokenWithoutTerminal(t *testing.T) {
	env, out := testEnv(t, nil)
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r", Sequence: "3"})
	err := authenticate(context.Background(), env, store, "bin", "p", Profile{URL: "https://unused.example.com"}, false)
	if err == nil || !strings.Contains(err.Error(), "run bin auth") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(out.String(), "older version") {
		t.Fatalf("output %q", out.String())
	}
	if _, err := store.Load("p"); err == nil {
		t.Fatal("a legacy token is deleted")
	}
	// second run: no loop, same clear error
	if err := authenticate(context.Background(), env, store, "bin", "p", Profile{URL: "https://unused.example.com"}, false); err == nil {
		t.Fatal("still needs authentication")
	}
}

func TestAuthenticateLegacyTokenSignsInAgainWithTerminal(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, out := testEnv(t, func(string) error { return errors.New("no browser") })
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "legacy-a", RefreshToken: "legacy-r", Sequence: "3"})
	if err := authenticate(context.Background(), env, store, "bin", "p", Profile{URL: s.URL}, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Your saved sign-in is from an older version; signing in again.") {
		t.Fatalf("output:\n%s", out.String())
	}
	ti, err := store.Load("p")
	if err != nil || ti.Legacy() || ti.RefreshToken == "legacy-r" || ti.RefreshToken == "" {
		t.Fatalf("%+v %v", ti, err)
	}
	// only the device poll reached /oauth/token: the legacy refresh token was never sent
	if got := atomic.LoadInt32(&s.TokenCalls); got != 1 {
		t.Fatalf("token calls %d", got)
	}
}

func TestAuthenticateWithoutTerminalFailsFastEvenForTheDeviceFlow(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, func(string) error { t.Fatal("browser opened"); return nil })
	env.Getenv = func(k string) string {
		if k == "SSH_CONNECTION" {
			return "1.2.3.4 22 5.6.7.8 22"
		}
		return ""
	}
	err := authenticate(context.Background(), env, newMemStore(), "bin", "p", Profile{URL: s.URL}, false)
	if err == nil || err.Error() != "profile p needs authentication; run bin auth" {
		t.Fatalf("got %v", err)
	}
	if got := atomic.LoadInt32(&s.TokenCalls); got != 0 {
		t.Fatalf("no sign-in expected, token calls %d", got)
	}
}

func TestAuthCommandKeepsThePreviousTokenOnFailure(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.DeviceAnswers = []string{"access_denied"}
	env, _ := testEnv(t, nil)
	orig := tokenStore
	store := newMemStore()
	tokenStore = store
	defer func() { tokenStore = orig }()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0"})
	cmd := newAuthCmd(env, "p", Profile{URL: s.URL})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--device"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("got %v", err)
	}
	if ti, err := store.Load("p"); err != nil || ti.RefreshToken != "r0" {
		t.Fatalf("previous token lost: %+v %v", ti, err)
	}
	if len(s.Revoked) != 0 {
		t.Fatalf("revoked %v", s.Revoked)
	}
}

func TestAuthCommandRevokesThePreviousTokenOnSuccess(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, nil)
	orig := tokenStore
	store := newMemStore()
	tokenStore = store
	defer func() { tokenStore = orig }()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0"})
	cmd := newAuthCmd(env, "p", Profile{URL: s.URL})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--device"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	ti, _ := store.Load("p")
	if ti.RefreshToken == "r0" || ti.RefreshToken == "" {
		t.Fatalf("%+v", ti)
	}
	if len(s.Revoked) != 1 || s.Revoked[0] != "r0" {
		t.Fatalf("revoked %v", s.Revoked)
	}
}

func TestAuthCommandWithoutRevocationEndpoint(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	s.NoRevocation = true
	env, out := testEnv(t, nil)
	orig := tokenStore
	store := newMemStore()
	tokenStore = store
	defer func() { tokenStore = orig }()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0"})
	cmd := newAuthCmd(env, "p", Profile{URL: s.URL})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--device"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(s.Revoked) != 0 || strings.Contains(out.String(), "Warning") {
		t.Fatalf("revoked %v output %q", s.Revoked, out.String())
	}
}

func TestValidPrompt(t *testing.T) {
	for _, p := range []string{"", "login", "consent", "none"} {
		if err := validPrompt(p); err != nil {
			t.Fatalf("%q: %v", p, err)
		}
	}
	if err := validPrompt("select_account"); err == nil {
		t.Fatal("unsupported prompt accepted")
	}
}

func TestAuthCommand(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, nil)
	orig := tokenStore
	store := newMemStore()
	tokenStore = store
	defer func() { tokenStore = orig }()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "old-r", Sequence: "2"})
	run := func(args ...string) (string, error) {
		cmd := newAuthCmd(env, "p", Profile{URL: s.URL})
		errOut := &bytes.Buffer{}
		cmd.SetIn(&bytes.Buffer{})
		cmd.SetErr(errOut)
		cmd.SetOut(errOut)
		cmd.SetArgs(args)
		cmd.SetContext(context.Background())
		err := cmd.Execute()
		return errOut.String(), err
	}
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("browser flow needs a terminal: %v", err)
	}
	if _, err := run("--device", "--prompt", "bogus"); err == nil || !strings.Contains(err.Error(), "--prompt") {
		t.Fatalf("got %v", err)
	}
	out, err := run("--device", "--scope", "Task.* offline_access")
	if err != nil || !strings.Contains(out, "Signed in to p.") {
		t.Fatalf("%q %v", out, err)
	}
	ti, _ := store.Load("p")
	if ti.Legacy() || ti.RefreshToken == "old-r" || ti.RefreshToken == "" {
		t.Fatalf("%+v", ti)
	}
	if len(s.Revoked) != 0 {
		t.Fatalf("a legacy token is never sent to the OAuth server: %v", s.Revoked)
	}
}

func TestLogoutCommand(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	env, _ := testEnv(t, nil)
	orig := tokenStore
	store := newMemStore()
	tokenStore = store
	defer func() { tokenStore = orig }()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0"})
	cmd := newLogoutCmd(env, "p", Profile{URL: s.URL})
	out := &bytes.Buffer{}
	cmd.SetErr(out)
	cmd.SetArgs(nil)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil || !strings.Contains(out.String(), "Signed out of p.") {
		t.Fatalf("%q %v", out.String(), err)
	}
	if len(s.Revoked) != 1 {
		t.Fatalf("revoked %v", s.Revoked)
	}
}
