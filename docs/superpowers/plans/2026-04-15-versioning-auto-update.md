# Versioning & Auto-Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bake a version into the CLI binary, display server version, and auto-update when the server requires a newer CLI version.

**Architecture:** Build-time ldflags set version/commit/date. The `/operations` response is extended to return server version and CLI version constraints. An `updater` package handles semver checking, GitHub release downloads, binary replacement, and process re-exec.

**Tech Stack:** Go, `Masterminds/semver/v3`, GitHub Releases API, `syscall.Exec`

---

## File Structure

| File | Responsibility |
|---|---|
| `version.go` | `version`, `commit`, `buildDate` vars; `newVersionCmd` cobra command |
| `version_test.go` | Tests for version formatting |
| `updater/updater.go` | `CheckVersion`, `Update`, `SelfReExec` — semver check, download, replace, re-exec |
| `updater/updater_test.go` | Tests with httptest mock for GitHub API |
| `main.go` | Parse `version`/`cli` from operations response, wire updater + version command |
| `.github/workflows/release.yml` | Add ldflags to release build |

---

### Task 1: Version variables and version command

**Files:**
- Create: `version.go`
- Create: `version_test.go`

- [ ] **Step 1: Write failing tests**

Create `version_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestFormatVersion" -v ./...`
Expected: FAIL — `formatVersion` not defined

- [ ] **Step 3: Implement version.go**

Create `version.go`:

```go
package main

import "fmt"

// Set at build time via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// formatVersion returns the formatted version string.
func formatVersion(ver, comm, date string) string {
	return fmt.Sprintf("%s (commit: %s, built: %s)", ver, comm, date)
}
```

- [ ] **Step 4: Run tests**

Run: `/usr/local/go/bin/go test -run "TestFormatVersion" -v ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add version.go version_test.go
git commit -m "feat: add version variables and formatVersion helper"
```

---

### Task 2: Updater package — semver checking

**Files:**
- Create: `updater/updater.go`
- Create: `updater/updater_test.go`

- [ ] **Step 1: Add semver dependency**

Run: `/usr/local/go/bin/go get github.com/Masterminds/semver/v3@latest`

- [ ] **Step 2: Write failing tests**

Create `updater/updater_test.go`:

```go
package updater

import "testing"

func TestNeedsUpdate_Satisfied(t *testing.T) {
	needs, err := NeedsUpdate("1.2.3", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("1.2.3 satisfies >=1.0.0, should not need update")
	}
}

func TestNeedsUpdate_NotSatisfied(t *testing.T) {
	needs, err := NeedsUpdate("1.0.0", ">=1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("1.0.0 does not satisfy >=1.2.0, should need update")
	}
}

func TestNeedsUpdate_DevVersion(t *testing.T) {
	needs, err := NeedsUpdate("dev", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("dev version should skip update check")
	}
}

func TestNeedsUpdate_EmptyConstraint(t *testing.T) {
	needs, err := NeedsUpdate("1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("empty constraint should not need update")
	}
}

func TestNeedsUpdate_ComplexConstraint(t *testing.T) {
	needs, err := NeedsUpdate("2.0.0", ">=1.0.0, <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("2.0.0 does not satisfy >=1.0.0, <2.0.0")
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "webda-cli-linux-amd64"},
		{"darwin", "arm64", "webda-cli-darwin-arm64"},
		{"windows", "amd64", "webda-cli-windows-amd64.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := AssetName(tt.goos, tt.goarch)
			if got != tt.want {
				t.Errorf("AssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestNeedsUpdate|TestAssetName" -v ./updater/...`
Expected: FAIL — package doesn't exist

- [ ] **Step 4: Implement updater.go**

Create `updater/updater.go`:

```go
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

// ghRelease is the GitHub API release shape (subset).
type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// FetchLatestRelease fetches the latest release info from GitHub (or a custom base URL).
// downloadURL should be like "https://github.com/loopingz/webda-cli/releases" or empty for default.
func FetchLatestRelease(downloadURL string) (*ghRelease, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", defaultGitHubRepo)
	if downloadURL != "" && !strings.Contains(downloadURL, "github.com/"+defaultGitHubRepo) {
		// Custom URL — assume it's a direct URL to a JSON release endpoint
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
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("cannot parse release: %w", err)
	}
	return &rel, nil
}

// FindAssetURL finds the download URL for the current platform in a release.
func FindAssetURL(rel *ghRelease) (string, string, error) {
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

	// Write to temp file in same directory (ensures same filesystem for rename)
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, "webda-cli-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // clean up on failure

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("download write failed: %w", err)
	}
	_ = tmp.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}

	// Atomic replace
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
```

