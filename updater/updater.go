package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	semver "github.com/Masterminds/semver/v3"
)

const defaultGitHubRepo = "loopingz/webda-cli"

// NeedsUpdate returns true if currentVersion does not satisfy the constraint.
// Returns false for dev builds or empty constraints.
func NeedsUpdate(currentVersion, constraint string) (bool, error) {
	if currentVersion == "dev" || constraint == "" {
		return false, nil
	}
	v, err := semver.NewVersion(currentVersion)
	if err != nil {
		return false, nil // unparseable version, skip check
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false, fmt.Errorf("invalid version constraint %q: %w", constraint, err)
	}
	return !c.Check(v), nil
}

// AssetName returns the expected release asset filename for the given OS and architecture.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("webda-cli-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// GHRelease is the GitHub API release shape (subset).
type GHRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []GHAsset `json:"assets"`
}

// GHAsset is a GitHub release asset.
type GHAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// FetchLatestRelease fetches the latest release info from GitHub (or a custom base URL).
// downloadURL should be like "https://github.com/loopingz/webda-cli/releases" or empty for default.
func FetchLatestRelease(downloadURL string) (*GHRelease, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", defaultGitHubRepo)
	if downloadURL != "" && !strings.Contains(downloadURL, "github.com/"+defaultGitHubRepo) {
		apiURL = strings.TrimRight(downloadURL, "/") + "/latest"
	}
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("release API returned %s", resp.Status)
	}
	var rel GHRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("cannot parse release: %w", err)
	}
	return &rel, nil
}

// FindAssetURL finds the download URL for the current platform in a release.
func FindAssetURL(rel *GHRelease) (string, string, error) {
	want := AssetName(runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.BrowserDownloadURL, rel.TagName, nil
		}
	}
	return "", "", fmt.Errorf("no asset %q found in release %s", want, rel.TagName)
}

// DownloadAndReplace downloads the binary from url and replaces the running executable.
func DownloadAndReplace(url string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("cannot resolve symlinks: %w", err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, "webda-cli-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("download write failed: %w", err)
	}
	_ = tmp.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("cannot replace binary: %w", err)
	}
	return nil
}

// SelfReExec replaces the current process with a new execution of the same binary and args.
func SelfReExec() error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(execPath, os.Args, os.Environ())
}

// Update performs the full update: fetch release, find asset, download, replace.
// Returns the new version tag on success.
func Update(downloadURL string) (string, error) {
	rel, err := FetchLatestRelease(downloadURL)
	if err != nil {
		return "", err
	}
	assetURL, tag, err := FindAssetURL(rel)
	if err != nil {
		return "", err
	}
	if err := DownloadAndReplace(assetURL); err != nil {
		return "", err
	}
	return tag, nil
}
