//go:build windows

package main

import "os/exec"

// detach is a no-op: screen lock watching is not supported on Windows.
func detach(*exec.Cmd) {}
