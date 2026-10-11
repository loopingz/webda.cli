package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/loopingz/webda-cli/tui"
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
			Args:  cobra.NoArgs,
			RunE:  makeOperationRunE(o, client, baseURL, logoData),
		}
		addSchemaFlags(cmd, o.Input)
		cmd.Flags().StringP("output", "o", "pretty", "output format: json|table|raw|pretty")
		cmd.Flags().BoolP("interactive", "i", false, "force interactive TUI form")
		cmd.Flags().Bool("no-interactive", false, "never prompt; fail on missing input")
		cmd.Flags().Bool("yes", false, "confirm an operation marked destructive")
		cmd.Flags().Bool("accessible", false, "use plain accessible prompts")
		cmd.Flags().String("input", "", "read JSON from a file or - for stdin")
		cmd.Flags().Bool("generate-cli-skeleton", false, "print a JSON skeleton for the input schema and exit")
		parent.AddCommand(cmd)
	}
}

// schemaFlagName keeps server field names from shadowing built-in flags.
func schemaFlagName(name string) string {
	flag := camelToKebab(name)
	switch flag {
	case "input", "output", "interactive", "no-interactive", "accessible", "yes", "generate-cli-skeleton", "profile", "timeout", "help":
		return "field-" + flag
	}
	return flag
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
		flagName := schemaFlagName(name)
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

// generateSkeleton builds a JSON skeleton from a JSON schema's properties.
func generateSkeleton(schema map[string]any) map[string]any {
	skeleton := map[string]any{}
	if schema == nil {
		return skeleton
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return skeleton
	}
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if value, ok := prop["default"]; ok {
			skeleton[name] = value
			continue
		}
		typ, _ := prop["type"].(string)
		switch typ {
		case "boolean":
			skeleton[name] = false
		case "integer":
			skeleton[name] = 0
		case "number":
			skeleton[name] = 0.0
		case "array":
			skeleton[name] = []any{}
		case "object":
			skeleton[name] = generateSkeleton(prop)
		default:
			// Check for enum — use first value as example
			if enums, ok := prop["enum"].([]any); ok && len(enums) > 0 {
				skeleton[name] = enums[0]
			} else {
				skeleton[name] = ""
			}
		}
	}
	return skeleton
}

// makeOperationRunE creates the RunE function for a leaf operation command.
// Collects flag values, optionally shows TUI form, then POSTs to /operations/{id}.
func makeOperationRunE(op Operation, client *webdaclient.Client, baseURL string, logoData []byte) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		// Handle --generate-cli-skeleton: print and exit
		if genSkeleton, _ := cmd.Flags().GetBool("generate-cli-skeleton"); genSkeleton {
			skeleton := generateSkeleton(op.Input)
			b, _ := json.MarshalIndent(skeleton, "", "  ")
			_, err := fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return err
		}

		body, err := collectInput(cmd, op, logoData)
		if err != nil {
			return err
		}
		if interactive, _ := cmd.Flags().GetBool("interactive"); interactive || cmd.Annotations["prompted"] == "true" {
			accessible, _ := cmd.Flags().GetBool("accessible")
			accepted, err := reviewOperation(cmd, op, client.Name, baseURL, body, accessible)
			if err != nil {
				return err
			}
			if !accepted {
				return fmt.Errorf("operation canceled")
			}
		} else if isDestructive(op) {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return fmt.Errorf("destructive operation; review with --interactive or explicitly confirm with --yes")
			}
		}
		format, _ := cmd.Flags().GetString("output")
		if !validOutput(format) {
			return fmt.Errorf("unknown output format %q; choose json, table, raw, or pretty", format)
		}
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		rb, err := executeOperation(ctx, op, client, baseURL, body)
		if err != nil {
			return err
		}
		rendered, err := renderOutput(rb, format)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(rendered)
		return err
	}
}

