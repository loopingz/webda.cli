package webdaclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/oauth/oauthtest"
	"github.com/loopingz/webda.cli/tokenstore"
)

// fixture is a fake OAuth server with a protected /api/x route that accepts only the newest access token (or accept).
type fixture struct {
	s     *oauthtest.Server
	meta  *oauth.Metadata
	store tokenstore.TokenStore
	mu    sync.Mutex
	seen  []string // Authorization headers received by /api/x
	saved []string // refresh token in the store when /api/x was called
	body  []string // request bodies received by /api/x
}

func newFixture(t *testing.T, accept string) *fixture {
	t.Helper()
	f := &fixture{s: oauthtest.New(), store: tokenstore.NewMachineStore(t.TempDir())}
	t.Cleanup(f.s.Close)
	f.meta = &oauth.Metadata{Issuer: f.s.URL, TokenEndpoint: f.s.URL + "/oauth/token"}
	f.s.Handle("/api/x", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		stored, _ := f.store.Load("p")
		f.mu.Lock()
		f.seen = append(f.seen, auth)
		f.saved = append(f.saved, stored.RefreshToken)
		f.body = append(f.body, string(data))
		f.mu.Unlock()
		current := f.s.CurrentAccessToken()
		if (current == "" || auth != "Bearer "+current) && (accept == "" || auth != "Bearer "+accept) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintln(w, `{"ok":true}`)
	}))
	return f
}

func (f *fixture) save(t *testing.T, ti tokenstore.TokenInfo) {
	t.Helper()
	if err := f.store.Save("p", ti); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) client(t *testing.T) *Client {
	t.Helper()
	c, err := New("p", f.s.URL, f.store, f.meta, "webda-cli", f.s.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) get(t *testing.T, c *Client) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.s.URL+"/api/x", nil)
	if err != nil {
		t.Error(err)
		return nil, err
	}
	resp, err := c.Do(req)
	if resp != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return resp, err
}

func (f *fixture) tokenCalls() int32 { return atomic.LoadInt32(&f.s.TokenCalls) }

func TestRotatedRefreshTokenIsSavedBeforeUse(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	resp, err := f.get(t, f.client(t))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if n := f.tokenCalls(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
	stored, err := f.store.Load("p")
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "r1" || stored.AccessToken != "a1" || !stored.ExpiresAt.After(time.Now()) {
		t.Fatalf("stored token not rotated: %+v", stored)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seen) != 1 || f.seen[0] != "Bearer a1" {
		t.Fatalf("API saw %v, want only Bearer a1", f.seen)
	}
	// the rotated refresh token was already stored when the new access token was used
	if f.saved[0] != "r1" {
		t.Fatalf("store held %q while the new access token was in use, want r1", f.saved[0])
	}
}

func TestConcurrent401sShareOneRefresh(t *testing.T) {
	f := newFixture(t, "")
	// the access token looks valid locally (far expiry) but the server refuses it: only the 401 path refreshes
	f.save(t, tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(time.Hour)})
	c := f.client(t)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := f.get(t, c)
			if err == nil && resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("status %d", resp.StatusCode)
			}
			errs <- err
		}()
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
	stored, _ := f.store.Load("p")
	if stored.RefreshToken != "r1" || stored.AccessToken != "a1" {
		t.Fatalf("stored token %+v, want r1/a1", stored)
	}
}

func TestKeepsRefreshTokenWhenNoneIsReturned(t *testing.T) {
	f := newFixture(t, "")
	f.s.KeepRefresh = true
	f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now(), Scope: "* offline_access"})
	resp, err := f.get(t, f.client(t))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	stored, _ := f.store.Load("p")
	if stored.RefreshToken != "r0" || stored.AccessToken != "a1" {
		t.Fatalf("stored token %+v, want refresh r0 kept with access a1", stored)
	}
	if stored.Scope != "*" {
		t.Fatalf("scope %q, want the granted scope", stored.Scope)
	}
}

func TestRefusedRefreshSignsOut(t *testing.T) {
	for name, expiresAt := range map[string]time.Time{"pre-expiry": time.Now(), "on 401": time.Now().Add(time.Hour)} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "")
			f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "unknown", ExpiresAt: expiresAt})
			c := f.client(t)
			_, err := f.get(t, c)
			if !errors.Is(err, ErrSignedOut) {
				t.Fatalf("want ErrSignedOut, got %v", err)
			}
			if _, err := f.store.Load("p"); err == nil {
				t.Fatal("the refused token is still stored")
			}
			if n := f.tokenCalls(); n != 1 {
				t.Fatalf("token endpoint called %d times, want 1", n)
			}
			// the refused refresh token is never presented again
			if _, err := f.get(t, c); err != nil {
				t.Fatalf("after sign-out a request goes out without a token, got %v", err)
			}
			if n := f.tokenCalls(); n != 1 {
				t.Fatalf("token endpoint called %d times after sign-out, want 1", n)
			}
		})
	}
}

