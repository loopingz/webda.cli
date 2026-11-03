package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/loopingz/webda.cli/webdaclient"
	"github.com/spf13/cobra"
)

// DeviceUnlock is the server's browser-approved unlock (`cli.device_unlock` of the operations listing): the
// operations of the pairing, the approval page and the header carrying `<session id>.<key>` on every request.
type DeviceUnlock struct {
	Request    string `json:"request"`
	Status     string `json:"status"`
	Lock       string `json:"lock"`
	ApproveURL string `json:"approve_url"`
	Header     string `json:"header"`
}

// unlockPoll is the wait between two status checks while the pairing is pending.
var unlockPoll = 2 * time.Second

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// parseDeviceUnlock reads `cli.device_unlock`; anything incomplete or unsafe (a non-HTTPS approval page outside
// localhost, a reserved header) is ignored so the commands stay hidden.
func parseDeviceUnlock(v any) *DeviceUnlock {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	str := func(k string) string { s, _ := raw[k].(string); return s }
	du := &DeviceUnlock{Request: str("request"), Status: str("status"), Lock: str("lock"), ApproveURL: str("approve_url"), Header: str("header")}
	if du.Request == "" || du.Status == "" || du.Lock == "" || !headerName.MatchString(du.Header) {
		return nil
	}
	switch strings.ToLower(du.Header) {
	case "authorization", "cookie", "host", "content-type", "content-length":
		return nil
	}
	u, err := url.Parse(strings.NewReplacer("{pairing}", "p", "{session}", "s", "{key}", "k").Replace(du.ApproveURL))
	if err != nil || !strings.Contains(du.ApproveURL, "{pairing}") || !strings.Contains(du.ApproveURL, "#") {
		return nil
	}
	local := u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
	if u.Scheme != "https" && !local {
		return nil
	}
	return du
}

// operationsEndpoint is the URL operations are posted to (`<endpoint>/<id>`).
func operationsEndpoint(ops []Operation, baseURL string) string {
	for i := range ops {
		if ops[i].Source != "openapi" && ops[i].Endpoint != "" {
			return strings.TrimRight(ops[i].Endpoint, "/")
		}
	}
	return strings.TrimRight(baseURL, "/") + "/operations"
}

// addIfAbsent adds the commands whose name no server operation already uses.
func addIfAbsent(root *cobra.Command, cmds ...*cobra.Command) {
	for _, cmd := range cmds {
		if existing, _, err := root.Find([]string{cmd.Name()}); err == nil && existing != root {
			continue
		}
		root.AddCommand(cmd)
	}
}

// deviceLabel is the device name the approval page shows: letters, digits, space and `.-_()`, 40 characters.
func deviceLabel(hostname string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune(" .-_()", r):
			return r
		}
		return -1
	}, hostname)
	label := strings.TrimSpace("webda-cli on " + clean)
	if clean == "" {
		label = "webda-cli"
	}
	if len(label) > 40 {
		label = strings.TrimSpace(label[:40])
	}
	return label
}

// newSessionKey returns a random session id (UUID v4) and a 32-byte key (base64url), made on this device only.
func newSessionKey() (string, string, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	id := b[:16]
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	uuid := fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
	return uuid, base64.RawURLEncoding.EncodeToString(b[16:]), nil
}

// callOperation posts input to the operation id and decodes its JSON answer into out (when not nil).
func callOperation(ctx context.Context, client *webdaclient.Client, endpoint, id string, input any, header http.Header, out any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/"+url.PathEscape(id), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s %s", id, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func newUnlockCmd(env authEnv, client *webdaclient.Client, du *DeviceUnlock, endpoint string) *cobra.Command {
	return &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the server-side vault for this profile (approved in the browser)",
		Long: "Asks the server to unlock its vault for this CLI. Approve the request in your browser with the " +
			"confirmation code shown here; every request of the profile then carries the unlock until it ends " +
			"(lock, logout, expiry or lock everywhere).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUnlock(cmd.Context(), env, client, du, endpoint, cmd.ErrOrStderr())
		},
	}
}

func newLockCmd(client *webdaclient.Client, du *DeviceUnlock, endpoint string) *cobra.Command {
	return &cobra.Command{
		Use:   "lock",
		Short: "End the vault unlock of this profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !client.Unlocked() {
				_, err := fmt.Fprintln(cmd.ErrOrStderr(), "This profile is not unlocked.")
				return err
			}
			// ending it server-side first: a key left there would keep working until it expires
			if err := callOperation(cmd.Context(), client, endpoint, du.Lock, map[string]any{}, nil, nil); err != nil {
				return err
			}
			if err := client.SetUnlock("", ""); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Locked.")
			return err
		},
	}
}

// runUnlock pairs this CLI with the browser: the session id and key are made here, the key only travels in the
// approval URL fragment (opened in the browser, never sent to the server by the CLI) and the unlock header.
func runUnlock(ctx context.Context, env authEnv, client *webdaclient.Client, du *DeviceUnlock, endpoint string, out io.Writer) error {
	session, key, err := newSessionKey()
	if err != nil {
		return err
	}
	host, _ := env.Hostname()
	var pairing struct {
		PairingID string `json:"pairingId"`
		Code      string `json:"code"`
		ExpiresAt string `json:"expiresAt"`
	}
	input := map[string]any{"vaultSessionId": session, "deviceName": deviceLabel(host)}
	if err := callOperation(ctx, client, endpoint, du.Request, input, nil, &pairing); err != nil {
		return err
	}
	if pairing.PairingID == "" || pairing.Code == "" {
		return errors.New("the server did not return a pairing")
	}
	approve := strings.NewReplacer(
		"{pairing}", url.QueryEscape(pairing.PairingID), "{session}", session, "{key}", key,
	).Replace(du.ApproveURL)
	_, _ = fmt.Fprintf(out, "Confirmation code: %s\nApprove it in your browser, with this code and a passkey or your recovery code.\n", pairing.Code)
	if err := env.Open(approve); err != nil {
		// headless: the person opens it elsewhere; the fragment carries the key, so it is only printed here
		_, _ = fmt.Fprintf(out, "Open this page (it carries the unlock key: do not share it):\n%s\n", approve)
	}
	deadline := time.Now().Add(10 * time.Minute)
	if t, err := time.Parse(time.RFC3339, pairing.ExpiresAt); err == nil {
		deadline = t
	}
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	value := session + "." + key
	header := http.Header{}
	header.Set(du.Header, value)
	for {
		var status struct {
			Unlocked bool   `json:"unlocked"`
			Pairing  string `json:"pairing"`
		}
		if err := callOperation(waitCtx, client, endpoint, du.Status, map[string]any{"pairing": pairing.PairingID}, header, &status); err != nil {
			if waitCtx.Err() != nil {
				return errors.New("the unlock request expired before it was approved")
			}
			return err
		}
		if status.Unlocked {
			if err := client.SetUnlock(du.Header, value); err != nil {
				return fmt.Errorf("unlocked, but the unlock could not be saved: %w", err)
			}
			_, err := fmt.Fprintln(out, "Unlocked.")
			return err
		}
		if status.Pairing == "gone" {
			return errors.New("the unlock request was denied or expired")
		}
		if err := env.Sleep(waitCtx, unlockPoll); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("the unlock request expired before it was approved")
		}
	}
}