// collectInput gathers input from a JSON file (--input), CLI flags, or TUI form.
// Priority: file input is loaded first, then flags override, then TUI fills gaps.
func collectInput(cmd *cobra.Command, op Operation, logoData []byte) (map[string]any, error) {
	body := map[string]any{}
	interactive, _ := cmd.Flags().GetBool("interactive")
	disabled, _ := cmd.Flags().GetBool("no-interactive")
	if interactive && disabled {
		return nil, fmt.Errorf("--interactive and --no-interactive cannot be combined")
	}
	if interactive && (!terminalInput(cmd.InOrStdin()) || !terminalOutput(cmd.ErrOrStderr())) {
		return nil, fmt.Errorf("--interactive requires terminal stdin and stderr; supply flags or --input without --interactive")
	}

	// Load from --input file if provided
	if inputFile, _ := cmd.Flags().GetString("input"); inputFile != "" {
		var data []byte
		var err error
		if inputFile == "-" {
			data, err = io.ReadAll(cmd.InOrStdin())
		} else {
			data, err = os.ReadFile(inputFile)
		}
		if err != nil {
			return nil, fmt.Errorf("cannot read input file: %w", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return nil, fmt.Errorf("invalid JSON in input file: %w", err)
		}
	}

	if body == nil {
		return nil, fmt.Errorf("input must be a JSON object")
	}
	if op.Input == nil {
		return body, nil
	}
	props, ok := op.Input["properties"].(map[string]any)
	if !ok {
		return body, nil
	}

	// Collect values from flags (override file values)
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		flagName := schemaFlagName(name)
		f := cmd.Flags().Lookup(flagName)
		if f == nil || !f.Changed {
			continue
		}
		typ, _ := prop["type"].(string)
		switch typ {
		case "boolean":
			val, _ := cmd.Flags().GetBool(flagName)
			body[name] = val
		case "integer":
			val, _ := cmd.Flags().GetInt(flagName)
			body[name] = val
		case "number":
			val, _ := cmd.Flags().GetFloat64(flagName)
			body[name] = val
		case "object", "array":
			val, _ := cmd.Flags().GetString(flagName)
			var decoded any
			if err := json.Unmarshal([]byte(val), &decoded); err != nil {
				return nil, fmt.Errorf("--%s must contain valid JSON: %w", flagName, err)
			}
			body[name] = decoded
		default:
			val, _ := cmd.Flags().GetString(flagName)
			body[name] = val
		}
	}

	for key, raw := range props {
		prop, _ := raw.(map[string]any)
		if _, exists := body[key]; !exists {
			if value, ok := prop["default"]; ok {
				body[key] = value
			}
		}
	}
	if interactive || hasMissingRequired(op.Input, body) {
		if disabled || !terminalInput(cmd.InOrStdin()) || !terminalOutput(cmd.ErrOrStderr()) {
			return nil, fmt.Errorf("input requires a terminal; supply required flags or --input (missing: %s)", strings.Join(missingFields(op.Input, body), ", "))
		}
		// Show logo above the form if available
		tui.RenderLogo(cmd.ErrOrStderr(), logoData)
		fields := tui.SchemaFields(op.Input)
		form, results := tui.BuildForm(fields, body)
		if form != nil {
			accessible, _ := cmd.Flags().GetBool("accessible")
			if err := form.WithOutput(cmd.ErrOrStderr()).WithAccessible(accessible).RunWithContext(cmd.Context()); err != nil {
				return nil, err
			}
			if cmd.Annotations == nil {
				cmd.Annotations = map[string]string{}
			}
			cmd.Annotations["prompted"] = "true"
			normalized, err := tui.NormalizeForm(fields, body, results)
			body = normalized
			if err != nil {
				return nil, err
			}
		}
	}

	return tui.ValidateInput(op.Input, body)
}

// hasMissingRequired checks if any required fields from the schema are missing from body.
func hasMissingRequired(schema map[string]any, body map[string]any) bool {
	req, ok := schema["required"].([]any)
	if !ok {
		return false
	}
	for _, r := range req {
		name, ok := r.(string)
		if !ok {
			continue
		}
		if _, exists := body[name]; !exists {
			return true
		}
	}
	return false
}