func TestTransientRefreshErrorKeepsTheToken(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newFixture(t, "")
			f.s.TokenStatus = status
			ti := tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now().Add(-time.Minute).UTC().Round(time.Second), Scope: "*"}
			f.save(t, ti)
			_, err := f.get(t, f.client(t))
			if err == nil || errors.Is(err, ErrSignedOut) {
				t.Fatalf("want a refresh error that is not a sign-out, got %v", err)
			}
			stored, lerr := f.store.Load("p")
			if lerr != nil || stored.RefreshToken != ti.RefreshToken || stored.AccessToken != ti.AccessToken || !stored.ExpiresAt.Equal(ti.ExpiresAt) {
				t.Fatalf("store changed: %+v (%v)", stored, lerr)
			}
		})
	}
}

func TestTransientPreExpiryRefreshUsesTheCurrentToken(t *testing.T) {
	f := newFixture(t, "live")
	f.s.TokenStatus = http.StatusServiceUnavailable
	f.save(t, tokenstore.TokenInfo{AccessToken: "live", RefreshToken: "r0", ExpiresAt: time.Now().Add(30 * time.Second)})
	resp, err := f.get(t, f.client(t))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("request with the still-valid token failed: %v", err)
	}
	if n := f.tokenCalls(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
}

func TestUnknownExpiryDoesNotPreRefresh(t *testing.T) {
	f := newFixture(t, "live")
	f.save(t, tokenstore.TokenInfo{AccessToken: "live", RefreshToken: "r0"})
	resp, err := f.get(t, f.client(t))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("request failed: %v", err)
	}
	if n := f.tokenCalls(); n != 0 {
		t.Fatalf("token endpoint called %d times, want 0", n)
	}
}

func TestNoRetryForUnreplayableBody(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(time.Hour)})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.s.URL+"/api/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Body = io.NopCloser(strings.NewReader("payload"))
	req.ContentLength = int64(len("payload"))
	resp, err := f.client(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want the 401 response", resp.StatusCode)
	}
	if n := f.tokenCalls(); n != 0 {
		t.Fatalf("token endpoint called %d times, want 0", n)
	}
}

func TestRefreshReplaysJSONBody(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now().Add(time.Hour)})
	payload := `{"name":"alice","active":false}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.s.URL+"/api/x", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.client(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	if resp.StatusCode != 200 || len(f.body) != 2 || f.body[0] != payload || f.body[1] != payload {
		t.Fatalf("request not replayed intact: %d %#v", resp.StatusCode, f.body)
	}
	if f.seen[0] != "Bearer a0" || f.seen[1] != "Bearer a1" {
		t.Fatalf("bearers %v", f.seen)
	}
}

func TestBearerOnlySentToTheProfileOrigin(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "a0", RefreshToken: "r0", ExpiresAt: time.Now()})
	var got []string
	var mu sync.Mutex
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer other.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, other.URL+"/logo.png", nil)
	resp, err := f.client(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if resp.StatusCode != http.StatusUnauthorized || len(got) != 1 || got[0] != "" {
		t.Fatalf("another origin got %v (status %d)", got, resp.StatusCode)
	}
	if n := f.tokenCalls(); n != 0 {
		t.Fatalf("token endpoint called %d times for another origin, want 0", n)
	}
}

// failingStore refuses to save and records deletions.
type failingStore struct {
	tokenstore.TokenStore
	deleted atomic.Int32
}

func (s *failingStore) Save(string, tokenstore.TokenInfo) error { return errors.New("disk full") }
func (s *failingStore) Delete(name string) error {
	s.deleted.Add(1)
	return s.TokenStore.Delete(name)
}

func TestSaveFailureKeepsTheNewTokensInMemory(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "old", RefreshToken: "r0", ExpiresAt: time.Now()})
	store := &failingStore{TokenStore: f.store}
	c, err := New("p", f.s.URL, store, f.meta, "webda-cli", f.s.Client())
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	c.stderr = &warn
	for i := 0; i < 2; i++ {
		resp, err := f.get(t, c)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	if n := f.tokenCalls(); n != 1 {
		t.Fatalf("token endpoint called %d times, want 1", n)
	}
	if !strings.Contains(warn.String(), "Warning") {
		t.Fatalf("no warning printed: %q", warn.String())
	}
	// the rotated-out r0 must not be replayed by the next run
	if store.deleted.Load() != 1 {
		t.Fatalf("stored entry not cleared")
	}
	if _, err := f.store.Load("p"); err == nil {
		t.Fatal("the rotated-out refresh token is still stored")
	}
}

func TestLegacyTokenIsNeverUsed(t *testing.T) {
	f := newFixture(t, "")
	f.save(t, tokenstore.TokenInfo{AccessToken: "legacy-a", RefreshToken: "legacy-r", Sequence: "3"})
	resp, err := f.get(t, f.client(t))
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if resp.StatusCode != http.StatusUnauthorized || f.seen[0] != "" {
		t.Fatalf("legacy token sent: %v (status %d)", f.seen, resp.StatusCode)
	}
	if n := f.tokenCalls(); n != 0 {
		t.Fatalf("legacy refresh token sent to the token endpoint (%d calls)", n)
	}
}
