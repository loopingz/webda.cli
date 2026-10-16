//go:build !unix && !windows

package filelock

import "os"

// tryLock is a no-op where no advisory lock is available; the in-process single-flight still applies.
func tryLock(*os.File) error { return nil }

func unlock(*os.File) error { return nil }
