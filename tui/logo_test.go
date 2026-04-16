package tui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestRenderLogo_iTerm2(t *testing.T) {
	os.Setenv("TERM_PROGRAM", "iTerm.app")
	os.Unsetenv("KITTY_PID")
	defer os.Unsetenv("TERM_PROGRAM")

	var buf bytes.Buffer
	data := []byte("fake-png-data")
	RenderLogo(&buf, data)

	output := buf.String()
	if !strings.Contains(output, "\033]1337;File=inline=1") {
		t.Errorf("expected iTerm2 escape sequence, got %q", output)
	}
}

func TestRenderLogo_Kitty(t *testing.T) {
	os.Unsetenv("TERM_PROGRAM")
	os.Setenv("KITTY_PID", "12345")
	defer os.Unsetenv("KITTY_PID")

	var buf bytes.Buffer
	data := []byte("fake-png-data")
	RenderLogo(&buf, data)

	output := buf.String()
	if !strings.Contains(output, "\033_G") {
		t.Errorf("expected Kitty escape sequence, got %q", output)
	}
}

func TestRenderLogo_NoProtocol(t *testing.T) {
	os.Setenv("TERM_PROGRAM", "Apple_Terminal")
	os.Unsetenv("KITTY_PID")
	os.Unsetenv("WEZTERM_EXECUTABLE")
	defer os.Unsetenv("TERM_PROGRAM")

	var buf bytes.Buffer
	RenderLogo(&buf, []byte("data"))
	if buf.Len() != 0 {
		t.Error("expected no output for unsupported terminal")
	}
}

func TestRenderLogo_EmptyData(t *testing.T) {
	os.Setenv("TERM_PROGRAM", "iTerm.app")
	defer os.Unsetenv("TERM_PROGRAM")

	var buf bytes.Buffer
	RenderLogo(&buf, nil)
	if buf.Len() != 0 {
		t.Error("expected no output for nil data")
	}
}

func TestFetchAndCacheLogo_Download(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("PNG-DATA"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.logo")

	data, err := FetchAndCacheLogo(srv.URL+"/logo.png", cachePath)
	if err != nil {
		t.Fatalf("FetchAndCacheLogo failed: %v", err)
	}
	if string(data) != "PNG-DATA" {
		t.Errorf("expected 'PNG-DATA', got %q", data)
	}

	// Verify cached to disk
	cached, _ := os.ReadFile(cachePath)
	if string(cached) != "PNG-DATA" {
		t.Error("expected file to be cached on disk")
	}
}

func TestFetchAndCacheLogo_UsesCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.logo")
	os.WriteFile(cachePath, []byte("CACHED"), 0o600)

	// Server should not be called if cache exists
	data, err := FetchAndCacheLogo("http://should-not-be-called.invalid/logo.png", cachePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "CACHED" {
		t.Errorf("expected cached data, got %q", data)
	}
}

func TestFetchAndCacheLogo_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cachePath := filepath.Join(dir, "test.logo")

	_, err := FetchAndCacheLogo(srv.URL+"/logo.png", cachePath)
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}
