package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/presence"
	"github.com/loopingz/webda.cli/webdaclient"
)

func guardSeams(t *testing.T, presenceOK bool, screenErr error) *[]string {
	t.Helper()
	oldP, oldS, oldSpawn, oldPoll := presenceSupported, screenSupported, spawnLockWatcher, screenPoll
	oldLocked, oldWait := screenLocked, screenWait
	t.Cleanup(func() {
		presenceSupported, screenSupported, spawnLockWatcher, screenPoll = oldP, oldS, oldSpawn, oldPoll
		screenLocked, screenWait = oldLocked, oldWait
	})
	presenceSupported = func() bool { return presenceOK }
	screenSupported = func(context.Context) error { return screenErr }
	screenPoll = time.Millisecond
	var spawned []string
	spawnLockWatcher = func(profile, fp string) error {
		spawned = append(spawned, profile+" "+fp)
		return nil
	}
	return &spawned
}

func TestUnsupportedGuardsFailBeforePairing(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, _, du, endpoint, env, out := unlockFixture(t, f)
	guardSeams(t, false, errors.New("no logind"))
	if err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{Presence: true}, out); !errors.Is(err, presence.ErrUnsupported) {
		t.Fatal(err)
	}
	err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{ScreenLock: true}, out)
	if err == nil || !strings.Contains(err.Error(), "no logind") {
		t.Fatal(err)
	}
	if f.requested != nil {
		t.Fatal("no pairing may be requested when a guard cannot work")
	}
}

func TestUnlockGuardsAreStoredAndTheWatcherStarted(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	spawned := guardSeams(t, true, nil)
	if err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{Presence: true, ScreenLock: true}, out); err != nil {
		t.Fatal(err)
	}
	ti, _ := store.Load("p")
	if !ti.UnlockPresence {
		t.Fatal("presence requirement not stored")
	}
	if len(*spawned) != 1 || *spawned != nil && (*spawned)[0] != "p "+unlockFingerprint(ti.UnlockKey) {
		t.Fatalf("watcher %v", *spawned)
	}
	if strings.Contains((*spawned)[0], ti.UnlockKey) {
		t.Fatal("the watcher argument must not reveal the key")
	}
}

func TestWatcherFailureLocksAgain(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	guardSeams(t, true, nil)
	spawnLockWatcher = func(string, string) error { return errors.New("cannot fork") }
	err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{ScreenLock: true}, out)
	if err == nil || !strings.Contains(err.Error(), "locked again") {
		t.Fatal(err)
	}
	if ti, _ := store.Load("p"); ti.UnlockKey != "" || !f.locked {
		t.Fatal("an unlock without its watcher must be locked")
	}
}

func TestLockWatchLocksOnScreenLockAndStopsWhenReplaced(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, _, _ := unlockFixture(t, f)
	guardSeams(t, true, nil)
	if err := client.SetUnlock(webdaclient.Unlock{Header: unlockHeader, Key: "s.k"}); err != nil {
		t.Fatal(err)
	}
	polls := 0
	screenLocked = func(context.Context) (bool, error) {
		polls++
		return polls >= 3, nil
	}
	if err := runLockWatch(context.Background(), client, du, endpoint, unlockFingerprint("s.k")); err != nil {
		t.Fatal(err)
	}
	if ti, _ := store.Load("p"); ti.UnlockKey != "" || !f.locked {
		t.Fatal("the screen lock must lock the unlock")
	}

	// another unlock replaced the watched one: the watcher leaves it alone and stops
	f.locked = false
	if err := client.SetUnlock(webdaclient.Unlock{Header: unlockHeader, Key: "s.new"}); err != nil {
		t.Fatal(err)
	}
	screenLocked = func(context.Context) (bool, error) { return true, nil }
	if err := runLockWatch(context.Background(), client, du, endpoint, unlockFingerprint("s.k")); err != nil {
		t.Fatal(err)
	}
	if f.locked || !client.Unlocked() {
		t.Fatal("a replaced unlock must not be locked by the old watcher")
	}
}

func TestMCPWatchLocksEachScreenLock(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, _, du, endpoint, _, _ := unlockFixture(t, f)
	guardSeams(t, true, nil)
	_ = client.SetUnlock(webdaclient.Unlock{Header: unlockHeader, Key: "s.k"})
	ctx, cancel := context.WithCancel(context.Background())
	waits := 0
	screenWait = func(ctx context.Context, _ time.Duration, locked bool) error {
		waits++
		if waits > 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	var logs []string
	watchScreenForMCP(ctx, client, du, endpoint, func(m string) { logs = append(logs, m) })
	if !f.locked || client.Unlocked() || len(logs) != 1 || !strings.Contains(logs[0], "locked") {
		t.Fatalf("locked=%v logs=%v", f.locked, logs)
	}
}

func TestPresenceSealsToTheSecureEnclaveWhenAvailable(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	guardSeams(t, true, nil)
	oldAvail, oldSeal := enclaveAvailable, enclaveSeal
	t.Cleanup(func() { enclaveAvailable, enclaveSeal = oldAvail, oldSeal })
	var sealedPlain string
	enclaveAvailable = func() bool { return true }
	enclaveSeal = func(plain []byte) (string, error) {
		sealedPlain = string(plain)
		return "SEALED", nil
	}
	if err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{Presence: true}, out); err != nil {
		t.Fatal(err)
	}
	ti, _ := store.Load("p")
	if ti.UnlockKey != "" || ti.UnlockSealed != "SEALED" || !ti.UnlockPresence || sealedPlain != f.approved {
		t.Fatalf("stored %+v (sealed %q, approved %q)", ti, sealedPlain, f.approved)
	}
	if !strings.Contains(out.String(), "Secure Enclave") {
		t.Fatalf("output %q", out.String())
	}

	// a failed seal locks the fresh unlock again instead of storing it in clear
	enclaveSeal = func([]byte) (string, error) { return "", errors.New("enclave busy") }
	f.locked = false
	err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{Presence: true}, out)
	if err == nil || !strings.Contains(err.Error(), "locked again") || !f.locked {
		t.Fatalf("err %v locked %v", err, f.locked)
	}
}

func TestPresenceFallsBackToTheCLICheckWithoutEnclave(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	guardSeams(t, true, nil)
	old := enclaveAvailable
	t.Cleanup(func() { enclaveAvailable = old })
	enclaveAvailable = func() bool { return false }
	if err := runUnlock(context.Background(), env, client, du, endpoint, "p", unlockOptions{Presence: true}, out); err != nil {
		t.Fatal(err)
	}
	if ti, _ := store.Load("p"); ti.UnlockKey == "" || ti.UnlockSealed != "" || !ti.UnlockPresence {
		t.Fatalf("stored %+v", ti)
	}
	if !strings.Contains(out.String(), "No Secure Enclave") {
		t.Fatalf("output %q", out.String())
	}
}
