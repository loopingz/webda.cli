package filelock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludesAnotherHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.lock")
	release, err := Lock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a held lock must not be taken twice, got %v", err)
	}
	acquired := make(chan func(), 1)
	go func() {
		r, err := Lock(context.Background(), path)
		if err != nil {
			t.Error(err)
		}
		acquired <- r
	}()
	release()
	select {
	case r := <-acquired:
		r()
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not handed over after release")
	}
}

func TestLockReportsOpenErrors(t *testing.T) {
	if _, err := Lock(context.Background(), filepath.Join(t.TempDir(), "missing", "p.lock")); err == nil {
		t.Fatal("want an error for a missing directory")
	}
}
