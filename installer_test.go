package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fixture release binary delegates to the real local setup command. This
// verifies installer arguments and configuration without an installed Go SDK.
func TestInstallerClientHelper(t *testing.T) {
	if os.Getenv("WEBDA_INSTALL_CLIENT_HELPER") != "1" {
		return
	}
	os.Args = flag.Args()
	if len(os.Args) == 0 {
		t.Fatal("missing installed command path")
	}
	if err := runCLI(); err != nil {
		t.Fatal(err)
	}
}

type installerFixture struct {
	dir, bin, home, script string
	env                    []string
}

func newInstallerFixture(t *testing.T, system, arch string) installerFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell installer supports macOS and Linux")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tools")
	home := filepath.Join(dir, "home")
	for _, path := range []string{bin, home} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("#!/bin/sh\nexec " + shellQuote(executable) + " -test.run=^TestInstallerClientHelper$ -- \"$0\" \"$@\"\n")
	if err := os.WriteFile(filepath.Join(dir, "binary"), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(binary))
	manifest := fmt.Sprintf("%s  webda-cli-linux-amd64\n%s  webda-cli-linux-arm64\n%s  webda-cli-darwin-amd64\n%s  webda-cli-darwin-arm64\n", hash, hash, hash, hash)
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTool := func(name, script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTool("uname", `#!/bin/sh
case "$1" in -s) printf '%s\n' "$WEBDA_INSTALL_TEST_OS" ;; -m) printf '%s\n' "$WEBDA_INSTALL_TEST_ARCH" ;; *) exit 1 ;; esac
`)
	writeTool("go", `#!/bin/sh
printf 'installer must not require Go\n' >&2
exit 99
`)
	writeTool("curl", `#!/bin/sh
set -eu
output=
url=
while [ "$#" -gt 0 ]; do
 case "$1" in
  --output) output=$2; shift 2 ;;
  https://*) url=$1; shift ;;
  *) shift ;;
 esac
done
printf '%s\n' "$url" >> "$WEBDA_INSTALL_FIXTURES/downloads"
case "$url" in
 https://github.com/loopingz/webda.cli/releases/latest)
  printf '%s' https://github.com/loopingz/webda.cli/releases/tag/v9.9.9 ;;
 https://github.com/loopingz/webda.cli/releases/download/*/checksums.txt)
  [ "${WEBDA_INSTALL_MISSING_CHECKSUMS:-0}" != 1 ] || exit 22
  cp "$WEBDA_INSTALL_FIXTURES/checksums.txt" "$output" ;;
 https://github.com/loopingz/webda.cli/releases/download/*/webda-cli-*)
  cp "$WEBDA_INSTALL_FIXTURES/binary" "$output" ;;
 *) exit 22 ;;
esac
`)
	script, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	return installerFixture{dir: dir, bin: bin, home: home, script: script, env: []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + dir, "SHELL=/bin/fish", "WEBDA_INSTALL_FIXTURES=" + dir, "WEBDA_INSTALL_TEST_OS=" + system, "WEBDA_INSTALL_TEST_ARCH=" + arch, "WEBDA_INSTALL_CLIENT_HELPER=1"}}
}
func (f installerFixture) run(args ...string) (string, error) {
	cmd := exec.Command("/bin/sh", append([]string{f.script}, args...)...)
	cmd.Env = append(os.Environ(), f.env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
func (f installerFixture) assertClean(t *testing.T) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(f.dir, "webda-cli-install.*"))
	if err != nil || len(files) > 0 {
		t.Fatalf("temporary downloads left behind: %v %v", files, err)
	}
}
func TestInstallerPlatformAndSetup(t *testing.T) {
	for _, tc := range []struct{ system, arch, asset string }{{"Linux", "x86_64", "webda-cli-linux-amd64"}, {"Linux", "aarch64", "webda-cli-linux-arm64"}, {"Darwin", "x86_64", "webda-cli-darwin-amd64"}, {"Darwin", "arm64", "webda-cli-darwin-arm64"}} {
		t.Run(tc.asset, func(t *testing.T) {
			f := newInstallerFixture(t, tc.system, tc.arch)
			out, err := f.run("--server", "https://app.example.com/api/", "--name", "myapp")
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, out)
			}
			binary := filepath.Join(f.home, ".local", "bin", "myapp")
			info, err := os.Stat(binary)
			if err != nil || info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("executable not installed: %v", err)
			}
			config, err := os.ReadFile(filepath.Join(f.home, ".webdacli", "config.yaml"))
			if err != nil || !strings.Contains(string(config), "myapp: https://app.example.com/api") {
				t.Fatalf("server not configured: %s %v", config, err)
			}
			if _, err := os.Stat(filepath.Join(f.home, ".config", "fish", "completions", "myapp.fish")); !os.IsNotExist(err) {
				t.Fatal("completion installed without opt-in")
			}
			log, _ := os.ReadFile(filepath.Join(f.dir, "downloads"))
			if !strings.Contains(string(log), "/v9.9.9/"+tc.asset) {
				t.Fatalf("incorrect platform or release selected: %s", log)
			}
			if !strings.Contains(out, "PATH") || !strings.Contains(out, "myapp explore") {
				t.Fatalf("missing next steps: %s", out)
			}
			f.assertClean(t)
		})
	}
}
func TestInstallerPinnedVersionAndCompletion(t *testing.T) {
	f := newInstallerFixture(t, "Linux", "amd64")
	destination := filepath.Join(f.dir, "bin with spaces and ' quote")
	out, err := f.run("--version", "1.2.3", "--install-dir", destination, "--server", "http://localhost:18080", "--install-completion")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(destination, "webda.cli")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".config", "fish", "completions", "webda.cli.fish")); err != nil {
		t.Fatalf("completion opt-in failed: %v", err)
	}
	log, _ := os.ReadFile(filepath.Join(f.dir, "downloads"))
	if strings.Contains(string(log), "/releases/latest") || !strings.Contains(string(log), "/v1.2.3/webda-cli-linux-amd64") {
		t.Fatalf("version not pinned: %s", log)
	}
	var exportCommand string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  export PATH=") {
			exportCommand = strings.TrimSpace(line)
			break
		}
	}
	if exportCommand == "" {
		t.Fatalf("missing PATH instruction: %s", out)
	}
	check := exec.Command("/bin/sh", "-c", exportCommand+"; command -v webda.cli")
	check.Env = append(os.Environ(), f.env...)
	found, err := check.CombinedOutput()
	canonicalDestination, canonicalErr := filepath.EvalSymlinks(destination)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || strings.TrimSpace(string(found)) != filepath.Join(canonicalDestination, "webda.cli") {
		t.Fatalf("PATH instruction failed: %s (%v)", found, err)
	}
	f.assertClean(t)
}
func TestInstallerRejectsBadDownloadWithoutReplacingBinary(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-checksums-%v", missing), func(t *testing.T) {
			f := newInstallerFixture(t, "Linux", "x86_64")
			destination := filepath.Join(f.dir, "bin")
			if err := os.MkdirAll(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(destination, "webda.cli")
			before := []byte("existing binary")
			if err := os.WriteFile(target, before, 0o755); err != nil {
				t.Fatal(err)
			}
			if missing {
				f.env = append(f.env, "WEBDA_INSTALL_MISSING_CHECKSUMS=1")
			} else {
				if err := os.WriteFile(filepath.Join(f.dir, "binary"), []byte("corrupt download"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.run("--install-dir", destination, "--server", "https://app.example.com")
			if err == nil {
				t.Fatal("bad download accepted")
			}
			if missing && !strings.Contains(out, "no downloadable checksums") {
				t.Fatalf("unhelpful error: %s", out)
			}
			if !missing && !strings.Contains(out, "checksum mismatch") {
				t.Fatalf("unhelpful error: %s", out)
			}
			after, readErr := os.ReadFile(target)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatal("existing binary changed")
			}
			if _, err := os.Stat(filepath.Join(f.home, ".webdacli")); !os.IsNotExist(err) {
				t.Fatal("server configured after download failure")
			}
			f.assertClean(t)
		})
	}
}
func TestInstallerRejectsInvalidOptionsBeforeDownloading(t *testing.T) {
	for _, args := range [][]string{{"--name", "../escape"}, {"--name", "update"}, {"--server", "ftp://example.com"}, {"--server", "https://user:password@example.com"}, {"--server", "https://example.com?secret=value"}, {"--server", "https://"}, {"--version", "../tag"}, {"--install-completion"}, {"--server"}, {"--unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newInstallerFixture(t, "Linux", "x86_64")
			out, err := f.run(args...)
			if err == nil || !strings.Contains(out, "Error:") {
				t.Fatalf("invalid options accepted: %s", out)
			}
			if _, err := os.Stat(filepath.Join(f.dir, "downloads")); !os.IsNotExist(err) {
				t.Fatal("invalid options caused downloads")
			}
		})
	}
}
func TestInstallerUnsupportedPlatformAndHelp(t *testing.T) {
	for _, tc := range []struct{ system, arch string }{{"FreeBSD", "amd64"}, {"Linux", "mips"}} {
		f := newInstallerFixture(t, tc.system, tc.arch)
		if out, err := f.run(); err == nil || !strings.Contains(out, "unsupported") {
			t.Fatalf("unsupported platform accepted: %s", out)
		}
	}
	f := newInstallerFixture(t, "Linux", "amd64")
	if out, err := f.run("--help"); err != nil || !strings.Contains(out, "--server") {
		t.Fatalf("help failed: %s %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "downloads")); !os.IsNotExist(err) {
		t.Fatal("help contacted network")
	}
}
func TestInstallerInstallOnlyAndSetupFailure(t *testing.T) {
	t.Run("install only", func(t *testing.T) {
		f := newInstallerFixture(t, "Darwin", "arm64")
		out, err := f.run()
		if err != nil {
			t.Fatalf("install failed: %s %v", out, err)
		}
		if _, err := os.Stat(filepath.Join(f.home, ".webdacli")); !os.IsNotExist(err) {
			t.Fatal("install-only wrote configuration")
		}
		if !strings.Contains(out, "webda.cli setup") {
			t.Fatalf("missing setup hint: %s", out)
		}
	})
	t.Run("setup failure preserves config", func(t *testing.T) {
		f := newInstallerFixture(t, "Linux", "amd64")
		dir := filepath.Join(f.home, ".webdacli")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "config.yaml")
		before := "broken: [yaml"
		if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := f.run("--server", "https://app.example.com")
		if err == nil || !strings.Contains(out, "binary installed, but server setup failed") {
			t.Fatalf("setup failure hidden: %s", out)
		}
		after, _ := os.ReadFile(path)
		if string(after) != before {
			t.Fatal("invalid configuration overwritten")
		}
	})
}
