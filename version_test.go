package main

import "testing"

func TestFormatVersion_Full(t *testing.T) {
	got := formatVersion("1.2.3", "abc1234", "2026-04-15")
	want := "1.2.3 (commit: abc1234, built: 2026-04-15)"
	if got != want {
		t.Errorf("formatVersion = %q, want %q", got, want)
	}
}

func TestFormatVersion_Dev(t *testing.T) {
	got := formatVersion("dev", "unknown", "unknown")
	want := "dev (commit: unknown, built: unknown)"
	if got != want {
		t.Errorf("formatVersion = %q, want %q", got, want)
	}
}
