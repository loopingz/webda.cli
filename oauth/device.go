package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

// DeviceCode is a device authorization answer (RFC 8628).
type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// StartDevice requests a device and user code.
func StartDevice(ctx context.Context, hc *http.Client, m *Metadata, clientID, scope, hostname string) (*DeviceCode, error) {
	if m.DeviceAuthorizationEndpoint == "" {
		return nil, errors.New("this server does not support the OAuth device flow")
	}
	v := url.Values{"client_id": {clientID}, "scope": {scope}}
	if hostname != "" {
		v.Set("hostname", hostname)
	}
	body, err := postForm(ctx, hc, m.DeviceAuthorizationEndpoint, v)
	if err != nil {
		return nil, err
	}
	var dc DeviceCode
	if err := json.Unmarshal(body, &dc); err != nil || dc.DeviceCode == "" {
		return nil, &Error{Code: "invalid_response", Description: "device authorization answered without a device_code"}
	}
	return &dc, nil
}

// Sleep waits d or until ctx ends, returning ctx.Err() in that case.
func Sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PollDevice polls the token endpoint until tokens, a terminal error, ctx done or expiry.
func PollDevice(ctx context.Context, hc *http.Client, m *Metadata, clientID string, dc *DeviceCode, sleep func(context.Context, time.Duration) error) (*Tokens, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	limit := time.Duration(dc.ExpiresIn) * time.Second
	var slept time.Duration
	for {
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}
		slept += interval
		if limit > 0 && slept > limit {
			return nil, &Error{Code: "expired_token", Description: "the device code expired"}
		}
		tok, err := tokenRequest(ctx, hc, m, url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {clientID}, "device_code": {dc.DeviceCode},
		})
		if err == nil {
			return tok, nil
		}
		var oe *Error
		if errors.As(err, &oe) {
			switch oe.Code {
			case "authorization_pending":
				continue
			case "slow_down":
				interval += 5 * time.Second
				continue
			}
		}
		return nil, err
	}
}
