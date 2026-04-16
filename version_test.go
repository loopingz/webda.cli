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

func TestNewVersionCmd_LocalOnly(t *testing.T) {
	info := ServerInfo{}
	cmd := newVersionCmd("https://example.com", &info)
	if cmd.Use != "version" {
		t.Errorf("expected Use 'version', got %q", cmd.Use)
	}
}

func TestNewVersionCmd_WithServerVersion(t *testing.T) {
	info := ServerInfo{ServerVersion: "2.1.0"}
	cmd := newVersionCmd("https://example.com", &info)
	if cmd.Use != "version" {
		t.Errorf("expected Use 'version', got %q", cmd.Use)
	}
}
