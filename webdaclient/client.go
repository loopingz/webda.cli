// Package webdaclient is the authenticated HTTP client of the CLI: it attaches the OAuth access token and
// refreshes it (with refresh token rotation) through the authorization server token endpoint.
package webdaclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/tokenstore"
)

const (
	// refreshSkew refreshes an access token this long before it expires.
	refreshSkew = 60 * time.Second
	// refreshTimeout bounds a refresh; it runs detached from the caller so a rotated token is never lost mid-answer.
	refreshTimeout = 30 * time.Second
)

// ErrSignedOut is returned when the refresh token is refused: the stored token is deleted, the user must sign in.
var ErrSignedOut = errors.New("signed out: run auth to sign in again")

// call is a refresh in flight; waiters block on done.
type call struct {
	done chan struct{}
	err  error
}

// Client handles authenticated HTTP calls to one profile, refreshing its OAuth token when needed.
type Client struct {
	BaseURL string
	Name    string

	store    tokenstore.TokenStore
	meta     *oauth.Metadata
	clientID string
	hc       *http.Client
	origin   string
	stderr   io.Writer

	mu       sync.Mutex
	token    tokenstore.TokenInfo
	inflight *call
}

// New loads the stored token; refreshing uses the metadata's token endpoint and clientID.
// A nil hc uses http.DefaultClient. A legacy token (with a sequence) is ignored.
func New(name, baseURL string, store tokenstore.TokenStore, meta *oauth.Metadata, clientID string, hc *http.Client) (*Client, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	c := &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Name: name,
		store: store, meta: meta, clientID: clientID, hc: hc,
		origin: originOf(baseURL), stderr: os.Stderr,
	}
	if ti, err := store.Load(name); err == nil && !ti.Legacy() {
		c.token = ti
	}
	return c, nil
}

// Request sends method path (relative to BaseURL) with body through Do.
func (c *Client) Request(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Do attaches the bearer (refreshing first when it expires within 60 s), and on a 401 refreshes once and retries.
// Only requests to the profile origin carry the token; a request whose body cannot be replayed is not retried.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.origin == "" || originOf(req.URL.String()) != c.origin || req.Header.Get("Authorization") != "" {
		return c.hc.Do(req)
	}
	ctx := req.Context()
	sent, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	first := req.Clone(ctx)
	if sent != "" {
		first.Header.Set("Authorization", "Bearer "+sent)
	}
	resp, err := c.hc.Do(first)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return resp, nil
	}
	c.mu.Lock()
	canRefresh := c.token.RefreshToken != ""
	c.mu.Unlock()
	if !canRefresh {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	fresh, err := c.refresh(ctx, sent, true)
	if err != nil {
		return nil, err
	}
	retry := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	if fresh != "" {
		retry.Header.Set("Authorization", "Bearer "+fresh)
	}
	return c.hc.Do(retry)
}

// expiringLocked reports whether the access token is missing or expires within refreshSkew; zero ExpiresAt is unknown (never expiring).
func (c *Client) expiringLocked(now time.Time) bool {
	if c.token.AccessToken == "" {
		return c.token.RefreshToken != ""
	}
	return !c.token.ExpiresAt.IsZero() && now.Add(refreshSkew).After(c.token.ExpiresAt)
}

// accessToken returns the access token to send, refreshing it first when it is about to expire.
// A transient refresh failure falls back to the current token while it has not expired yet.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	ti := c.token
	expiring := c.expiringLocked(time.Now())
	c.mu.Unlock()
	if !expiring || ti.RefreshToken == "" {
		return ti.AccessToken, nil
	}
	at, err := c.refresh(ctx, ti.AccessToken, false)
	if err != nil && !errors.Is(err, ErrSignedOut) && ti.AccessToken != "" && time.Now().Before(ti.ExpiresAt) {
		return ti.AccessToken, nil
	}
	return at, err
}

// refresh obtains a new access token; failed is the access token the caller used.
// Concurrent callers share one refresh; a caller whose token was already rotated away reuses the new one.
func (c *Client) refresh(ctx context.Context, failed string, unauthorized bool) (string, error) {
	c.mu.Lock()
	if cl := c.inflight; cl != nil {
		c.mu.Unlock()
		return c.wait(ctx, cl)
	}
	current := c.token.AccessToken
	if current != "" && current != failed && !c.expiringLocked(time.Now()) {
		c.mu.Unlock()
		return current, nil
	}
	if !unauthorized && !c.expiringLocked(time.Now()) {
		c.mu.Unlock()
		return current, nil
	}
	if c.token.RefreshToken == "" {
		c.mu.Unlock()
		return "", ErrSignedOut
	}
	cl := &call{done: make(chan struct{})}
	c.inflight = cl
	previous := c.token
	c.mu.Unlock()
	// detached from the caller: an answer carrying a rotated refresh token must always be read and saved
	go func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer cancel()
		err := c.exchange(rctx, previous)
		c.mu.Lock()
		c.inflight = nil
		c.mu.Unlock()
		cl.err = err
		close(cl.done)
	}()
	return c.wait(ctx, cl)
}

func (c *Client) wait(ctx context.Context, cl *call) (string, error) {
	select {
	case <-cl.done:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if cl.err != nil {
		return "", cl.err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token.AccessToken, nil
}

// refusedCodes are the token endpoint errors that mean the refresh token will never work again.
var refusedCodes = map[string]bool{"invalid_grant": true, "invalid_client": true, "unauthorized_client": true}

// exchange runs the refresh_token grant, saving the rotated token before it is used.
func (c *Client) exchange(ctx context.Context, previous tokenstore.TokenInfo) error {
	if c.meta == nil || c.meta.TokenEndpoint == "" {
		return errors.New("token refresh failed: the server does not advertise a token endpoint")
	}
	t, err := oauth.Refresh(ctx, c.hc, c.meta, c.clientID, previous.RefreshToken)
	if err != nil {
		var oe *oauth.Error
		if errors.As(err, &oe) && (oe.Status == http.StatusBadRequest || oe.Status == http.StatusUnauthorized) && refusedCodes[oe.Code] {
			c.mu.Lock()
			c.token = tokenstore.TokenInfo{}
			c.mu.Unlock()
			_ = c.store.Delete(c.Name)
			return ErrSignedOut
		}
		return fmt.Errorf("token refresh failed: %w", err)
	}
	ti := tokenstore.TokenInfo{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    t.ExpiresAt(time.Now()),
		Scope:        t.Scope,
	}
	if ti.RefreshToken == "" {
		ti.RefreshToken = previous.RefreshToken
	}
	if ti.Scope == "" {
		ti.Scope = previous.Scope
	}
	if err := c.store.Save(c.Name, ti); err != nil {
		// keep going with the new tokens in memory, but never leave the rotated-out refresh token stored
		_, _ = fmt.Fprintf(c.stderr, "Warning: could not save the refreshed sign-in (%v); you will need to run auth next time.\n", err)
		_ = c.store.Delete(c.Name)
	}
	c.mu.Lock()
	c.token = ti
	c.mu.Unlock()
	return nil
}

// originOf returns scheme://host:port with the default port made explicit ("" when raw is not absolute).
func originOf(raw string) string {
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
