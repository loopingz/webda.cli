// Package filelock provides an advisory, cross-process exclusive lock on a file.
package filelock

import (
	"context"
	"errors"
	"os"
	"time"
)

// pollInterval is the delay between two attempts to take a busy lock.
const pollInterval = 20 * time.Millisecond

// errBusy is returned by tryLock when another holder has the lock.
var errBusy = errors.New("filelock: busy")

// Lock opens (creating it 0600) path and takes an exclusive advisory lock on it, waiting until ctx is done.
// The returned function releases the lock; the file itself is left in place.
func Lock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := tryLock(f)
		if err == nil {
			return func() {
				_ = unlock(f)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, errBusy) {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
