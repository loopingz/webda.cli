package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/mdp/qrterminal/v3"
	browser "github.com/pkg/browser"
	"github.com/spf13/cobra"
)

// signInTimeout bounds the wait for the browser callback.
const signInTimeout = 5 * time.Minute

// authEnv holds the seams of the sign-in (tests replace them).
type authEnv struct {
	HTTP     *http.Client
	Open     func(url string) error // browser.OpenURL
	Getenv   func(string) string
	GOOS     string
	Hostname func() (string, error)
	Out      io.Writer // stderr
	Sleep    func(context.Context, time.Duration) error
	Timeout  time.Duration
}

func defaultAuthEnv() authEnv {
	return authEnv{
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		Open:     browser.OpenURL,
		Getenv:   os.Getenv,
		GOOS:     runtime.GOOS,
		Hostname: os.Hostname,
		Out:      os.Stderr,
		Sleep:    oauth.Sleep,
		Timeout:  signInTimeout,
	}
}

// authOptions are the options of the auth command.
type authOptions struct {
	Scope, Prompt string
	Device        bool
}

// validPrompt accepts the prompt values the authorization server understands.
func validPrompt(p string) error {
	switch p {
	case "", "login", "consent", "none":
		return nil
	}
	return fmt.Errorf("--prompt must be login, consent or none")
}

func metadataCachePath(profile string) string {
	return filepath.Join(configDir(), profile+".oauth.json")
}

// metadataFor returns the cached or discovered metadata of a profile (cache: configDir()/<profile>.oauth.json).
func metadataFor(ctx context.Context, env authEnv, profile string, prof Profile) (*oauth.Metadata, error) {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return nil, err
	}
	m, err := oauth.LoadOrDiscover(ctx, env.HTTP, prof.URL, metadataCachePath(profile))
	if errors.Is(err, oauth.ErrNotSupported) {
		return nil, fmt.Errorf("%s: %w", prof.URL, err)
	}
	return m, err
}

// useDeviceFlow reports whether the device flow must be used (flag, SSH, Linux without display).
func useDeviceFlow(env authEnv, opts authOptions) bool {
	if opts.Device || env.Getenv("SSH_CONNECTION") != "" {
		return true
	}
	return env.GOOS == "linux" && env.Getenv("DISPLAY") == "" && env.Getenv("WAYLAND_DISPLAY") == ""
}

// sanitizeTerminal drops control characters (including ESC) from server-provided text before printing it.
func sanitizeTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// signIn runs the browser flow (falling back to the device flow when the browser cannot be opened) and stores the tokens.
func signIn(ctx context.Context, env authEnv, store tokenstore.TokenStore, profile string, prof Profile, opts authOptions) (tokenstore.TokenInfo, error) {
	m, err := metadataFor(ctx, env, profile, prof)
	if err != nil {
		return tokenstore.TokenInfo{}, err
	}
	scope := opts.Scope
	if scope == "" {
		scope = prof.EffectiveScope()
	}
	hostname, err := env.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	clientID := prof.EffectiveClientID()
	var tokens *oauth.Tokens
	if !useDeviceFlow(env, opts) {
		tokens, err = browserFlow(ctx, env, m, clientID, scope, hostname, opts.Prompt)
		if errors.Is(err, errBrowserUnavailable) {
			_, _ = fmt.Fprintln(env.Out, "Could not open a browser, using the device code instead.")
			tokens, err = deviceFlow(ctx, env, m, clientID, scope, hostname)
		}
	} else {
		tokens, err = deviceFlow(ctx, env, m, clientID, scope, hostname)
	}
	if err != nil {
		return tokenstore.TokenInfo{}, err
	}
	ti := tokenstore.TokenInfo{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    tokens.ExpiresAt(time.Now()),
		Scope:        tokens.Scope,
	}
	if err := store.Save(profile, ti); err != nil {
		return tokenstore.TokenInfo{}, err
	}
	return ti, nil
}

// errBrowserUnavailable means the browser could not be opened; the caller falls back to the device flow.
var errBrowserUnavailable = errors.New("cannot open a browser")

