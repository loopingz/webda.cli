package main

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/loopingz/webda-cli/webdaclient"
	"github.com/spf13/cobra"
)

// camelToKebab converts camelCase or PascalCase strings to kebab-case.
// Consecutive uppercase letters are treated as an acronym (e.g., "HTTP" stays together).
func camelToKebab(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				if unicode.IsLower(prev) {
					b.WriteRune('-')
				} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					b.WriteRune('-')
				}
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitOperationName splits "Foo.Bar.Baz" into ["foo", "bar", "baz"],
// converting each segment from camelCase to kebab-case.
func splitOperationName(name string) []string {
	parts := strings.Split(name, ".")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = camelToKebab(p)
	}
	return out
}

// buildCommandTree creates nested cobra commands from operations.
// Operations are split by "." into command groups. Only leaf commands are runnable.
// logoData is passed through to operation execution for TUI display.
func buildCommandTree(root *cobra.Command, client *webdaclient.Client, baseURL string, ops []Operation, logoData []byte) {
	for _, op := range ops {
		o := op
		segments := splitOperationName(o.Name)
		parent := root
		// Create or find intermediate group commands
		for i := 0; i < len(segments)-1; i++ {
			seg := segments[i]
			var found *cobra.Command
			for _, c := range parent.Commands() {
				if c.Use == seg {
					found = c
					break
				}
			}
			if found == nil {
				found = &cobra.Command{Use: seg, Short: fmt.Sprintf("%s commands", seg)}
				parent.AddCommand(found)
			}
			parent = found
		}
		// Create leaf command
		leaf := segments[len(segments)-1]
		cmd := &cobra.Command{
			Use:   leaf,
			Short: o.Description,
			RunE:  makeOperationRunE(o, client, baseURL, logoData),
		}
		addSchemaFlags(cmd, o.Input)
		cmd.Flags().StringP("output", "o", "pretty", "output format: raw|pretty")
		cmd.Flags().BoolP("interactive", "i", false, "force interactive TUI form")
		parent.AddCommand(cmd)
	}
}

// addSchemaFlags adds CLI flags from a JSON schema's properties.
func addSchemaFlags(cmd *cobra.Command, schema map[string]any) {
	if schema == nil {
		return
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	required := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		flagName := camelToKebab(name)
		desc := ""
		if d, ok := prop["description"].(string); ok {
			desc = d
		}
		if required[name] {
			desc += " (required)"
		}
		typ, _ := prop["type"].(string)
		switch typ {
		case "boolean":
			cmd.Flags().Bool(flagName, false, desc)
		case "integer":
			cmd.Flags().Int(flagName, 0, desc)
		case "number":
			cmd.Flags().Float64(flagName, 0, desc)
		default:
			cmd.Flags().String(flagName, "", desc)
		}
	}
}

// makeOperationRunE creates the RunE function for a leaf operation command.
// Placeholder — will be filled in Task 4.
func makeOperationRunE(op Operation, client *webdaclient.Client, baseURL string, logoData []byte) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("operation %s: execution not yet implemented", op.Name)
	}
}
