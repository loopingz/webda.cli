package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCamelToKebab(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"testOperations", "test-operations"},
		{"AuthorizerService", "authorizer-service"},
		{"MFA", "mfa"},
		{"SMS", "sms"},
		{"getHTTPResponse", "get-http-response"},
		{"already-kebab", "already-kebab"},
		{"simple", "simple"},
		{"ABCDef", "abc-def"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := camelToKebab(tt.in)
			if got != tt.want {
				t.Errorf("camelToKebab(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitOperationName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"two parts", "AuthorizerService.testOperations", []string{"authorizer-service", "test-operations"}},
		{"three parts", "Foo.Bar.Baz", []string{"foo", "bar", "baz"}},
		{"acronyms", "MFA.SMS", []string{"mfa", "sms"}},
		{"single", "Deploy", []string{"deploy"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitOperationName(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitOperationName(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitOperationName(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildCommandTree(t *testing.T) {
	ops := []Operation{
		{Name: "AuthorizerService.testOperations", Input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"user": map[string]any{"type": "string"},
			},
			"required": []any{"user"},
		}},
		{Name: "AuthorizerService.listUsers"},
		{Name: "MFA.SMS", Input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"phone":  map[string]any{"type": "string"},
				"dryRun": map[string]any{"type": "boolean"},
			},
		}},
		{Name: "Deploy"},
	}
	root := &cobra.Command{Use: "test"}
	buildCommandTree(root, nil, "", ops, nil)

	// Check top-level groups exist
	authCmd, _, err := root.Find([]string{"authorizer-service"})
	if err != nil || authCmd == nil || authCmd.Use != "authorizer-service" {
		t.Fatalf("expected authorizer-service command, got err=%v cmd=%v", err, authCmd)
	}

	// Check nested subcommand
	testOpsCmd, _, err := root.Find([]string{"authorizer-service", "test-operations"})
	if err != nil || testOpsCmd == nil {
		t.Fatalf("expected test-operations subcommand, got err=%v", err)
	}
	if testOpsCmd.RunE == nil {
		t.Fatal("expected test-operations to be runnable")
	}
	// Check flags from schema
	f := testOpsCmd.Flags().Lookup("user")
	if f == nil {
		t.Fatal("expected --user flag on test-operations")
	}

	// Check MFA group
	smsCmd, _, err := root.Find([]string{"mfa", "sms"})
	if err != nil || smsCmd == nil {
		t.Fatalf("expected mfa sms command, got err=%v", err)
	}
	if smsCmd.Flags().Lookup("phone") == nil {
		t.Fatal("expected --phone flag on mfa sms")
	}
	if smsCmd.Flags().Lookup("dry-run") == nil {
		t.Fatal("expected --dry-run flag on mfa sms")
	}

	// Check single-segment operation
	deployCmd, _, err := root.Find([]string{"deploy"})
	if err != nil || deployCmd == nil {
		t.Fatalf("expected deploy command, got err=%v", err)
	}

	// Check --generate-cli-skeleton and --input flags exist
	if testOpsCmd.Flags().Lookup("generate-cli-skeleton") == nil {
		t.Fatal("expected --generate-cli-skeleton flag")
	}
	if testOpsCmd.Flags().Lookup("input") == nil {
		t.Fatal("expected --input flag")
	}
}

func TestGenerateSkeleton(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user":   map[string]any{"type": "string"},
			"count":  map[string]any{"type": "integer"},
			"score":  map[string]any{"type": "number"},
			"active": map[string]any{"type": "boolean"},
			"role":   map[string]any{"type": "string", "enum": []any{"admin", "user"}},
			"tags":   map[string]any{"type": "array"},
			"meta":   map[string]any{"type": "object"},
		},
	}
	skeleton := generateSkeleton(schema)

	if skeleton["user"] != "" {
		t.Errorf("expected empty string for user, got %v", skeleton["user"])
	}
	if skeleton["count"] != 0 {
		t.Errorf("expected 0 for count, got %v", skeleton["count"])
	}
	if skeleton["score"] != 0.0 {
		t.Errorf("expected 0.0 for score, got %v", skeleton["score"])
	}
	if skeleton["active"] != false {
		t.Errorf("expected false for active, got %v", skeleton["active"])
	}
	if skeleton["role"] != "admin" {
		t.Errorf("expected first enum value 'admin' for role, got %v", skeleton["role"])
	}

	if skeleton["tags"] == nil {
		t.Error("expected empty slice for tags")
	}
	if skeleton["meta"] == nil {
		t.Error("expected empty map for meta")
	}
}

func TestGenerateSkeleton_Nil(t *testing.T) {
	skeleton := generateSkeleton(nil)
	if len(skeleton) != 0 {
		t.Errorf("expected empty skeleton for nil schema, got %v", skeleton)
	}
}

// --- hasMissingRequired tests ---

func TestHasMissingRequired_MissingField(t *testing.T) {
	schema := map[string]any{
		"required": []any{"user", "phone"},
	}
	body := map[string]any{"user": "alice"}
	if !hasMissingRequired(schema, body) {
		t.Error("expected true when required field is missing from body")
	}
}

func TestHasMissingRequired_AllPresent(t *testing.T) {
	schema := map[string]any{
		"required": []any{"user", "phone"},
	}
	body := map[string]any{"user": "alice", "phone": "123"}
	if hasMissingRequired(schema, body) {
		t.Error("expected false when all required fields are present")
	}
}

func TestHasMissingRequired_NoRequiredArray(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"user": map[string]any{"type": "string"},
		},
	}
	body := map[string]any{}
	if hasMissingRequired(schema, body) {
		t.Error("expected false when schema has no required array")
	}
}

