package screenlock

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fake(t *testing.T, os string, outputs ...string) *int {
	t.Helper()
	n := 0
	oldRun, oldOS := run, goos
	t.Cleanup(func() { run, goos = oldRun, oldOS })
	goos = os
	run = func(context.Context, string, ...string) ([]byte, error) {
		out := outputs[min(n, len(outputs)-1)]
		n++
		if out == "error" {
			return nil, errors.New("boom")
		}
		return []byte(out), nil
	}
	return &n
}

func TestDarwin(t *testing.T) {
	fake(t, "darwin", "<key>IOConsoleLocked</key>\n\t<false/>")
	if locked, err := Locked(context.Background()); err != nil || locked {
		t.Fatal(locked, err)
	}
	fake(t, "darwin", "<key>IOConsoleLocked</key>\n\t<true/>")
	if locked, _ := Locked(context.Background()); !locked {
		t.Fatal("IOConsoleLocked true")
	}
	fake(t, "darwin", "<key>IOConsoleLocked</key><false/><dict><key>CGSSessionScreenIsLocked</key>\n<true/>")
	if locked, _ := Locked(context.Background()); !locked {
		t.Fatal("CGSSessionScreenIsLocked true")
	}
}

func TestLinuxAndUnsupported(t *testing.T) {
	fake(t, "linux", "yes\n")
	if locked, _ := Locked(context.Background()); !locked {
		t.Fatal("LockedHint yes")
	}
	fake(t, "windows", "")
	if err := Supported(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestWaitRetriesErrorsAndReturnsOnLock(t *testing.T) {
	n := fake(t, "linux", "no", "error", "no", "yes")
	if err := Wait(context.Background(), time.Millisecond, true); err != nil {
		t.Fatal(err)
	}
	if *n != 4 {
		t.Fatalf("%d polls", *n)
	}
	fake(t, "linux", "no")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := Wait(ctx, time.Millisecond, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestWaitForUnlocked(t *testing.T) {
	fake(t, "linux", "yes", "yes", "no")
	if err := Wait(context.Background(), time.Millisecond, false); err != nil {
		t.Fatal(err)
	}
}
