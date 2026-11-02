// Package presence asks the person at the computer to prove they are there (Touch ID or the account password)
// before a secret is used.
package presence

import "errors"

// ErrUnsupported is returned where no user presence check exists (non-macOS, or a build without cgo).
var ErrUnsupported = errors.New("user presence checks are only supported on macOS (Touch ID or the account password)")

// ErrDenied is returned when the person cancels or fails the check.
var ErrDenied = errors.New("user presence was not confirmed")

// Supported reports whether Check can prompt on this build and machine.
func Supported() bool { return supported() }

// Check prompts for Touch ID (or the account password) with reason; nil once confirmed.
func Check(reason string) error { return check(reason) }
