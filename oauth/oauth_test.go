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
