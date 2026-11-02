package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/loopingz/webda.cli/presence"
	"github.com/loopingz/webda.cli/screenlock"
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

// unlockOptions are the guards of an unlock.
type unlockOptions struct {
	// Presence withholds the unlock until Touch ID or the account password confirms the person is there (macOS).
	Presence bool
	// ScreenLock starts a background watcher that locks the unlock when the screen locks.
	ScreenLock bool
}

func newUnlockCmd(env authEnv, client *webdaclient.Client, du *DeviceUnlock, endpoint, profile string, prof Profile) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the server-side vault for this profile (approved in the browser)",
		Long: "Asks the server to unlock its vault for this CLI. Approve the request in your browser with the " +
			"confirmation code shown here; every request of the profile then carries the unlock until it ends " +
			"(lock, logout, expiry, lock everywhere, or the screen locking with --lock-on-screen-lock).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var opts unlockOptions
			opts.Presence, _ = cmd.Flags().GetBool("require-presence")
			opts.ScreenLock, _ = cmd.Flags().GetBool("lock-on-screen-lock")
			return runUnlock(cmd.Context(), env, client, du, endpoint, profile, opts, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().Bool("require-presence", prof.UnlockPresence, "ask for Touch ID or the account password before the unlock is used (macOS, once per process)")
	cmd.Flags().Bool("lock-on-screen-lock", prof.LockOnScreenLock, "lock the unlock when the screen locks (macOS, Linux)")
	return cmd
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
			if err := lockNow(cmd.Context(), client, du, endpoint); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Locked.")
			return err
		},
	}
}

// lockNow ends the unlock on the server, then forgets it: a key left there would keep working until it expires.
func lockNow(ctx context.Context, client *webdaclient.Client, du *DeviceUnlock, endpoint string) error {
	if err := callOperation(ctx, client, endpoint, du.Lock, map[string]any{}, nil, nil); err != nil {
		return err
	}
	return client.SetUnlock("", "", false)
}

// unlockFingerprint identifies an unlock without revealing it (the watcher's argument).
func unlockFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// presenceSupported and screenSupported probe the guards; tests replace them.
var (
	presenceSupported = presence.Supported
	screenSupported   = screenlock.Supported
)

// checkGuards fails before pairing when a requested guard cannot work here.
func checkGuards(ctx context.Context, opts unlockOptions) error {
	if opts.Presence && !presenceSupported() {
		return presence.ErrUnsupported
	}
	if opts.ScreenLock {
		if err := screenSupported(ctx); err != nil {
			return fmt.Errorf("--lock-on-screen-lock: %w", err)
		}
	}
	return nil
}

// runUnlock pairs this CLI with the browser: the session id and key are made here, the key only travels in the
// approval URL fragment (opened in the browser, never sent to the server by the CLI) and the unlock header.
func runUnlock(ctx context.Context, env authEnv, client *webdaclient.Client, du *DeviceUnlock, endpoint, profile string, opts unlockOptions, out io.Writer) error {
	if err := checkGuards(ctx, opts); err != nil {
		return err
	}
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
			if err := client.SetUnlock(du.Header, value, opts.Presence); err != nil {
				return fmt.Errorf("unlocked, but the unlock could not be saved: %w", err)
			}
			if opts.ScreenLock {
				if err := spawnLockWatcher(profile, unlockFingerprint(value)); err != nil {
					// never leave an unlock the person expects to end with the screen
					_ = lockNow(context.WithoutCancel(ctx), client, du, endpoint)
					return fmt.Errorf("could not start the screen lock watcher, locked again: %w", err)
				}
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
