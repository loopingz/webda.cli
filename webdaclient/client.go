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
	// PresenceCheck confirms the person is there before an unlock stored with Presence is sent; nil refuses.
	PresenceCheck func() error
	// Opener opens a sealed unlock (prompting the person); nil refuses.
	Opener func(sealed string) (string, error)

	store    tokenstore.TokenStore
	meta     *oauth.Metadata
	clientID string
	hc       *http.Client
	origin   string
	stderr   io.Writer

	mu       sync.Mutex
	token    tokenstore.TokenInfo
	inflight *call
	// opened is the unlock value confirmed (or opened) in this process
	opened string
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
	unlock := c.attachUnlock(first)
	resp, err := c.hc.Do(first)
	if err == nil && resp.StatusCode == http.StatusLocked {
		if unlock != "" {
			c.unlockEnded(unlock)
		} else if c.withheld(first) {
			// the server needs the unlock this process withheld until the person confirms their presence
			return c.retryWithPresence(req, resp, sent)
		}
	}
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
	c.attachUnlock(retry)
	return c.hc.Do(retry)
}

// Unlock is a device unlock as stored with the tokens.
type Unlock struct {
	Header string
	// Key is `<session id>.<key>` in clear; empty when Sealed holds it.
	Key string
	// Sealed is the key sealed to hardware (Secure Enclave): Opener opens it, once per process.
	Sealed string
	// Presence withholds Key until PresenceCheck passes, once per process (when no hardware seals it).
	Presence bool
}

func unlockOf(ti tokenstore.TokenInfo) Unlock {
	return Unlock{Header: ti.UnlockHeader, Key: ti.UnlockKey, Sealed: ti.UnlockSealed, Presence: ti.UnlockPresence}
}

func setUnlock(ti *tokenstore.TokenInfo, u Unlock) {
	ti.UnlockHeader, ti.UnlockKey, ti.UnlockSealed, ti.UnlockPresence = u.Header, u.Key, u.Sealed, u.Presence
}

// id identifies the stored unlock without opening it ("" when none).
func (u Unlock) id() string {
	if u.Sealed != "" {
		return u.Sealed
	}
	return u.Key
}

// usableLocked is the unlock value to send now without prompting ("" when none or not yet confirmed).
func (c *Client) usableLocked() string {
	u := unlockOf(c.token)
	switch {
	case u.Header == "":
		return ""
	case u.Sealed != "":
		return c.opened
	case u.Presence:
		if c.opened == u.Key {
			return u.Key
		}
		return ""
	default:
		return u.Key
	}
}

// withheld reports whether req went without the stored unlock because it waits for the person's presence.
func (c *Client) withheld(req *http.Request) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	u := unlockOf(c.token)
	return u.id() != "" && c.usableLocked() == "" && req.Header.Get(u.Header) == ""
}

// retryWithPresence confirms the person's presence (opening a sealed unlock), once per process, then replays req with
// the unlock. A refused confirmation or a body that cannot be replayed returns the 423 as it came.
func (c *Client) retryWithPresence(req *http.Request, resp *http.Response, bearer string) (*http.Response, error) {
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return resp, nil
	}
	if err := c.confirmPresence(); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "The vault unlock was not used: %v\n", err)
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	if bearer != "" {
		retry.Header.Set("Authorization", "Bearer "+bearer)
	}
	unlock := c.attachUnlock(retry)
	again, err := c.hc.Do(retry)
	if err == nil && again.StatusCode == http.StatusLocked && unlock != "" {
		c.unlockEnded(unlock)
	}
	return again, err
}

// confirmPresence opens a sealed unlock with Opener, or runs PresenceCheck for a guarded one; the result is kept for
// the process.
func (c *Client) confirmPresence() error {
	c.mu.Lock()
	u := unlockOf(c.token)
	if c.usableLocked() != "" || u.id() == "" {
		c.mu.Unlock()
		return nil
	}
	check, opener := c.PresenceCheck, c.Opener
	c.mu.Unlock()
	var value string
	switch {
	case u.Sealed != "":
		if opener == nil {
			return errors.New("this unlock is sealed to hardware this build cannot use")
		}
		plain, err := opener(u.Sealed)
		if err != nil {
			return err
		}
		value = plain
	default:
		if check == nil {
			return errors.New("this unlock requires a presence check, which is not available here")
		}
		if err := check(); err != nil {
			return err
		}
		value = u.Key
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if unlockOf(c.token).id() == u.id() {
		c.opened = value
	}
	return nil
}

// attachUnlock adds the usable device unlock header to req unless the caller set that header itself; it returns the
// value sent ("" when none).
func (c *Client) attachUnlock(req *http.Request) string {
	c.mu.Lock()
	header, value := c.token.UnlockHeader, c.usableLocked()
	c.mu.Unlock()
	if value == "" || req.Header.Get(header) != "" {
		return ""
	}
	req.Header.Set(header, value)
	return value
}

// unlockEnded drops a device unlock the server answered 423 to (its session ended: locked, expired or revoked).
func (c *Client) unlockEnded(sent string) {
	c.mu.Lock()
	if c.usableLocked() != sent {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	if err := c.SetUnlock(Unlock{}); err == nil {
		_, _ = fmt.Fprintln(c.stderr, "The vault unlock of this profile has ended; run unlock to unlock it again.")
	}
}

// Unlocked reports whether a device unlock is stored for the profile.
func (c *Client) Unlocked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return unlockOf(c.token).id() != ""
}

// UnlockID identifies the stored device unlock without opening it ("" when none).
func (c *Client) UnlockID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return unlockOf(c.token).id()
}

// ReloadUnlock adopts the device unlock another process stored (or cleared) for the profile.
func (c *Client) ReloadUnlock() {
	stored, err := c.store.Load(c.Name)
	if err != nil || !stored.IssuedFor(c.origin) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if unlockOf(stored).id() != unlockOf(c.token).id() {
		c.opened = ""
	}
	setUnlock(&c.token, unlockOf(stored))
}

// SetUnlock stores (or, with a zero Unlock, clears) the device unlock, with the tokens.
func (c *Client) SetUnlock(u Unlock) error {
	if u.Key == "" && u.Sealed == "" {
		u = Unlock{}
	}
	c.mu.Lock()
	if u.id() != unlockOf(c.token).id() {
		c.opened = ""
	}
	setUnlock(&c.token, u)
	ti := c.token
	c.mu.Unlock()
	if c.LockFile != "" {
		release, err := filelock.Lock(context.Background(), c.LockFile)
		if err != nil {
			return err
		}
		defer release()
	}
	// keep tokens another process rotated meanwhile
	if stored, err := c.store.Load(c.Name); err == nil && stored.IssuedFor(c.origin) && stored.RefreshToken != "" {
		setUnlock(&stored, u)
		ti = stored
	}
	return c.store.Save(c.Name, ti)
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
	// a device unlock is bound to the refresh token family, which rotation keeps
	setUnlock(&ti, unlockOf(previous))
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
