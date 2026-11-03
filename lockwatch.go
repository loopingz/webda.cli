package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/loopingz/webda.cli/screenlock"
	"github.com/loopingz/webda.cli/webdaclient"
	"github.com/spf13/cobra"
)

// lockWatchCommand is the hidden command the background watcher runs.
const lockWatchCommand = "__lock-watch"

// screenPoll is how often the screen lock state is read.
var screenPoll = 3 * time.Second

// screenLocked and screenWait read the screen lock state; tests replace them.
var (
	screenLocked = screenlock.Locked
	screenWait   = screenlock.Wait
)

// spawnLockWatcher starts `<cli> --profile <profile> __lock-watch <fingerprint>` detached from the terminal; tests
// replace it.
var spawnLockWatcher = func(profile, fingerprint string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--profile", profile, lockWatchCommand, fingerprint)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runLockWatch locks the unlock identified by fingerprint when the screen locks, and stops once that unlock is gone
// or replaced (lock, logout, 423, a new unlock with its own watcher).
func runLockWatch(ctx context.Context, client *webdaclient.Client, du *DeviceUnlock, endpoint, fingerprint string) error {
	t := time.NewTicker(screenPoll)
	defer t.Stop()
	for {
		client.ReloadUnlock()
		id := client.UnlockID()
		if id == "" || unlockFingerprint(id) != fingerprint {
			return nil
		}
		if locked, err := screenLocked(ctx); err == nil && locked {
			return lockNow(ctx, client, du, endpoint)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func newLockWatchCmd(client *webdaclient.Client, du *DeviceUnlock, endpoint string) *cobra.Command {
	return &cobra.Command{
		Use:    lockWatchCommand + " <fingerprint>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLockWatch(cmd.Context(), client, du, endpoint, args[0])
		},
	}
}

// watchScreenForMCP locks the profile unlock each time the screen locks while `mcp` runs (an unlock made again
// afterwards is picked up by the next tool call, and locked at the next screen lock).
func watchScreenForMCP(ctx context.Context, client *webdaclient.Client, du *DeviceUnlock, endpoint string, log func(string)) {
	for {
		if screenWait(ctx, screenPoll, true) != nil {
			return
		}
		client.ReloadUnlock()
		if client.Unlocked() {
			if err := lockNow(ctx, client, du, endpoint); err != nil {
				log(fmt.Sprintf("screen locked, but the vault unlock could not be locked: %v", err))
			} else {
				log("screen locked: the vault unlock was locked")
			}
		}
		if screenWait(ctx, screenPoll, false) != nil {
			return
		}
	}
}
