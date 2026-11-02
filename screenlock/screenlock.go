// Package screenlock tells whether the session's screen is locked, by polling what the OS reports (no cgo).
package screenlock

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// ErrUnsupported is returned on systems where the lock state cannot be read.
var ErrUnsupported = errors.New("screen lock detection is only supported on macOS and Linux (systemd-logind)")

// run executes a command and returns its output; tests replace it.
var run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// goos is runtime.GOOS; tests replace it.
var goos = runtime.GOOS

// macOS: the root registry entry says IOConsoleLocked, the console session CGSSessionScreenIsLocked.
var darwinLocked = regexp.MustCompile(`<key>(IOConsoleLocked|CGSSessionScreenIsLocked)</key>\s*<true/>`)

// Locked reports whether the screen of the current session is locked.
func Locked(ctx context.Context) (bool, error) {
	switch goos {
	case "darwin":
		out, err := run(ctx, "ioreg", "-n", "Root", "-d1", "-a")
		if err != nil {
			return false, err
		}
		return darwinLocked.Match(out), nil
	case "linux":
		out, err := run(ctx, "loginctl", "show-session", "auto", "--property=LockedHint", "--value")
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(string(out)) == "yes", nil
	default:
		return false, ErrUnsupported
	}
}

// Supported reports whether the lock state can be read here (the check runs once).
func Supported(ctx context.Context) error {
	_, err := Locked(ctx)
	return err
}

// Wait polls every interval until the screen lock state is `locked` (nil) or ctx ends (its error). Read errors are
// retried: a transient failure must neither count as a change nor stop the watch.
func Wait(ctx context.Context, interval time.Duration, locked bool) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if state, err := Locked(ctx); err == nil && state == locked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
