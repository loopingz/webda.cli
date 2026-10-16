package webdaclient

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/tokenstore"
)

// redirectFixture serves https://api.test (port 443) and every other port with TLS, and port 80 in clear text;
// /start?to=<url> redirects, /echo answers the Authorization header it received.
func redirectFixture(t *testing.T) *http.Client {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, r.Header.Get("Authorization"))
	})
	tlsSrv := httptest.NewTLSServer(handler)
	plain := httptest.NewServer(handler)
	t.Cleanup(tlsSrv.Close)
	t.Cleanup(plain.Close)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test servers only
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			if port == "80" {
				return dialer.DialContext(ctx, network, plain.Listener.Addr().String())
			}
			return dialer.DialContext(ctx, network, tlsSrv.Listener.Addr().String())
		},
	}}
}

func TestRedirectsOffOriginDropTheBearer(t *testing.T) {
	hc := redirectFixture(t)
	store := tokenstore.NewMachineStore(t.TempDir())
	if err := store.Save("p", tokenstore.TokenInfo{AccessToken: "secret", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	c, err := New("p", "https://api.test", store, nil, "webda-cli", hc)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"https://api.test/echo":      "Bearer secret", // same origin keeps it
		"http://api.test/echo":       "",              // https -> http
		"https://api.test:8443/echo": "",              // other port
		"https://sub.api.test/echo":  "",              // subdomain
		"https://other.test/echo":    "",              // other host
	}
	for to, want := range cases {
		resp, err := c.Request(http.MethodGet, "/start?to="+url.QueryEscape(to), nil)
		if err != nil {
			t.Fatalf("%s: %v", to, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != want {
			t.Errorf("redirect to %s received Authorization %q, want %q", to, body, want)
		}
	}
	// the caller's client is not modified
	if hc.CheckRedirect != nil {
		t.Fatal("the provided http.Client must not be changed")
	}
}

// Two CLI processes (two Clients over the same store and lock file) with the same expired token
// spend the rotating refresh token once.
func TestTwoClientsShareOneRefreshThroughTheStore(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	lock := filepath.Join(t.TempDir(), "p.lock")
	a, b := f.client(t), f.client(t)
	a.LockFile, b.LockFile = lock, lock
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, c := range []*Client{a, b} {
		wg.Add(1)
		go func(c *Client) {
			defer wg.Done()
			resp, err := f.get(t, c)
			if err == nil && resp.StatusCode != http.StatusOK {
				err = &statusError{resp.StatusCode}
			}
			errs <- err
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := f.tokenCalls(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
}

// Without the lock, a client whose refresh token was already rotated by another process adopts the stored token.
func TestRefreshAdoptsATokenRotatedByAnotherProcess(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	a, b := f.client(t), f.client(t)
	for _, c := range []*Client{a, b} {
		resp, err := f.get(t, c)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%v %v", resp, err)
		}
	}
	if n := f.tokenCalls(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
	if b.Scope() != "*" {
		t.Fatalf("adopted scope %q", b.Scope())
	}
}

func TestRefreshedTokenRecordsTheServer(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	if _, err := f.get(t, f.client(t)); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.store.Load("p")
	u, _ := url.Parse(f.s.URL)
	if stored.Server != "http://"+u.Host {
		t.Fatalf("stored server %q", stored.Server)
	}
}

func TestTokenOfAnotherServerIsNeverUsed(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0", Server: "https://old.example.com:443"})
	if _, err := f.get(t, f.client(t)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) != 1 || f.seen[0] != "" {
		t.Fatalf("a token of another server was sent: %v", f.seen)
	}
}

type statusError struct{ code int }

func (e *statusError) Error() string { return http.StatusText(e.code) }
