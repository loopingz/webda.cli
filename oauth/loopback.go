package oauth

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type loopbackResult struct {
	code string
	err  error
}

// Loopback is a one-shot authorization callback listener on 127.0.0.1 (RFC 8252).
type Loopback struct {
	RedirectURI string // http://127.0.0.1:<port>/callback

	listener  net.Listener
	server    *http.Server
	result    chan loopbackResult
	mu        sync.Mutex
	state     string
	resultURL string
	armed     bool
	done      bool
}

// ListenLoopback binds 127.0.0.1:0 and serves GET /callback.
func ListenLoopback() (*Loopback, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("oauth: cannot open loopback listener: %w", err)
	}
	l := &Loopback{
		RedirectURI: fmt.Sprintf("http://%s/callback", ln.Addr().String()),
		listener:    ln,
		result:      make(chan loopbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handle)
	l.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       10 * time.Second,
	}
	go func() { _ = l.server.Serve(ln) }()
	return l, nil
}

func (l *Loopback) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/callback" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		http.Error(w, "Gone", http.StatusGone)
		return
	}
	if !l.armed || q.Get("state") == "" || q.Get("state") != l.state {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	var res loopbackResult
	switch {
	case q.Get("error") != "":
		res.err = &Error{Code: q.Get("error"), Description: q.Get("error_description")}
	case q.Get("code") != "":
		res.code = q.Get("code")
	default:
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	l.done = true
	l.result <- res
	l.respond(w, r, res.err == nil, q.Get("error"))
}

func (l *Loopback) respond(w http.ResponseWriter, r *http.Request, ok bool, code string) {
	if l.resultURL != "" {
		target := l.resultURL + "?status=success"
		if !ok {
			target = l.resultURL + "?status=error&error=" + url.QueryEscape(code)
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	msg := "Signed in. You can close this window."
	if !ok {
		msg = "Sign-in failed."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=\"utf-8\"><title>webda</title><p>%s</p>", html.EscapeString(msg))
}

// Wait blocks until the callback with the given state arrives (returns the code), an authorization
// error (returns *Error), ctx is done, or timeout. Stray requests never end the wait.
func (l *Loopback) Wait(ctx context.Context, state, resultURL string, timeout time.Duration) (string, error) {
	l.mu.Lock()
	l.state, l.resultURL, l.armed = state, resultURL, true
	l.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-l.result:
		return res.code, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("sign-in timed out (%d minutes)", int(timeout.Minutes()))
	}
}

// Close shuts the listener down.
func (l *Loopback) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.server.Shutdown(ctx); err != nil {
		return l.server.Close()
	}
	return nil
}
