package main

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/spf13/cobra"
)

// runSetup runs setup with args against a memStore token store in a temporary HOME.
func runSetup(t *testing.T, store *memStore, args ...string) {
	t.Helper()
	orig := newTokenStore
	newTokenStore = func(string) tokenstore.TokenStore { return store }
	t.Cleanup(func() { newTokenStore = orig })
	root := &cobra.Command{Use: "myapp", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	cfg, _ := loadConfig()
	addLocalCommands(root, "myapp", cfg)
	root.SetArgs(append([]string{"setup"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestSetupChangingTheURLForgetsThePreviousServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newMemStore()
	runSetup(t, store, "--name", "p", "--url", "https://old.example.com")
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "a", RefreshToken: "r"})
	for _, path := range []string{metadataCachePath("p"), opsPath("p")} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// same URL (trailing slash): nothing is forgotten
	runSetup(t, store, "--name", "p", "--url", "https://old.example.com/")
	if _, err := store.Load("p"); err != nil {
		t.Fatal("setup with the same URL must keep the sign-in")
	}
	runSetup(t, store, "--name", "p", "--url", "https://new.example.com")
	if _, err := store.Load("p"); err == nil {
		t.Fatal("the token of the previous server must be deleted")
	}
	for _, path := range []string{metadataCachePath("p"), opsPath("p")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed: %v", path, err)
		}
	}
}

func TestSetupKeepsClientIDAndScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newMemStore()
	runSetup(t, store, "--name", "p", "--url", "https://h.example.com", "--client-id", "my-cli", "--scope", "Task.* offline_access")
	runSetup(t, store, "--name", "p", "--url", "https://h.example.com")
	cfg, err := loadConfig()
	if err != nil || cfg.Profiles["p"].ClientID != "my-cli" || cfg.Profiles["p"].Scope != "Task.* offline_access" {
		t.Fatalf("%+v %v", cfg.Profiles["p"], err)
	}
	runSetup(t, store, "--name", "p", "--url", "https://h.example.com", "--scope", "A.b offline_access")
	cfg, _ = loadConfig()
	if cfg.Profiles["p"].ClientID != "my-cli" || cfg.Profiles["p"].Scope != "A.b offline_access" {
		t.Fatalf("%+v", cfg.Profiles["p"])
	}
}

func TestConfigFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configDir()+"/config.yaml", []byte("p: https://h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSetup(t, newMemStore(), "--name", "p", "--url", "https://h.example.com")
	st, err := os.Stat(configDir() + "/config.yaml")
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v %v", st.Mode(), err)
	}
	dir, err := os.Stat(configDir())
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode %v %v", dir.Mode(), err)
	}
}

func TestVersionShowsTheOperationsVersion(t *testing.T) {
	root := &cobra.Command{Use: "myapp"}
	var out bytes.Buffer
	root.SetOut(&out)
	root.AddCommand(newVersionCmd("https://h.example.com", &ServerInfo{OperationsVersion: "abc123"}))
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "operations version: abc123 (https://h.example.com)") || strings.Contains(out.String(), "server:") {
		t.Fatalf("output %q", out.String())
	}
}
