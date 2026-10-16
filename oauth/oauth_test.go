package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestDiscoverRejectsOtherOrigin(t *testing.T) {
	m := &Metadata{Issuer: "http://h:80", TokenEndpoint: "http://H/t", AuthorizationEndpoint: "http://h/a"}
	if err := validate("http://h/base", m); err != nil {
		t.Fatalf("same origin (case, default port): %v", err)
	}
	m.TokenEndpoint = "http://evil/t"
	if err := validate("http://h", m); err == nil || !strings.Contains(err.Error(), "token_endpoint") {
		t.Fatalf("want token_endpoint error, got %v", err)
	}
	m.TokenEndpoint = "https://h/t"
	if validate("http://h", m) == nil {
		t.Fatal("scheme mismatch must fail")
	}
	m.TokenEndpoint = "http://h/t"
	m.RevocationEndpoint = "http://evil/r"
	if validate("http://h", m) == nil {
		t.Fatal("revocation_endpoint on another origin must fail")
	}
}

func TestDiscoverDropsCrossOriginResultURI(t *testing.T) {
	m := &Metadata{Issuer: "http://h", TokenEndpoint: "http://h/t", AuthorizationEndpoint: "http://h/a",
		ResultURI: "https://app.example.com/oauth/done", OperationsURI: "http://h/ops"}
	if err := validate("http://h", m); err != nil {
		t.Fatalf("a cross-origin result page must not reject discovery: %v", err)
	}
	if m.ResultURI != "" || m.OperationsURI != "http://h/ops" {
		t.Fatalf("result URI must be dropped (built-in page): %+v", m)
	}
	m.ResultURI = "http://h/oauth/done"
	if err := validate("http://h", m); err != nil || m.ResultURI != "http://h/oauth/done" {
		t.Fatalf("same-origin result URI is kept: %+v %v", m, err)
	}
	m.OperationsURI = "http://evil/ops"
	if validate("http://h", m) == nil {
		t.Fatal("webda_operations_uri on another origin must fail")
	}
}

func TestOrigin(t *testing.T) {
	cases := map[string]string{
		"https://H.example.com/x": "https://h.example.com:443",
		"http://h:8080":           "http://h:8080",
		"/relative":               "",
		"::":                      "",
	}
	for in, want := range cases {
		if got := Origin(in); got != want {
			t.Errorf("Origin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitize(t *testing.T) {
	if got := Sanitize("ab\x1b[31mc\n"); got != "ab[31mc" {
		t.Fatalf("%q", got)
	}
}

func TestDiscoverOtherOriginServer(t *testing.T) {
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"` + other.URL + `","token_endpoint":"` + other.URL + `/t","authorization_endpoint":"` + other.URL + `/a"}`))
	}))
	defer srv.Close()
	if _, err := Discover(context.Background(), http.DefaultClient, srv.URL); err == nil {
		t.Fatal("other-origin endpoints must be rejected")
	}
}

func TestPoisonedCacheIgnored(t *testing.T) {
	s := oauthtest.New()
	defer s.Close()
	cache := filepath.Join(t.TempDir(), "p.oauth.json")
	poisoned := `{"base_url":"` + s.URL + `","metadata":{"issuer":"` + s.URL + `","token_endpoint":"http://evil/t","authorization_endpoint":"` + s.URL + `/a"}}`
	if err := os.WriteFile(cache, []byte(poisoned), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadOrDiscover(context.Background(), http.DefaultClient, s.URL, cache)
	if err != nil || m.TokenEndpoint != s.URL+"/oauth/token" {
		t.Fatalf("%+v %v", m, err)
	}
	st, err := os.Stat(cache)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode %v %v", st.Mode(), err)
	}
}

func TestDiscoveryRedirectNotFollowed(t *testing.T) {
	var hit int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hit, 1)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	if _, err := Discover(context.Background(), http.DefaultClient, srv.URL); err == nil {
		t.Fatal("302 must be an error")
	}
	if atomic.LoadInt32(&hit) != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestTokenRedirectNotFollowed(t *testing.T) {
	var hit int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hit, 1)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	m := &Metadata{TokenEndpoint: srv.URL, RevocationEndpoint: srv.URL}
	_, err := Refresh(context.Background(), http.DefaultClient, m, "webda-cli", "secret")
	var oe *Error
	if !errors.As(err, &oe) || oe.Code != "http_307" {
		t.Fatalf("want http_307, got %v", err)
	}
	if Revoke(context.Background(), http.DefaultClient, m, "webda-cli", "secret") == nil {
		t.Fatal("revoke redirect must fail")
	}
	if atomic.LoadInt32(&hit) != 0 {
		t.Fatal("the other host received the request")
	}
}

func TestEmptyEndpointNoRequest(t *testing.T) {
	var calls int32
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("must not be called")
	})
	hc := &http.Client{Transport: rt}
	m := &Metadata{}
	ctx := context.Background()
	if Revoke(ctx, hc, m, "c", "t") == nil {
		t.Fatal("revoke")
	}
	if _, err := Refresh(ctx, hc, m, "c", "t"); err == nil {
		t.Fatal("refresh")
	}
	if _, err := ExchangeCode(ctx, hc, m, "c", "c", "v", "r"); err == nil {
		t.Fatal("exchange")
	}
	if _, err := StartDevice(ctx, hc, m, "c", "s", "h"); err == nil {
		t.Fatal("device")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("a request was sent")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestErrorStripsControlChars(t *testing.T) {
	e := &Error{Code: "bad\x1b[31m", Description: "x\x1b[2Jy\x7f\n"}
	if got := e.Error(); got != "oauth error bad[31m: x[2Jy" {
		t.Fatalf("%q", got)
	}
}
