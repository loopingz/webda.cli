package tui

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DetectImageProtocol returns "iterm2", "kitty", or "" based on terminal env vars.
func DetectImageProtocol() string {
	if strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "iterm") {
		return "iterm2"
	}
	if os.Getenv("KITTY_PID") != "" {
		return "kitty"
	}
	if os.Getenv("WEZTERM_EXECUTABLE") != "" {
		return "iterm2" // WezTerm supports iTerm2 protocol
	}
	return ""
}

// LogoCachePath returns the path where the logo is cached on disk.
func LogoCachePath(configDir, name string) string {
	return filepath.Join(configDir, name+".logo")
}

// FetchAndCacheLogo downloads the logo from url and caches it to disk.
// Returns the image bytes, or cached bytes if already present.
func FetchAndCacheLogo(url, cachePath string) ([]byte, error) {
	if data, err := os.ReadFile(cachePath); err == nil && len(data) > 0 {
		return data, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("logo fetch failed: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(cachePath, data, 0o600)
	return data, nil
}

// RenderLogo writes the logo to w using the appropriate terminal protocol.
// Does nothing if the terminal doesn't support inline images.
func RenderLogo(w io.Writer, data []byte) {
	protocol := DetectImageProtocol()
	if protocol == "" || len(data) == 0 {
		return
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	switch protocol {
	case "iterm2":
		_, _ = fmt.Fprintf(w, "\033]1337;File=inline=1;width=30;preserveAspectRatio=1:%s\a\n", b64)
	case "kitty":
		// Kitty protocol: send in chunks of 4096
		for i := 0; i < len(b64); i += 4096 {
			end := i + 4096
			if end > len(b64) {
				end = len(b64)
			}
			chunk := b64[i:end]
			more := 1
			if end >= len(b64) {
				more = 0
			}
			if i == 0 {
				_, _ = fmt.Fprintf(w, "\033_Ga=T,f=100,m=%d;%s\033\\", more, chunk)
			} else {
				_, _ = fmt.Fprintf(w, "\033_Gm=%d;%s\033\\", more, chunk)
			}
		}
		_, _ = fmt.Fprintln(w)
	}
}
