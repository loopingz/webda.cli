package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tokens is a token endpoint answer.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// ExpiresAt returns now + ExpiresIn (300 s when ExpiresIn <= 0).
func (t Tokens) ExpiresAt(now time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return now.Add(300 * time.Second)
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

// Error is an RFC 6749 error answer.
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("oauth error %s: %s", e.Code, e.Description)
	}
	return "oauth error " + e.Code
}

// postForm POSTs a form and returns the body of a 2xx answer, or an *Error.
func postForm(ctx context.Context, hc *http.Client, endpoint string, v url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &Error{Status: resp.StatusCode}
		var d struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &d) == nil && d.Error != "" {
			e.Code, e.Description = d.Error, d.Description
		} else {
			e.Code = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		return nil, e
	}
	return body, nil
}

func tokenRequest(ctx context.Context, hc *http.Client, m *Metadata, v url.Values) (*Tokens, error) {
	body, err := postForm(ctx, hc, m.TokenEndpoint, v)
	if err != nil {
		return nil, err
	}
	var t Tokens
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return nil, &Error{Code: "invalid_response", Description: "token endpoint answered without an access_token"}
	}
	return &t, nil
}

// ExchangeCode trades an authorization code for tokens.
func ExchangeCode(ctx context.Context, hc *http.Client, m *Metadata, clientID, code, verifier, redirectURI string) (*Tokens, error) {
	return tokenRequest(ctx, hc, m, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"code_verifier": {verifier}, "redirect_uri": {redirectURI},
	})
}

// Refresh trades a refresh token for new tokens; refresh tokens rotate, so the answer's one must be stored.
func Refresh(ctx context.Context, hc *http.Client, m *Metadata, clientID, refreshToken string) (*Tokens, error) {
	return tokenRequest(ctx, hc, m, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refreshToken},
	})
}

// Revoke revokes a refresh token; any 2xx counts as success.
func Revoke(ctx context.Context, hc *http.Client, m *Metadata, clientID, token string) error {
	_, err := postForm(ctx, hc, m.RevocationEndpoint, url.Values{
		"client_id": {clientID}, "token": {token}, "token_type_hint": {"refresh_token"},
	})
	return err
}
