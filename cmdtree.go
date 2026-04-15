package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
// Collects flag values, optionally shows TUI form, then POSTs to /operations/{id}.
func makeOperationRunE(op Operation, client *webdaclient.Client, baseURL string, logoData []byte) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		body, err := collectInput(cmd, op)
		if err != nil {
			return err
		}
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return err
		}
		var reader io.Reader
		if len(body) > 0 {
			reader = bytes.NewReader(jsonBody)
		}
		url := strings.TrimRight(baseURL, "/") + "/operations/" + op.Name
		req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, url, reader)
		if err != nil {
			return err
		}
		if reader != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		format, _ := cmd.Flags().GetString("output")
		if format == "pretty" && json.Valid(rb) {
			var out bytes.Buffer
			if err := json.Indent(&out, rb, "", "  "); err == nil {
				rb = out.Bytes()
			}
		}
		if resp.StatusCode >= 300 {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Status)
		}
		os.Stdout.Write(rb)
		if len(rb) == 0 {
			fmt.Println(resp.Status)
		}
		return nil
	}
}

// collectInput gathers input values from CLI flags based on the operation's input schema.
// Returns a map of property name → value for all flags that were explicitly set.
func collectInput(cmd *cobra.Command, op Operation) (map[string]any, error) {
	body := map[string]any{}
	if op.Input == nil {
		return body, nil
	}
	props, ok := op.Input["properties"].(map[string]any)
	if !ok {
		return body, nil
	}
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		flagName := camelToKebab(name)
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
		default:
			val, _ := cmd.Flags().GetString(flagName)
			body[name] = val
		}
	}
	return body, nil
}