- [ ] **Step 5: Run tests**

Run: `/usr/local/go/bin/go test -run "TestNeedsUpdate|TestAssetName" -v ./updater/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add updater/updater.go updater/updater_test.go go.mod go.sum
git commit -m "feat: add updater package with semver checking and GitHub release download"
```

---

### Task 3: Updater tests with mock GitHub API

**Files:**
- Modify: `updater/updater_test.go`

- [ ] **Step 1: Add mock-based tests**

Append to `updater/updater_test.go`:

```go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
)

func TestFetchLatestRelease_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ghRelease{
			TagName: "v1.3.0",
			Assets: []ghAsset{
				{Name: "webda-cli-linux-amd64", BrowserDownloadURL: "https://example.com/linux"},
				{Name: "webda-cli-darwin-arm64", BrowserDownloadURL: "https://example.com/darwin"},
			},
		})
	}))
	defer srv.Close()

	rel, err := FetchLatestRelease(srv.URL + "/")
	if err != nil {
		t.Fatalf("FetchLatestRelease failed: %v", err)
	}
	if rel.TagName != "v1.3.0" {
		t.Errorf("expected tag v1.3.0, got %q", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("expected 2 assets, got %d", len(rel.Assets))
	}
}

func TestFetchLatestRelease_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchLatestRelease(srv.URL + "/")
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestFindAssetURL_Found(t *testing.T) {
	rel := &ghRelease{
		TagName: "v1.3.0",
		Assets: []ghAsset{
			{Name: AssetName("linux", "amd64"), BrowserDownloadURL: "https://example.com/linux-amd64"},
			{Name: AssetName("darwin", "arm64"), BrowserDownloadURL: "https://example.com/darwin-arm64"},
		},
	}
	url, tag, err := FindAssetURL(rel)
	if err != nil {
		t.Fatalf("FindAssetURL failed: %v", err)
	}
	if tag != "v1.3.0" {
		t.Errorf("expected tag v1.3.0, got %q", tag)
	}
	if url == "" {
		t.Error("expected non-empty URL")
	}
}

func TestFindAssetURL_NotFound(t *testing.T) {
	rel := &ghRelease{
		TagName: "v1.3.0",
		Assets:  []ghAsset{{Name: "webda-cli-plan9-mips", BrowserDownloadURL: "https://example.com/plan9"}},
	}
	_, _, err := FindAssetURL(rel)
	if err == nil {
		t.Fatal("expected error when asset not found")
	}
}

func TestDownloadAndReplace_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := DownloadAndReplace(srv.URL + "/binary")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}
```

- [ ] **Step 2: Run all updater tests**

