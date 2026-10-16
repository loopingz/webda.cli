// Package webdaclient is the authenticated HTTP client of the CLI: it attaches the OAuth access token and
// refreshes it (with refresh token rotation) through the authorization server token endpoint.
package webdaclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/loopingz/webda.cli/filelock"
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
	// LockFile, when set, is an advisory lock file held around load→refresh→save so concurrent CLI
	// processes sharing the token store never spend the same rotating refresh token twice.
	LockFile string

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
// A nil hc uses a new http.Client. A legacy token (with a sequence) or a token issued for another server is ignored.
// The client never forwards the access token on a redirect that leaves the profile origin.
func New(name, baseURL string, store tokenstore.TokenStore, meta *oauth.Metadata, clientID string, hc *http.Client) (*Client, error) {
	c := &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Name: name,
		store: store, meta: meta, clientID: clientID,
		origin: oauth.Origin(baseURL), stderr: os.Stderr,
	}
	c.hc = c.ownClient(hc)
	if ti, err := store.Load(name); err == nil && ti.IssuedFor(c.origin) {
		c.token = ti
	}
	return c, nil
}

// ownClient returns a copy of hc whose redirects drop the Authorization header when they leave the profile origin
// (other scheme, host, subdomain or port); the redirect policy of hc otherwise still applies.
func (c *Client) ownClient(hc *http.Client) *http.Client {
	own := &http.Client{}
	if hc != nil {
		*own = *hc
	}
	next := own.CheckRedirect
	own.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if c.origin == "" || oauth.Origin(req.URL.String()) != c.origin {
			req.Header.Del("Authorization")
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return own
}

// Scope returns the scope granted to the current token ("" when unknown).
func (c *Client) Scope() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token.Scope
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
	if c.origin == "" || oauth.Origin(req.URL.String()) != c.origin || req.Header.Get("Authorization") != "" {
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

// expiring reports whether the access token is missing or expires within refreshSkew; zero ExpiresAt is unknown (never expiring).
func expiring(ti tokenstore.TokenInfo, now time.Time) bool {
	if ti.AccessToken == "" {
		return ti.RefreshToken != ""
	}
	return !ti.ExpiresAt.IsZero() && now.Add(refreshSkew).After(ti.ExpiresAt)
}

func (c *Client) expiringLocked(now time.Time) bool { return expiring(c.token, now) }

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
		err := c.exchange(rctx, previous, failed)
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

// exchange runs the refresh_token grant, saving the rotated token before it is used. Under the lock file it first
// reloads the store: when another process already rotated the refresh token, its token is adopted (or refreshed).
func (c *Client) exchange(ctx context.Context, previous tokenstore.TokenInfo, failed string) error {
	if c.LockFile != "" {
		release, err := filelock.Lock(ctx, c.LockFile)
		if err != nil {
			return fmt.Errorf("token refresh failed: %w", err)
		}
		defer release()
	}
	if stored, err := c.store.Load(c.Name); err == nil && stored.IssuedFor(c.origin) &&
		stored.RefreshToken != "" && stored.RefreshToken != previous.RefreshToken {
		if stored.AccessToken != "" && stored.AccessToken != failed && !expiring(stored, time.Now()) {
			c.mu.Lock()
			c.token = stored
			c.mu.Unlock()
			return nil
		}
		previous = stored
	}
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
		Server:       c.origin,
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