func TestHasMissingRequired_NilSchema(t *testing.T) {
	// Should not panic and should return false
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("hasMissingRequired panicked with nil schema: %v", r)
		}
	}()
	// nil map access on "required" key returns nil interface, cast to []any fails → ok = false → returns false
	var schema map[string]any
	body := map[string]any{}
	if hasMissingRequired(schema, body) {
		t.Error("expected false for nil schema")
	}
}

// --- collectInput tests ---

func newTestCommand(op Operation) *cobra.Command {
	cmd := &cobra.Command{Use: "test", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	addSchemaFlags(cmd, op.Input)
	cmd.Flags().BoolP("interactive", "i", false, "")
	cmd.Flags().String("input", "", "")
	cmd.Flags().Bool("generate-cli-skeleton", false, "")
	cmd.Flags().StringP("output", "o", "pretty", "")
	return cmd
}

func TestCollectInput_FileValid(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "input*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	payload := map[string]any{"user": "from-file"}
	data, _ := json.Marshal(payload)
	if _, err := f.Write(data); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	f.Close()

	op := Operation{
		Input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"user": map[string]any{"type": "string"},
			},
		},
	}
	cmd := newTestCommand(op)
	if err := cmd.Flags().Set("input", f.Name()); err != nil {
		t.Fatalf("failed to set --input flag: %v", err)
	}

	body, err := collectInput(cmd, op, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["user"] != "from-file" {
		t.Errorf("expected user=from-file, got %v", body["user"])
	}
}

func TestCollectInput_FileNotFound(t *testing.T) {
	op := Operation{Input: map[string]any{"type": "object", "properties": map[string]any{}}}
	cmd := newTestCommand(op)
	if err := cmd.Flags().Set("input", "/nonexistent/path/input.json"); err != nil {
		t.Fatalf("failed to set --input flag: %v", err)
	}
	_, err := collectInput(cmd, op, nil)
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}

func TestCollectInput_FileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "input*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	f.WriteString("not valid json{{{")
	f.Close()

	op := Operation{Input: map[string]any{"type": "object", "properties": map[string]any{}}}
	cmd := newTestCommand(op)
	if err := cmd.Flags().Set("input", f.Name()); err != nil {
		t.Fatalf("failed to set --input flag: %v", err)
	}
	_, err = collectInput(cmd, op, nil)
	if err == nil {
		t.Error("expected error for invalid JSON file, got nil")
	}
}

func TestCollectInput_FlagOverridesFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "input*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	payload := map[string]any{"user": "from-file"}
	data, _ := json.Marshal(payload)
	f.Write(data)
	f.Close()

	op := Operation{
		Input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"user": map[string]any{"type": "string"},
			},
		},
	}
	cmd := newTestCommand(op)
	if err := cmd.Flags().Set("input", f.Name()); err != nil {
		t.Fatalf("failed to set --input flag: %v", err)
	}
	if err := cmd.Flags().Set("user", "from-flag"); err != nil {
		t.Fatalf("failed to set --user flag: %v", err)
	}

	body, err := collectInput(cmd, op, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if body["user"] != "from-flag" {
		t.Errorf("expected flag to override file: got user=%v, want from-flag", body["user"])
	}
}

func TestAddSchemaFlags_IntegerAndNumber(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count":   map[string]any{"type": "integer", "description": "item count"},
			"score":   map[string]any{"type": "number"},
			"enabled": map[string]any{"type": "boolean"},
		},
		"required": []any{"count"},
	}
	addSchemaFlags(cmd, schema)

	f := cmd.Flags().Lookup("count")
	if f == nil {
		t.Fatal("expected --count flag")
	}
	if !strings.Contains(f.Usage, "(required)") {
		t.Error("expected (required) in usage")
	}

	if cmd.Flags().Lookup("score") == nil {
		t.Fatal("expected --score flag")
	}
	if cmd.Flags().Lookup("enabled") == nil {
		t.Fatal("expected --enabled flag")
	}
}

func TestAddSchemaFlags_NilSchema(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	addSchemaFlags(cmd, nil)
	// Should not panic, no flags added
}

func TestAddSchemaFlags_NoProperties(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	addSchemaFlags(cmd, map[string]any{"type": "object"})
	// Should not panic, no flags added
}

func TestGenerateSkeleton_ObjectAndArray(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nested": map[string]any{"type": "object"},
			"items":  map[string]any{"type": "array"},
		},
	}
	skeleton := generateSkeleton(schema)
	if skeleton["nested"] == nil {
		t.Error("expected empty map for nested object")
	}
	if skeleton["items"] == nil {
		t.Error("expected empty slice for array")
	}
}
