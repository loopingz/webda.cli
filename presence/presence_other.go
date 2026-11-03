//go:build !darwin || !cgo

package presence

func supported() bool { return false }

func check(string) error { return ErrUnsupported }
