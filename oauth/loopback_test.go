package oauth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func asError(err error, target **Error) bool { return errors.As(err, target) }

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

// location parses the Location of a redirect.
func location(t *testing.T, r *http.Response) url.Values {
	t.Helper()
	u, err := url.Parse(r.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestLoopbackIgnoresStraysAndReturnsTheCode(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	base := strings.TrimSuffix(l.RedirectURI, "/callback")
	if r, _ := noFollow.Get(l.RedirectURI + "?code=x&state=s1"); r == nil || r.StatusCode != 400 {
		t.Fatalf("callback before Arm: %v", r)
	}
	l.Arm("s1", "https://srv/oauth/done")
	if r, _ := noFollow.Get(base + "/favicon.ico"); r == nil || r.StatusCode != 404 {
		t.Fatalf("favicon: %v", r)
	}
	if r, _ := noFollow.Get(l.RedirectURI + "?code=x&state=WRONG"); r == nil || r.StatusCode != 400 {
		t.Fatalf("wrong state: %v", r)
	}
	// the callback arrives before Wait is called: it is kept for Wait
	r, err := noFollow.Get(l.RedirectURI + "?code=c1&state=s1")
	if err != nil || r.StatusCode != 302 || r.Header.Get("Location") != "https://srv/oauth/done?status=success" {
		t.Fatalf("callback: %v %v", r, err)
	}
	code, werr := l.Wait(context.Background(), 5*time.Second)
	if werr != nil || code != "c1" {
		t.Fatalf("code %q err %v", code, werr)
	}
	if r, _ := noFollow.Get(l.RedirectURI + "?code=c2&state=s1"); r == nil || r.StatusCode != 410 {
		t.Fatalf("second callback: %v", r)
	}
}

func TestLoopbackReportsAuthorizationErrors(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	l.Arm("s1", "https://srv/oauth/done")
	r, _ := noFollow.Get(l.RedirectURI + "?error=access_denied&state=s1")
	if r == nil || !strings.HasPrefix(r.Header.Get("Location"), "https://srv/oauth/done?") {
		t.Fatalf("error redirect: %v", r)
	}
	if q := location(t, r); q.Get("status") != "error" || q.Get("error") != "access_denied" {
		t.Fatalf("error redirect: %v", q)
	}
	_, err := l.Wait(context.Background(), 5*time.Second)
	var oe *Error
	if err == nil || !asError(err, &oe) || oe.Code != "access_denied" {
		t.Fatalf("want access_denied, got %v", err)
	}
}

func TestLoopbackResultURLKeepsItsQuery(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	l.Arm("s1", "https://srv/oauth/done?lang=fr&status=stale")
	r, _ := noFollow.Get(l.RedirectURI + "?code=c&state=s1")
	if r == nil || r.StatusCode != 302 {
		t.Fatalf("%v", r)
	}
	if q := location(t, r); q.Get("lang") != "fr" || q.Get("status") != "success" || len(q["status"]) != 1 {
		t.Fatalf("result redirect: %v", q)
	}
}

func TestLoopbackTimesOut(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	l.Arm("s1", "")
	if _, err := l.Wait(context.Background(), 100*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want timeout, got %v", err)
	}
	// a callback after the wait ended is refused instead of being reported as a success
	if r, _ := noFollow.Get(l.RedirectURI + "?code=c&state=s1"); r == nil || r.StatusCode != 410 {
		t.Fatalf("late callback: %v", r)
	}
}

func TestLoopbackBuiltInPageWithoutResultURL(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	l.Arm("s1", "")
	r, err := noFollow.Get(l.RedirectURI + "?code=c&state=s1")
	if err != nil || r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("%v %v", r, err)
	}
	if code, err := l.Wait(context.Background(), 5*time.Second); err != nil || code != "c" {
		t.Fatalf("%q %v", code, err)
	}
}

func TestLoopbackEscapesErrorCodeAndSilentConnDoesNotBlock(t *testing.T) {
	l, _ := ListenLoopback()
	defer l.Close()
	// a silent connection must not block the real callback
	c, err := (&netDialer{}).dial(strings.TrimPrefix(strings.TrimSuffix(l.RedirectURI, "/callback"), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	l.Arm("s1", "https://srv/d")
	r, _ := noFollow.Get(l.RedirectURI + "?error=a%26b%3Dc&state=s1")
	if r == nil || !strings.HasPrefix(r.Header.Get("Location"), "https://srv/d?") || !strings.Contains(r.Header.Get("Location"), "error=a%26b%3Dc") {
		t.Fatalf("escape: %v", r)
	}
	_, werr := l.Wait(context.Background(), 5*time.Second)
	var oe *Error
	if !asError(werr, &oe) || oe.Code != "a&b=c" {
		t.Fatalf("got %v", werr)
	}
}

type netDialer struct{}

func (*netDialer) dial(addr string) (net.Conn, error) { return net.Dial("tcp", addr) }