Run: `/usr/local/go/bin/go test -v ./updater/...`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add updater/updater_test.go
git commit -m "test: add mock GitHub API tests for updater package"
```

---

### Task 4: Parse server version and CLI constraints from operations response

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update `parseOperationsResponse` to return server version and CLI info**

The current signature is:
```go
func parseOperationsResponse(body []byte) ([]Operation, string, error)
```

Create a new type and update the function. Add this type near the top of `main.go` (after `Operation`):

```go
// ServerInfo holds metadata from the operations response.
type ServerInfo struct {
	LogoURL         string
	ServerVersion   string
	CLIVersionRange string
	CLIDownloadURL  string
}
```

Replace `parseOperationsResponse`:

```go
func parseOperationsResponse(body []byte) ([]Operation, ServerInfo, error) {
	var info ServerInfo
	var gen map[string]any
	if err := json.Unmarshal(body, &gen); err == nil {
		info.LogoURL, _ = gen["logo"].(string)
		info.ServerVersion, _ = gen["version"].(string)
		if cli, ok := gen["cli"].(map[string]any); ok {
			info.CLIVersionRange, _ = cli["version_range"].(string)
			info.CLIDownloadURL, _ = cli["download_url"].(string)
		}
		ops, err := parseOperations(body)
		return ops, info, err
	}
	ops, err := parseOperations(body)
	return ops, info, err
}
```

- [ ] **Step 2: Update `fetchOperations` signature**

Change:
```go
func fetchOperations(ctx context.Context, name string) ([]Operation, string, error) {
```
To:
```go
func fetchOperations(ctx context.Context, name string) ([]Operation, ServerInfo, error) {
```

And update the error returns from `return nil, "", err` to `return nil, ServerInfo{}, err`.

- [ ] **Step 3: Update all callers in `main()`**

In main(), update the operations fetch block:

```go
	// Fetch operations for dynamic commands
	var logoData []byte
	var serverInfo ServerInfo
	if ops, info, err := fetchOperations(ctx, invoked); err == nil {
		serverInfo = info
		if info.LogoURL != "" {
			cachePath := tui.LogoCachePath(configDir(), invoked)
			logoData, _ = tui.FetchAndCacheLogo(info.LogoURL, cachePath)
		}
		buildCommandTree(root, cli, baseURL, ops, logoData)
	} else {
		fmt.Fprintf(os.Stderr, "Warning: cannot fetch operations: %v\n", err)
	}
```

Update the `refresh-operations` command:
```go
		ops, _, err := fetchOperations(cmd.Context(), invoked)
```

- [ ] **Step 4: Update tests**

In `main_test.go`, update `TestParseOperationsResponse_WithLogo` to check the new return type:

Find any test that calls `parseOperationsResponse` and update from `ops, logoURL, err` to `ops, info, err` and check `info.LogoURL` instead of `logoURL`.

Similarly update `TestFetchOperations_Success` to use `ops, info, err` and check `info.LogoURL`.

- [ ] **Step 5: Run tests and build**

Run: `/usr/local/go/bin/go vet ./... && /usr/local/go/bin/go test -count=1 ./... && /usr/local/go/bin/go build -o webda-cli .`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add main.go main_test.go testutil_test.go
git commit -m "feat: parse server version and CLI constraints from operations response"
```

---

### Task 5: Wire version command and updater into main.go

**Files:**
- Modify: `version.go`
- Modify: `main.go`

- [ ] **Step 1: Add version command builder to version.go**

Append to `version.go`:

```go
import (
	"fmt"

	"github.com/loopingz/webda-cli/webdaclient"
	"github.com/spf13/cobra"
)

func newVersionCmd(baseURL string, serverInfo *ServerInfo, client *webdaclient.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show CLI and server version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s version %s\n", cmd.Root().Use, formatVersion(version, commit, buildDate))
			if serverInfo != nil && serverInfo.ServerVersion != "" {
				fmt.Printf("server: %s (%s)\n", serverInfo.ServerVersion, baseURL)
			}
			return nil
		},
	}
}
```

Note: merge the import blocks — `version.go` should have a single import block with `"fmt"`, `"github.com/loopingz/webda-cli/webdaclient"`, and `"github.com/spf13/cobra"`.

- [ ] **Step 2: Add update command and auto-update check to main.go**

Add `"github.com/loopingz/webda-cli/updater"` to the import block in `main.go`.

After the operations fetch block and before `installShellCompletion`, add the auto-update check:

```go
	// Check if CLI version satisfies server requirement
	if needs, _ := updater.NeedsUpdate(version, serverInfo.CLIVersionRange); needs {
		updateMode := cfg["update"]
		if updateMode == "" {
			updateMode = "silent"
		}
		downloadURL := serverInfo.CLIDownloadURL
		switch updateMode {
		case "silent":
			if tag, err := updater.Update(downloadURL); err == nil {
				fmt.Fprintf(os.Stderr, "Updated to %s\n", tag)
				if err := updater.SelfReExec(); err != nil {
					fmt.Fprintf(os.Stderr, "Re-exec failed: %v. Please re-run the command.\n", err)
					os.Exit(0)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Warning: auto-update failed: %v\n", err)
			}
		case "prompt":
			fmt.Fprintf(os.Stderr, "Update available (requires %s). Update now? [Y/n] ", serverInfo.CLIVersionRange)
			var answer string
			fmt.Scanln(&answer)
			if answer == "" || strings.ToLower(answer) == "y" || strings.ToLower(answer) == "yes" {
				if tag, err := updater.Update(downloadURL); err == nil {
					fmt.Fprintf(os.Stderr, "Updated to %s\n", tag)
					if err := updater.SelfReExec(); err != nil {
						fmt.Fprintf(os.Stderr, "Re-exec failed: %v. Please re-run the command.\n", err)
						os.Exit(0)
					}
				} else {
					fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
				}
			}
		case "warn":
			fmt.Fprintf(os.Stderr, "Warning: CLI version %s is outdated (server requires %s). Run '%s update' to upgrade.\n", version, serverInfo.CLIVersionRange, invoked)
		}
	}
```

Add the `version` and `update` commands after the existing command registrations:

```go
	root.AddCommand(newVersionCmd(baseURL, &serverInfo, cli))
	root.AddCommand(&cobra.Command{Use: "update", Short: "Update CLI to latest version", RunE: func(cmd *cobra.Command, args []string) error {
		downloadURL := serverInfo.CLIDownloadURL
		tag, err := updater.Update(downloadURL)
		if err != nil {
			return err
		}
		fmt.Printf("Updated to %s\n", tag)
		return nil
	}})
```

- [ ] **Step 3: Run tests and build**

Run: `/usr/local/go/bin/go vet ./... && /usr/local/go/bin/go test -count=1 ./... && /usr/local/go/bin/go build -o webda-cli .`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add version.go main.go
git commit -m "feat: add version/update commands and auto-update check"
```

---

### Task 6: Update release workflow with ldflags

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Update the build step to include ldflags**

Replace the build step in `.github/workflows/release.yml`:

```yaml
      - name: Build binaries
        run: |
          TAG="${{ needs.release-please.outputs.tag_name }}"
          VERSION="${TAG#v}"
          COMMIT="$(git rev-parse --short HEAD)"
          DATE="$(date -u +%Y-%m-%d)"
          LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${DATE}"
          for GOOS in linux darwin windows; do
            for GOARCH in amd64 arm64; do
              ext=""
              if [ "$GOOS" = "windows" ]; then ext=".exe"; fi
              GOOS=$GOOS GOARCH=$GOARCH go build -ldflags="${LDFLAGS}" -o "webda-cli-${GOOS}-${GOARCH}${ext}" .
            done
          done
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: add version ldflags to release build"
```

---

### Task 7: Add version tests for command and update integration

**Files:**
- Modify: `version_test.go`
- Modify: `main_test.go`

- [ ] **Step 1: Add version command test**

Append to `version_test.go`:

```go
func TestNewVersionCmd_LocalOnly(t *testing.T) {
	info := ServerInfo{}
	cmd := newVersionCmd("https://example.com", &info, nil)
	if cmd.Use != "version" {
		t.Errorf("expected Use 'version', got %q", cmd.Use)
	}
}

func TestNewVersionCmd_WithServerVersion(t *testing.T) {
	info := ServerInfo{ServerVersion: "2.1.0"}
	cmd := newVersionCmd("https://example.com", &info, nil)
	if cmd.Use != "version" {
		t.Errorf("expected Use 'version', got %q", cmd.Use)
	}
}
```

- [ ] **Step 2: Add tests for ServerInfo parsing**

Append to `main_test.go`:

```go
func TestParseOperationsResponse_WithServerInfo(t *testing.T) {
	body := []byte(`{
		"operations": {"TestOp": {"id": "TestOp"}},
		"version": "2.1.0",
		"logo": "https://example.com/logo.png",
		"cli": {
			"version_range": ">=1.2.0",
			"download_url": "https://example.com/releases"
		}
	}`)
	ops, info, err := parseOperationsResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if info.ServerVersion != "2.1.0" {
		t.Errorf("expected server version 2.1.0, got %q", info.ServerVersion)
	}
	if info.LogoURL != "https://example.com/logo.png" {
		t.Errorf("expected logo URL, got %q", info.LogoURL)
	}
	if info.CLIVersionRange != ">=1.2.0" {
		t.Errorf("expected version range >=1.2.0, got %q", info.CLIVersionRange)
	}
	if info.CLIDownloadURL != "https://example.com/releases" {
		t.Errorf("expected download URL, got %q", info.CLIDownloadURL)
	}
}

func TestParseOperationsResponse_NoCLIField(t *testing.T) {
	body := []byte(`{"operations": {"TestOp": {"id": "TestOp"}}}`)
	_, info, err := parseOperationsResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.CLIVersionRange != "" {
		t.Errorf("expected empty version range, got %q", info.CLIVersionRange)
	}
}
```

- [ ] **Step 3: Run all tests**

Run: `/usr/local/go/bin/go test -count=1 -v ./...`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add version_test.go main_test.go
git commit -m "test: add version command and ServerInfo parsing tests"
```

---

### Task 8: Final verification

- [ ] **Step 1: Run full test suite with race detector**

Run: `/usr/local/go/bin/go test -count=1 -race -v ./...`

- [ ] **Step 2: Run vet and lint**

Run: `/usr/local/go/bin/go vet ./...`
Run: `PATH="/usr/local/go/bin:$PATH" ~/go/bin/golangci-lint run --timeout=5m` (if available)

- [ ] **Step 3: Build with ldflags to verify they work**

Run: `/usr/local/go/bin/go build -ldflags="-X main.version=0.1.0 -X main.commit=test123 -X main.buildDate=2026-04-15" -o webda-cli .`

Then: `./webda-cli version` (will fail on config but verifies the binary embeds the version)

- [ ] **Step 4: Verify release workflow**

Check `.github/workflows/release.yml` has the ldflags in the build step.
