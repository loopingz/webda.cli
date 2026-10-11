package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/loopingz/webda.cli/webdaclient"
	"github.com/spf13/cobra"
)

func TestExecutionFailureAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		slow   bool
	}{{"forbidden", 403, false}, {"validation", 422, false}, {"timeout", 200, true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.slow {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"message":"denied"}`))
			}))
			defer srv.Close()
			client, err := webdaclient.New("test", srv.URL, tokenstore.NewMachineStore(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, err = executeOperation(ctx, Operation{Name: "Test.run"}, client, srv.URL, nil)
			if err == nil {
				t.Fatal("failure must return an error")
			}
			if tc.slow && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected deadline error, got %v", err)
			}
		})
	}
}
func TestCLIInputAutomation(t *testing.T) {
	op := Operation{Name: "Test.run", Input: map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}, "items": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}}}
	command := func() *cobra.Command {
		cmd := &cobra.Command{Use: "test"}
		addSchemaFlags(cmd, op.Input)
		cmd.Flags().String("input", "", "")
		cmd.Flags().Bool("interactive", false, "")
		cmd.Flags().Bool("no-interactive", false, "")
		cmd.SetIn(strings.NewReader(""))
		cmd.SetErr(&bytes.Buffer{})
		return cmd
	}
	t.Run("missing flags fail without form", func(t *testing.T) {
		cmd := command()
		_, err := collectInput(cmd, op, nil)
		if err == nil || !strings.Contains(err.Error(), "--name") {
			t.Fatalf("expected actionable error: %v", err)
		}
	})
	t.Run("stdin plus flag override", func(t *testing.T) {
		cmd := command()
		cmd.SetIn(strings.NewReader(`{"name":"file","items":[1,2]}`))
		_ = cmd.Flags().Set("input", "-")
		_ = cmd.Flags().Set("name", "flag")
		body, err := collectInput(cmd, op, nil)
		if err != nil {
			t.Fatal(err)
		}
		if body["name"] != "flag" || len(body["items"].([]any)) != 2 {
			t.Fatalf("unexpected input: %#v", body)
		}
	})
	t.Run("array flag parsed as JSON", func(t *testing.T) {
		cmd := command()
		_ = cmd.Flags().Set("name", "alice")
		_ = cmd.Flags().Set("items", "[1,2]")
		body, err := collectInput(cmd, op, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := body["items"].([]any); !ok {
			t.Fatalf("array remained string: %#v", body)
		}
	})
	t.Run("null is rejected", func(t *testing.T) {
		cmd := command()
		cmd.SetIn(strings.NewReader(`null`))
		_ = cmd.Flags().Set("input", "-")
		if _, err := collectInput(cmd, op, nil); err == nil {
			t.Fatal("null accepted")
		}
	})
	t.Run("required defaults avoid prompt", func(t *testing.T) {
		op.Input["properties"].(map[string]any)["name"].(map[string]any)["default"] = "alice"
		cmd := command()
		body, err := collectInput(cmd, op, nil)
		if err != nil || body["name"] != "alice" {
			t.Fatalf("default not applied: %#v %v", body, err)
		}
	})
}
func TestRenderOutput(t *testing.T) {
	data := []byte(`[{"name":"alice","active":false},{"name":"bob","active":true}]`)
	output, err := renderOutput(data, "table")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output, []byte("alice")) || !bytes.Contains(output, []byte("false")) {
		t.Fatalf("invalid table: %s", output)
	}
	if _, err := renderOutput([]byte("oops"), "json"); err == nil {
		t.Fatal("non-JSON accepted")
	}
	if _, err := renderOutput(data, "bogus"); err == nil {
		t.Fatal("invalid format accepted")
	}
}

func TestBuiltinFlagCollision(t *testing.T) {
	op := Operation{Name: "Test.run", Input: map[string]any{"properties": map[string]any{"input": map[string]any{"type": "string"}, "output": map[string]any{"type": "string"}}}}
	root := &cobra.Command{Use: "myapp"}
	buildCommandTree(root, nil, "", []Operation{op}, nil)
	cmd, _, err := root.Find([]string{"test", "run"})
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Flags().Set("field-input", "data")
	_ = cmd.Flags().Set("field-output", "value")
	body, err := collectInput(cmd, op, nil)
	if err != nil {
		t.Fatal(err)
	}
	if body["input"] != "data" || body["output"] != "value" {
		t.Fatalf("schema flag values lost: %#v", body)
	}
}
func TestForcedInteractiveWithoutSchemaFailsInScripts(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Bool("interactive", true, "")
	cmd.SetIn(strings.NewReader(""))
	cmd.SetErr(&bytes.Buffer{})
	if _, err := collectInput(cmd, Operation{Name: "Test.run"}, nil); err == nil || !strings.Contains(err.Error(), "requires terminal") {
		t.Fatalf("expected terminal error, got %v", err)
	}
}

func TestDestructiveOperationRequiresConfirmation(t *testing.T) {
	for _, op := range []Operation{{Name: "Test.delete", Destructive: true}, {Name: "Test.delete", Raw: map[string]any{"destructive": true}}} {
		root := &cobra.Command{Use: "myapp"}
		buildCommandTree(root, nil, "", []Operation{op}, nil)
		cmd, _, err := root.Find([]string{"test", "delete"})
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Fatalf("unconfirmed destructive operation allowed: %v", err)
		}
	}
	ops, err := parseOperations([]byte(`[{"name":"Test.delete","destructive":true,"input":{"type":"object"}}]`))
	if err != nil || !isDestructive(ops[0]) || ops[0].Input == nil {
		t.Fatalf("array metadata lost: %#v %v", ops, err)
	}
}
