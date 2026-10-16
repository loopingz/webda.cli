package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/loopingz/webda.cli/tui"
	"github.com/loopingz/webda.cli/webdaclient"
)

func terminalInput(r io.Reader) bool  { f, ok := r.(*os.File); return ok && term.IsTerminal(f.Fd()) }
func terminalOutput(w io.Writer) bool { f, ok := w.(*os.File); return ok && term.IsTerminal(f.Fd()) }
func missingFields(schema map[string]any, body map[string]any) []string {
	var missing []string
	fields := tui.SchemaFields(schema)
	for i := range fields {
		field := &fields[i]
		if field.Required {
			if _, ok := body[field.Name]; !ok {
				missing = append(missing, "--"+schemaFlagName(field.Name))
			}
		}
	}
	return missing
}

func isDestructive(op Operation) bool {
	marked, _ := op.Raw["destructive"].(bool)
	return op.Destructive || marked
}

// executeOperation is shared by the command-line and interactive explorer.
func executeOperation(ctx context.Context, op Operation, client *webdaclient.Client, baseURL string, body map[string]any) ([]byte, error) {
	validated, err := tui.ValidateInput(op.Input, body)
	if err != nil {
		return nil, err
	}
	req, err := buildRequest(ctx, op, baseURL, validated)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", op.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read response: %w", op.Name, err)
	}
	if resp.StatusCode >= 300 {
		hint := ""
		switch resp.StatusCode {
		case 401:
			hint = "; run auth to sign in again"
		case 403:
			hint = "; check your account permissions"
			if scopeOp, ok := insufficientScope(rb); ok {
				if scopeOp == "" {
					scopeOp = op.Name
				}
				return nil, fmt.Errorf("%s: %s; this token does not cover %s; run %s auth --scope \"%s offline_access\"", op.Name, resp.Status, scopeOp, filepath.Base(os.Args[0]), scopeOp)
			}
		case 400, 422:
			hint = "; check operation inputs"
		}
		return nil, fmt.Errorf("%s: %s%s\n%s", op.Name, resp.Status, hint, ansi.Strip(strings.TrimSpace(string(rb))))
	}
	if len(rb) == 0 {
		rb = []byte(resp.Status + "\n")
	}
	return rb, nil
}

// insufficientScope reports whether a 403 body is an insufficient_scope error and the operation it names.
func insufficientScope(body []byte) (string, bool) {
	var e struct {
		Error     string `json:"error"`
		Operation string `json:"operation"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error != "insufficient_scope" {
		return "", false
	}
	return sanitizeTerminal(e.Operation), true
}

func validOutput(format string) bool {
	return format == "raw" || format == "pretty" || format == "json" || format == "table"
}
func renderOutput(data []byte, format string) ([]byte, error) {
	if !validOutput(format) {
		return nil, fmt.Errorf("unknown output format %q", format)
	}
	if format == "raw" {
		return data, nil
	}
	if format == "table" {
		return renderTable(data)
	}
	if !json.Valid(data) {
		if format == "json" {
			return nil, fmt.Errorf("server response is not JSON; use --output raw")
		}
		return data, nil
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
func renderTable(data []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("table output requires JSON: %w", err)
	}
	var out bytes.Buffer
	w := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
	switch v := value.(type) {
	case map[string]any:
		keys := sortedKeys(v)
		for _, key := range keys {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", key, cell(v[key]))
		}
	case []any:
		columns := map[string]any{}
		for _, item := range v {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("table output requires an object or array of objects")
			}
			for key := range row {
				columns[key] = nil
			}
		}
		keys := sortedKeys(columns)
		_, _ = fmt.Fprintln(w, strings.Join(keys, "\t"))
		for _, item := range v {
			row := item.(map[string]any)
			cells := make([]string, len(keys))
			for i, key := range keys {
				if val, ok := row[key]; ok {
					cells[i] = cell(val)
				}
			}
			_, _ = fmt.Fprintln(w, strings.Join(cells, "\t"))
		}
	default:
		return nil, fmt.Errorf("table output requires an object or array of objects")
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func cell(v any) string {
	if s, ok := v.(string); ok {
		return strings.NewReplacer("\n", "\\n", "\r", "\\r", "\t", " ", "\x1b", "").Replace(s)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
