package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectImageProtocol(t *testing.T) {
	tests := []struct {
		name     string
		envSetup func()
		want     string
	}{
		{"iterm2", func() {
			os.Setenv("TERM_PROGRAM", "iTerm.app")
			os.Unsetenv("KITTY_PID")
			os.Unsetenv("WEZTERM_EXECUTABLE")
		}, "iterm2"},
		{"kitty", func() {
			os.Unsetenv("TERM_PROGRAM")
			os.Setenv("KITTY_PID", "12345")
			os.Unsetenv("WEZTERM_EXECUTABLE")
		}, "kitty"},
		{"wezterm", func() {
			os.Unsetenv("TERM_PROGRAM")
			os.Unsetenv("KITTY_PID")
			os.Setenv("WEZTERM_EXECUTABLE", "/usr/bin/wezterm")
		}, "iterm2"},
		{"none", func() {
			os.Setenv("TERM_PROGRAM", "Apple_Terminal")
			os.Unsetenv("KITTY_PID")
			os.Unsetenv("WEZTERM_EXECUTABLE")
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.envSetup()
			got := DetectImageProtocol()
			if got != tt.want {
				t.Errorf("DetectImageProtocol() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogoCachePath(t *testing.T) {
	got := LogoCachePath("/home/user/.webdacli", "lc")
	want := filepath.Join("/home/user/.webdacli", "lc.logo")
	if got != want {
		t.Errorf("LogoCachePath() = %q, want %q", got, want)
	}
}
