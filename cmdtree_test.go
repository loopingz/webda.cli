package main

import (
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
