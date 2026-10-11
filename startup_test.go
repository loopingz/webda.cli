package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestLocalInvocation(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"version"}, {"--profile", "staging", "setup"}, {"--timeout", "1s", "completion", "zsh"}, {"sync", "aws", "--generate-cli-skeleton"}} {
		if !localInvocation(args) {
			t.Fatalf("expected local: %v", args)
		}
	}
	if localInvocation([]string{"--profile", "staging", "sync", "aws"}) {
		t.Fatal("operation classified as local")
	}
}
func TestSetupAndProfiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := &cobra.Command{Use: "myapp"}
	var out bytes.Buffer
	root.SetOut(&out)
	addLocalCommands(root, "myapp", nil)
	root.SetArgs([]string{"setup", "--name", "staging", "--url", "https://example.com"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil || cfg["staging"] != "https://example.com" {
		t.Fatalf("setup failed: %v %v", cfg, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatal("setup changed shell configuration without explicit choice")
	}
	root = &cobra.Command{Use: "myapp"}
	out.Reset()
	root.SetOut(&out)
	addLocalCommands(root, "myapp", cfg)
	root.SetArgs([]string{"profiles"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "staging") {
		t.Fatal("profile missing")
	}
}
func TestLocalStartupWithoutConfiguration(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	for _, args := range [][]string{{"version"}, {"--help"}, {"completion", "bash"}} {
		os.Args = append([]string{"myapp"}, args...)
		if err := runCLI(); err != nil {
			t.Fatalf("local startup %v: %v", args, err)
		}
	}
}

func TestCompletionInvocationIsLocal(t *testing.T) {
	if !localInvocation([]string{"__complete", "sync", ""}) {
		t.Fatal("shell completion must not trigger authentication")
	}
}
func TestRequestedTimeout(t *testing.T) {
	for _, args := range [][]string{{"--timeout", "0s"}, {"--timeout=-1s"}, {"--timeout", "bad"}, {"--timeout"}} {
		if _, err := requestedTimeout(args); err == nil {
			t.Fatalf("invalid timeout accepted: %v", args)
		}
	}
	timeout, err := requestedTimeout([]string{"--timeout=2s"})
	if err != nil || timeout.String() != "2s" {
		t.Fatalf("unexpected timeout: %v %v", timeout, err)
	}
}