// browserFlow runs the authorization code flow with PKCE through a loopback redirect.
func browserFlow(ctx context.Context, env authEnv, m *oauth.Metadata, clientID, scope, hostname, prompt string) (*oauth.Tokens, error) {
	l, err := oauth.ListenLoopback()
	if err != nil {
		return nil, err
	}
	defer func() { _ = l.Close() }()
	pkce, err := oauth.NewPKCE()
	if err != nil {
		return nil, err
	}
	state, err := oauth.RandomState()
	if err != nil {
		return nil, err
	}
	authorize := oauth.AuthorizeURL(m, oauth.AuthorizeParams{
		ClientID: clientID, RedirectURI: l.RedirectURI, State: state, Challenge: pkce.Challenge,
		Scope: scope, Hostname: hostname, Prompt: prompt,
	})
	_, _ = fmt.Fprintf(env.Out, "Opening your browser to sign in: %s\n", authorize)
	if err := env.Open(authorize); err != nil {
		return nil, errBrowserUnavailable
	}
	_, _ = fmt.Fprintln(env.Out, "Waiting for the sign-in to complete (Ctrl+C to cancel)...")
	code, err := l.Wait(ctx, state, m.ResultURI, env.Timeout)
	if err != nil {
		return nil, err
	}
	return oauth.ExchangeCode(ctx, env.HTTP, m, clientID, code, pkce.Verifier, l.RedirectURI)
}

// deviceFlow runs the device authorization grant, printing the code and a QR code of the verification URL.
func deviceFlow(ctx context.Context, env authEnv, m *oauth.Metadata, clientID, scope, hostname string) (*oauth.Tokens, error) {
	dc, err := oauth.StartDevice(ctx, env.HTTP, m, clientID, scope, hostname)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(env.Out, "To sign in, open %s and enter the code %s\n",
		sanitizeTerminal(dc.VerificationURI), sanitizeTerminal(dc.UserCode))
	if complete := sanitizeTerminal(dc.VerificationURIComplete); complete != "" {
		qrterminal.GenerateWithConfig(complete, qrterminal.Config{
			Level:          qrterminal.L,
			Writer:         env.Out,
			HalfBlocks:     true,
			BlackChar:      qrterminal.BLACK_BLACK,
			WhiteBlackChar: qrterminal.WHITE_BLACK,
			WhiteChar:      qrterminal.WHITE_WHITE,
			BlackWhiteChar: qrterminal.BLACK_WHITE,
			QuietZone:      1,
		})
	}
	return oauth.PollDevice(ctx, env.HTTP, m, clientID, dc, env.Sleep)
}

