package oauth

import (
	"context"
	"errors"
	"net"
	"net/http"
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
	if r, _ := noFollow.Get(base + "/favicon.ico"); r == nil || r.StatusCode != 404 {
		t.Fatalf("favicon: %v", r)
	}
	if r, _ := noFollow.Get(l.RedirectURI + "?code=x&state=WRONG"); r == nil || r.StatusCode != 400 {
		t.Fatalf("wrong state: %v", r)
	}
	r, err := noFollow.Get(l.RedirectURI + "?code=c1&state=s1")
	if err != nil || r.StatusCode != 302 || r.Header.Get("Location") != "https://srv/oauth/done?status=success" {
		t.Fatalf("callback: %v %v", r, err)
	}
	<-done
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
	go func() {
		time.Sleep(50 * time.Millisecond)
		r, _ := noFollow.Get(l.RedirectURI + "?error=access_denied&state=s1")
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
	r, err := noFollow.Get(l.RedirectURI + "?code=c&state=s1")
	if err != nil || r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("%v %v", r, err)
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
	go func() {
		time.Sleep(50 * time.Millisecond)
		r, _ := noFollow.Get(l.RedirectURI + "?error=a%26b%3Dc&state=s1")
		if r == nil || r.Header.Get("Location") != "https://srv/d?status=error&error=a%26b%3Dc" {
			t.Errorf("escape: %v", r)
		}
	}()
	_, werr := l.Wait(context.Background(), "s1", "https://srv/d", 5*time.Second)
	var oe *Error
	if !asError(werr, &oe) || oe.Code != "a&b=c" {
		t.Fatalf("got %v", werr)
	}
}

type netDialer struct{}

func (*netDialer) dial(addr string) (net.Conn, error) { return net.Dial("tcp", addr) }
