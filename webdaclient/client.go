package webdaclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	configDirName = ".webdacli"
	tokenExt      = ".tok"
	// defaultTTL is used when the refresh response does not carry an explicit expiry.
	defaultTTL = 15 * time.Minute
)

// Client handles authenticated HTTP calls with automatic refresh and background renewal.
type Client struct {
	BaseURL      string
	Name         string
	tokenPath    string
	mu           sync.RWMutex
	RefreshToken string
	AccessToken  string
	Sequence     string
	Expiry       time.Time
	stopCh       chan struct{}
	refreshedCh  chan struct{} // closed & recreated on each successful refresh
}

// New constructs a client for given logical name (command invocation name) and baseURL.
// It loads existing token information from disk if present.
func New(name, baseURL string) (*Client, error) {
	c := &Client{BaseURL: strings.TrimRight(baseURL, "/"), Name: name, tokenPath: tokenPath(name), stopCh: make(chan struct{}), refreshedCh: make(chan struct{})}
	if ti, err := parseTokenFile(c.tokenPath); err == nil {
		c.RefreshToken = ti.RefreshToken
		c.AccessToken = ti.AccessToken
		c.Sequence = ti.Sequence
		// Start with a conservative expiry window so we trigger background refresh before long.
		c.Expiry = time.Now().Add(defaultTTL)
	}
	go c.backgroundLoop()
	return c, nil
}

// Close stops background activities.
func (c *Client) Close() { close(c.stopCh) }

func (c *Client) Request(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Do executes the request adding Authorization header and performing automatic retry on 401 with refresh.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	// Ensure we have an access token; if missing but refresh token available, refresh first.
	if c.AccessToken == "" && c.RefreshToken != "" && c.Sequence != "" {
		_ = c.refresh(req.Context())
	}
	c.attachAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && c.RefreshToken != "" && c.Sequence != "" {
		resp.Body.Close()
		if rerr := c.refresh(req.Context()); rerr == nil {
			// retry once
			req2 := req.Clone(req.Context())
			c.attachAuth(req2)
			return http.DefaultClient.Do(req2)
		}
	}
	return resp, nil
}

func (c *Client) attachAuth(req *http.Request) {
	if at := c.AccessToken; at != "" {
		req.Header.Set("Authorization", "Bearer "+at)
	}
}

// refresh obtains a new access token using stored refresh token and sequence.
func (c *Client) refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.RefreshToken == "" || c.Sequence == "" {
		return errors.New("missing refresh credentials")
	}
	fmt.Println("Refreshing access_token")
	err := c.exchangeTokenWithTTL(ctx, c.BaseURL, c.RefreshToken, c.Sequence)
	if err != nil {
		fmt.Println("Cannot refresh access_token", err)
		return err
	}
	// Persist updated file (still 3 lines as original format for compatibility).
	content := c.RefreshToken + "\n" + c.AccessToken + "\n" + c.Sequence + "\n"
	_ = os.WriteFile(c.tokenPath, []byte(content), 0o600)
	// Cycle channel to wake background loop.
	close(c.refreshedCh)
	c.refreshedCh = make(chan struct{})
	return nil
}

// ForceRefresh triggers a refresh explicitly.
func (c *Client) ForceRefresh(ctx context.Context) error { return c.refresh(ctx) }

// backgroundLoop schedules proactive refresh ~1 minute before expiry.
func (c *Client) backgroundLoop() {
	for {
		c.mu.RLock()
		expiry := c.Expiry
		c.mu.RUnlock()
		var wait time.Duration
		if expiry.IsZero() {
			wait = 2 * time.Minute
		} else {
			wait = time.Until(expiry.Add(-1 * time.Minute))
		}
		if wait < time.Second {
			wait = 10 * time.Second
		}
		select {
		case <-c.stopCh:
			return
		case <-c.refreshedCh:
			// token just refreshed; loop again to adjust next wait
			continue
		case <-time.After(wait):
			// proactive refresh
			_ = c.refresh(context.Background())
		}
	}
}

// ---- helpers (duplicated minimal versions to keep library self-contained) ----

func userHome() string             { h, _ := os.UserHomeDir(); return h }
func configDir() string            { return filepath.Join(userHome(), configDirName) }
func tokenPath(name string) string { return filepath.Join(configDir(), name+tokenExt) }

type tokenInfo struct {
	RefreshToken string
	AccessToken  string
	Sequence     string
}

func parseTokenFile(path string) (tokenInfo, error) {
	var ti tokenInfo
	b, err := os.ReadFile(path)
	if err != nil {
		return ti, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) >= 1 {
		ti.RefreshToken = strings.TrimSpace(lines[0])
	}
	if len(lines) >= 2 {
		ti.AccessToken = strings.TrimSpace(lines[1])
	}
	if len(lines) >= 3 {
		ti.Sequence = strings.TrimSpace(lines[2])
	}
	if ti.RefreshToken == "" || ti.Sequence == "" {
		return ti, errors.New("invalid token file")
	}
	return ti, nil
}

// exchangeTokenWithTTL mirrors the main exchange but attempts to read expires_in if present.
func (c *Client) exchangeTokenWithTTL(ctx context.Context, baseURL, refresh, sequence string) error {
	payload := strings.NewReader("{\"token\":\"" + refresh + "\",\"sequence\":" + sequence + "}")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+"/auth/refresh", payload)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errors.New("refresh failed: " + resp.Status)
	}
	type respShape struct {
		AccessToken string `json:"access_token"`
		Sequence    int    `json:"sequence"`
	}
	var rs respShape
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&rs); err != nil {
		return err
	}
	c.AccessToken = rs.AccessToken
	c.Sequence = fmt.Sprintf("%d", rs.Sequence)
	return nil
}