// signOut revokes the refresh token (best effort) and deletes the stored token.
func signOut(ctx context.Context, env authEnv, store tokenstore.TokenStore, profile string, prof Profile) error {
	ti, loadErr := store.Load(profile)
	// a legacy token is never sent to the OAuth server
	if loadErr == nil && ti.RefreshToken != "" && !ti.Legacy() {
		if err := revoke(ctx, env, profile, prof, ti.RefreshToken); err != nil {
			_, _ = fmt.Fprintf(env.Out, "Warning: could not revoke the sign-in on the server: %v\n", err)
		}
	}
	if err := store.Delete(profile); err != nil && loadErr == nil {
		return err
	}
	if err := os.Remove(metadataCachePath(profile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// revoke revokes a refresh token; a server without revocation_endpoint is skipped silently.
func revoke(ctx context.Context, env authEnv, profile string, prof Profile, refreshToken string) error {
	m, err := metadataFor(ctx, env, profile, prof)
	if err != nil {
		return err
	}
	if m.RevocationEndpoint == "" {
		return nil
	}
	return oauth.Revoke(ctx, env.HTTP, m, prof.EffectiveClientID(), refreshToken)
}

// loadToken returns the stored token when usable; a legacy token is deleted and reported.
func loadToken(store tokenstore.TokenStore, profile string) (ti tokenstore.TokenInfo, ok, legacy bool) {
	ti, err := store.Load(profile)
	if err != nil {
		return tokenstore.TokenInfo{}, false, false
	}
	if ti.Legacy() {
		_ = store.Delete(profile)
		return tokenstore.TokenInfo{}, false, true
	}
	if ti.AccessToken == "" && ti.RefreshToken == "" {
		return tokenstore.TokenInfo{}, false, false
	}
	return ti, true, false
}

// usableToken loads the stored token; a legacy token is deleted and never returned.
func usableToken(store tokenstore.TokenStore, profile string) (tokenstore.TokenInfo, bool) {
	ti, ok, _ := loadToken(store, profile)
	return ti, ok
}

// authenticate ensures the profile has a usable token, signing in on first use when possible.
func authenticate(ctx context.Context, env authEnv, store tokenstore.TokenStore, bin, profile string, prof Profile, interactive bool) error {
	_, ok, legacy := loadToken(store, profile)
	if ok {
		return nil
	}
	if !interactive {
		if legacy {
			_, _ = fmt.Fprintln(env.Out, "Your saved sign-in is from an older version and was removed.")
		}
		return fmt.Errorf("profile %s needs authentication; run %s auth", profile, bin)
	}
	if legacy {
		_, _ = fmt.Fprintln(env.Out, "Your saved sign-in is from an older version; signing in again.")
	}
	_, err := signIn(ctx, env, store, profile, prof, authOptions{})
	return err
}

// stubCommand replaces the action of a command listed only for help and completion.
func stubCommand(cmd *cobra.Command) *cobra.Command {
	cmd.RunE = func(*cobra.Command, []string) error {
		return fmt.Errorf("run %s as a standalone command", cmd.Name())
	}
	return cmd
}

// newAuthCmd builds the auth command: it replaces the stored token with a new sign-in.
func newAuthCmd(env authEnv, profile string, prof Profile) *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Sign in to the selected profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var opts authOptions
		opts.Scope, _ = cmd.Flags().GetString("scope")
		opts.Prompt, _ = cmd.Flags().GetString("prompt")
		opts.Device, _ = cmd.Flags().GetBool("device")
		if err := validPrompt(opts.Prompt); err != nil {
			return err
		}
		if !useDeviceFlow(env, opts) && (!terminalInput(cmd.InOrStdin()) || !terminalOutput(cmd.ErrOrStderr())) {
			return fmt.Errorf("auth requires a terminal; use --device to sign in with a code")
		}
		previous, loadErr := tokenStore.Load(profile)
		if loadErr == nil && previous.Legacy() {
			// a legacy token is never kept nor sent to the OAuth server
			if err := tokenStore.Delete(profile); err != nil {
				return err
			}
			previous = tokenstore.TokenInfo{}
		}
		// the previous token stays stored until signIn saves the new one
		ti, err := signIn(cmd.Context(), env, tokenStore, profile, prof, opts)
		if err != nil {
			return err
		}
		if loadErr == nil && previous.RefreshToken != "" && previous.RefreshToken != ti.RefreshToken {
			if err := revoke(cmd.Context(), env, profile, prof, previous.RefreshToken); err != nil {
				_, _ = fmt.Fprintf(env.Out, "Warning: could not revoke the previous sign-in on the server: %v\n", err)
			}
		}
		_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Signed in to %s.\n", profile)
		return err
	}}
	cmd.Flags().String("scope", "", "OAuth scope to request (default: the profile scope)")
	cmd.Flags().Bool("device", false, "sign in with a code shown in the terminal instead of opening a browser")
	cmd.Flags().String("prompt", "", "browser flow only: ask the server for login, consent or none")
	return cmd
}

// newLogoutCmd builds the logout command: it revokes and deletes the stored token.
func newLogoutCmd(env authEnv, profile string, prof Profile) *cobra.Command {
	return &cobra.Command{Use: "logout", Short: "Sign out of the selected profile", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := signOut(cmd.Context(), env, tokenStore, profile, prof); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Signed out of %s.\n", profile)
		return err
	}}
}
