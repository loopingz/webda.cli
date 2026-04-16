package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOperations_NormalMap(t *testing.T) {
	body := []byte(`{
		"operations": {
			"Sync.AWS": {"id":"Sync.AWS"},
			"MFA.SMS": {
				"id":"MFA.SMS",
				"input":{"type":"object","properties":{"phone":{"type":"string"}},"required":["phone"]},
				"output":{"type":"object","properties":{"message":{"type":"string"}}}
			}
		}
	}`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	for _, o := range ops {
		if o.Name == "MFA.SMS" {
			if o.Input == nil {
				t.Fatal("expected Input to be set for MFA.SMS")
			}
			if o.Input["type"] != "object" {
				t.Fatalf("expected Input type=object, got %v", o.Input["type"])
			}
			if o.Output == nil {
				t.Fatal("expected Output to be set for MFA.SMS")
			}
		}
	}
}

func TestParseOperations_Array(t *testing.T) {
	body := []byte(`[ {"name":"OpA","method":"GET","path":"/a"}, {"name":"OpB"} ]`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
}

func TestParseOperations_Wrapper(t *testing.T) {
	body := []byte(`{"operations": [ {"name":"Op1"} ] }`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 || ops[0].Name != "Op1" {
		t.Fatalf("unexpected ops: %+v", ops)
	}
}

func TestParseOperations_OpenAPIPaths(t *testing.T) {
	body := []byte(`{"paths": {"/users/{id}": {"get": {"description":"get user"}}}}`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Method != "GET" || ops[0].Path != "/users/{id}" {
		t.Fatalf("unexpected op: %+v", ops[0])
	}
}

func TestParseOperations_Smoke(t *testing.T) {
	body := []byte(`{"operations": {"A": {"id":"A"}}}`)
	ops, err := parseOperations(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
}

// --- parseOperationsResponse tests ---

func TestParseOperationsResponse_WithLogo(t *testing.T) {
	body := []byte(`{"logo":"https://example.com/logo.png","operations":{"A":{"id":"A"}}}`)
	ops, logoURL, err := parseOperationsResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logoURL != "https://example.com/logo.png" {
		t.Errorf("expected logo URL, got %q", logoURL)
	}
	if len(ops) != 1 {
		t.Errorf("expected 1 op, got %d", len(ops))
	}
}

func TestParseOperationsResponse_WithoutLogo(t *testing.T) {
	body := []byte(`{"operations":{"B":{"id":"B"}}}`)
	ops, logoURL, err := parseOperationsResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logoURL != "" {
		t.Errorf("expected empty logo URL, got %q", logoURL)
	}
	if len(ops) != 1 {
		t.Errorf("expected 1 op, got %d", len(ops))
	}
}

func TestParseOperationsResponse_InvalidJSON(t *testing.T) {
	body := []byte(`{invalid json`)
	_, _, err := parseOperationsResponse(body)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestExtractToken_Valid(t *testing.T) {
	body := []byte(`{"refresh_token":"abc123","sequence":42}`)
	refresh, seq := extractToken(body)
	if refresh != "abc123" {
		t.Errorf("expected refresh 'abc123', got %q", refresh)
	}
	if seq != 42 {
		t.Errorf("expected seq 42, got %d", seq)
	}
}

func TestExtractToken_InvalidJSON(t *testing.T) {
	refresh, seq := extractToken([]byte("not json"))
	if refresh != "" || seq != 0 {
		t.Errorf("expected empty for invalid JSON, got %q %d", refresh, seq)
	}
}

func TestExtractToken_MissingFields(t *testing.T) {
	body := []byte(`{"other":"field"}`)
	refresh, seq := extractToken(body)
	if refresh != "" || seq != 0 {
		t.Errorf("expected empty for missing fields, got %q %d", refresh, seq)
	}
}

func TestDeriveOpName(t *testing.T) {
	tests := []struct {
		method, path, want string
	}{
		{"get", "/users/{id}", "getUsersId"},
		{"post", "/items", "postItems"},
		{"delete", "/users/{id}/posts/{postId}", "deleteUsersIdPostsPostId"},
		{"get", "/", "get"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := deriveOpName(tt.method, tt.path)
			if got != tt.want {
				t.Errorf("deriveOpName(%q, %q) = %q, want %q", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestUrlQueryEscape(t *testing.T) {
	got := urlQueryEscape("hello world")
	if got != "hello+world" {
		t.Errorf("expected 'hello+world', got %q", got)
	}
}

func TestMapKeys(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2}
	keys := mapKeys(m)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestBuildRootCommand(t *testing.T) {
	cmd := buildRootCommand("test", "https://example.com")
	if cmd.Use != "test" {
		t.Errorf("expected Use 'test', got %q", cmd.Use)
	}
}

func TestLoadConfig_Missing(t *testing.T) {
	// Temporarily override HOME to point to a dir without config
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", t.TempDir())
	defer os.Setenv("HOME", origHome)

	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".webdacli")
	os.MkdirAll(configDir, 0o700)
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("myapp: https://example.com\n"), 0o644)

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	if cfg["myapp"] != "https://example.com" {
		t.Errorf("expected myapp URL, got %v", cfg)
	}
}

func TestDetectShell_Zsh(t *testing.T) {
	orig := os.Getenv("SHELL")
	defer os.Setenv("SHELL", orig)

	os.Setenv("SHELL", "/bin/zsh")
	if got := detectShell(); got != "zsh" {
		t.Errorf("expected 'zsh', got %q", got)
	}
}

func TestDetectShell_Bash(t *testing.T) {
	orig := os.Getenv("SHELL")
	defer os.Setenv("SHELL", orig)

	os.Setenv("SHELL", "/usr/bin/bash")
	if got := detectShell(); got != "bash" {
		t.Errorf("expected 'bash', got %q", got)
	}
}

func TestDetectShell_Fish(t *testing.T) {
	orig := os.Getenv("SHELL")
	defer os.Setenv("SHELL", orig)

	os.Setenv("SHELL", "/usr/local/bin/fish")
	if got := detectShell(); got != "fish" {
		t.Errorf("expected 'fish', got %q", got)
	}
}

func TestDetectShell_Unknown(t *testing.T) {
	orig := os.Getenv("SHELL")
	defer os.Setenv("SHELL", orig)

	os.Setenv("SHELL", "/bin/csh")
	if got := detectShell(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDetectShell_Empty(t *testing.T) {
	orig := os.Getenv("SHELL")
	defer os.Setenv("SHELL", orig)

	os.Unsetenv("SHELL")
	if got := detectShell(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestAppendLineIfMissing_NewFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "testrc")

	err := appendLineIfMissing(f, "export FOO=bar", "FOO")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), "export FOO=bar") {
		t.Error("expected line to be appended")
	}
}

func TestAppendLineIfMissing_AlreadyPresent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "testrc")
	os.WriteFile(f, []byte("# existing\nsome FOO config\n"), 0o644)

	err := appendLineIfMissing(f, "export FOO=bar", "FOO")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(f)
	if strings.Contains(string(data), "export FOO=bar") {
		t.Error("should not append when marker already present")
	}
}

func TestOpsPath(t *testing.T) {
	// opsPath depends on configDir which depends on HOME
	orig := os.Getenv("HOME")
	os.Setenv("HOME", "/fakehome")
	defer os.Setenv("HOME", orig)

	got := opsPath("myapp")
	want := "/fakehome/.webdacli/myapp.operations"
	if got != want {
		t.Errorf("opsPath = %q, want %q", got, want)
	}
}

func TestInstallShellCompletion_Zsh(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origShell := os.Getenv("SHELL")
	os.Setenv("HOME", dir)
	os.Setenv("SHELL", "/bin/zsh")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("SHELL", origShell)
	}()

	// Create config dir
	os.MkdirAll(filepath.Join(dir, ".webdacli"), 0o700)

	root := buildRootCommand("testapp", "https://example.com")
	installShellCompletion(root, "testapp")

	// Check completion file was created
	compFile := filepath.Join(dir, ".webdacli", "completions", "_testapp")
	if _, err := os.Stat(compFile); err != nil {
		t.Fatalf("expected completion file: %v", err)
	}

	// Check marker was written
	marker := filepath.Join(dir, ".webdacli", "testapp.completion-installed")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected marker file: %v", err)
	}

	// Check .zshrc was updated
	zshrc, _ := os.ReadFile(filepath.Join(dir, ".zshrc"))
	if !strings.Contains(string(zshrc), "webdacli/completions") {
		t.Error("expected .zshrc to contain completions fpath")
	}
}

func TestInstallShellCompletion_Bash(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origShell := os.Getenv("SHELL")
	os.Setenv("HOME", dir)
	os.Setenv("SHELL", "/bin/bash")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("SHELL", origShell)
	}()

	os.MkdirAll(filepath.Join(dir, ".webdacli"), 0o700)

	root := buildRootCommand("testapp", "https://example.com")
	installShellCompletion(root, "testapp")

	compFile := filepath.Join(dir, ".webdacli", "completions", "testapp.bash")
	if _, err := os.Stat(compFile); err != nil {
		t.Fatalf("expected bash completion file: %v", err)
	}

	bashrc, _ := os.ReadFile(filepath.Join(dir, ".bashrc"))
	if !strings.Contains(string(bashrc), "webdacli/completions") {
		t.Error("expected .bashrc to contain source line")
	}
}

func TestInstallShellCompletion_Fish(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origShell := os.Getenv("SHELL")
	os.Setenv("HOME", dir)
	os.Setenv("SHELL", "/usr/local/bin/fish")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("SHELL", origShell)
	}()

	os.MkdirAll(filepath.Join(dir, ".webdacli"), 0o700)

	root := buildRootCommand("testapp", "https://example.com")
	installShellCompletion(root, "testapp")

	compFile := filepath.Join(dir, ".config", "fish", "completions", "testapp.fish")
	if _, err := os.Stat(compFile); err != nil {
		t.Fatalf("expected fish completion file: %v", err)
	}
}

func TestInstallShellCompletion_AlreadyInstalled(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origShell := os.Getenv("SHELL")
	os.Setenv("HOME", dir)
	os.Setenv("SHELL", "/bin/zsh")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("SHELL", origShell)
	}()

	configDir := filepath.Join(dir, ".webdacli")
	os.MkdirAll(configDir, 0o700)
	// Write marker to simulate already installed
	os.WriteFile(filepath.Join(configDir, "testapp.completion-installed"), []byte("zsh\n"), 0o600)

	root := buildRootCommand("testapp", "https://example.com")
	installShellCompletion(root, "testapp")

	// .zshrc should NOT exist (completion was skipped)
	if _, err := os.Stat(filepath.Join(dir, ".zshrc")); err == nil {
		t.Error("should not have written .zshrc when already installed")
	}
}

func TestInstallShellCompletion_UnknownShell(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	origShell := os.Getenv("SHELL")
	os.Setenv("HOME", dir)
	os.Setenv("SHELL", "/bin/csh")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("SHELL", origShell)
	}()

	os.MkdirAll(filepath.Join(dir, ".webdacli"), 0o700)

	root := buildRootCommand("testapp", "https://example.com")
	installShellCompletion(root, "testapp")

	// No marker should be written for unknown shell
	marker := filepath.Join(dir, ".webdacli", "testapp.completion-installed")
	if _, err := os.Stat(marker); err == nil {
		t.Error("should not write marker for unknown shell")
	}
}
