package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/loopingz/webda.cli/webdaclient"
	"github.com/spf13/cobra"
)

func TestQueryRendering(t *testing.T) {
	data := []byte(`{"items":[{"name":"Ada","email":"a@example.com","team":{"name":"Engineering"}},{"name":"Bob","email":"b@example.com","team":{"name":"Sales"}}],"big":9007199254740993}`)
	for _, tc := range []struct {
		name, query, format, want string
		failure                   bool
	}{
		{"raw stream", ".items[].name", "raw", "Ada\nBob\n", false},
		{"JSON stream", ".items[].name", "json", "\"Ada\"\n\"Bob\"\n", false},
		{"precision", ".big", "raw", "9007199254740993\n", false},
		{"empty", "empty", "table", "", false},
		{"empty array", "[]", "table", "", false},
		{"null", ".missing", "raw", "null\n", false},
		{"mixed stream", "1, null, true", "pretty", "1\nnull\ntrue\n", false},
		{"scalar table", ".big", "table", "", true},
		{"partial failure", `.items[0], error("failure")`, "json", "", true},
		{"table projection", `.items[] | select(.team.name == "Engineering") | {name, email, team: .team.name}`, "table", "email", false},
		{"table array", `[.items[] | {name}]`, "table", "Bob", false},
		{"environment hidden", "env", "raw", "{}\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, err := compileQuery(tc.query, true)
			if err != nil {
				t.Fatal(err)
			}
			out, err := renderQuery(context.Background(), data, tc.format, code)
			if (err != nil) != tc.failure {
				t.Fatalf("output %q, error %v", out, err)
			}
			if tc.failure {
				if len(out) != 0 {
					t.Fatal("partial output on error")
				}
				return
			}
			if tc.format == "table" && tc.want != "" {
				if !strings.Contains(string(out), tc.want) || !strings.Contains(string(out), "Ada") {
					t.Fatalf("table: %s", out)
				}
			} else if string(out) != tc.want {
				t.Fatalf("got %q, want %q", out, tc.want)
			}
		})
	}
	code, _ := compileQuery(".", true)
	for _, invalid := range []string{"not JSON", "{} {}"} {
		if _, err := renderQuery(context.Background(), []byte(invalid), "json", code); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	code, _ = compileQuery("def forever: forever; forever", true)
	if _, err := renderQuery(ctx, data, "raw", code); err == nil {
		t.Fatal("query ignored cancellation")
	}
}

func TestQueryValidationBeforeOperation(t *testing.T) {
	for _, query := range []string{"", ".items[", "unknown_function", `import "secret" as secret; secret::value`} {
		cmd := newOperationCommand(Operation{Name: "Test.run", Destructive: true}, "run", nil, "", nil)
		_ = cmd.Flags().Set("query", query)
		if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "query") {
			t.Fatalf("%q: %v", query, err)
		}
	}
	if schemaFlagName("query") != "field-query" {
		t.Fatal("input query conflicts with built-in flag")
	}
}

func queryFixture(t *testing.T) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(`{"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/User"}},"total":{"type":"integer"},"a.b":{"type":"string"}},"$defs":{"User":{"properties":{"name":{"type":"string"},"email":{"type":"string"},"team":{"anyOf":[{"type":"null"},{"properties":{"name":{"type":"string"},"postal-code":{"type":"string"},"a\\b":{"type":"string"}}}]},"next":{"$ref":"#/$defs/User"}}}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestQueryCompletion(t *testing.T) {
	schema := queryFixture(t)
	for _, tc := range []struct{ prefix, want string }{
		{"", ".items"}, {".it", ".items"}, {".items[].em", ".items[].email"},
		{".items[0].te", ".items[0].team"}, {".items[].team.na", ".items[].team.name"},
		{".items[] | .em", ".items[] | .email"},
		{".items[] | ", ".items[] | .name"},
		{".items[] | .team | .na", ".items[] | .team | .name"},
		{".items[] | {na", ".items[] | {name"},
		{".items[] | {name, email: .em", ".items[] | {name, email: .email"},
		{".items[] | {team: .team.na", ".items[] | {team: .team.name"},
		{".a", `.["a.b"]`}, {`.["a`, `.["a.b"]`},
		{".items[].team.po", `.items[].team["postal-code"]`},
		{`.items[].team["postal-code"] | .`, ""},
		{".items[].next.next.na", ".items[].next.next.name"},
		{".items[] | map(.) | .na", ""}, {".items[] | select(.name) | .na", ""},
		{".unknown.na", ""}, {".items | .na", ""},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			results := completeQuery(schema, tc.prefix)
			found := false
			for _, result := range results {
				if strings.SplitN(result, "\t", 2)[0] == tc.want {
					found = true
				}
			}
			if tc.want == "" {
				if len(results) != 0 {
					t.Fatalf("unexpected completion: %v", results)
				}
			} else if !found {
				t.Fatalf("want %q in %v", tc.want, results)
			}
		})
	}
	if len(completeQuery(nil, ".")) != 0 {
		t.Fatal("missing schema")
	}
	cycle := map[string]any{"$ref": "#/self"}
	cycle["self"] = cycle
	if len(completeQuery(cycle, ".")) != 0 {
		t.Fatal("cyclic schema invented fields")
	}
}

func TestQueryCobraCompletion(t *testing.T) {
	// Exercise Cobra's protocol, including alias and a query containing spaces.
	for _, flag := range []string{"--query", "-q"} {
		root := &cobra.Command{Use: "app"}
		root.AddCommand(newOperationCommand(Operation{Name: "Test.run", Output: queryFixture(t)}, "run", nil, "", nil))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{"__complete", "run", flag, ".items[] | .em"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), ".items[] | .email\tstring") || !strings.Contains(out.String(), ":6") {
			t.Fatalf("completion protocol: %s", &out)
		}
	}
}

func TestOperationQueryIntegration(t *testing.T) {
	requests := 0
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/operations/Users.list" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"items":[{"name":"Ada","email":"a@example.com"}]}`))
	}))
	defer server.Close()
	client, err := webdaclient.New("test", server.URL, tokenstore.NewMachineStore(t.TempDir()), nil, "test", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, format, want string
		fail                bool
		status              int
	}{
		{`.items[] | {name, email}`, "table", "email  name\na@example.com  Ada", false, 200},
		{`.items[].name`, "raw", "Ada\n", false, 200},
		{`.items[0].name`, "json", "\"Ada\"\n", false, 200},
		{`.items[`, "raw", "", true, 200},
		{`error("failed")`, "raw", "", true, 200},
		{`error("query should not run")`, "raw", "", true, 500},
	} {
		status = tc.status
		cmd := newOperationCommand(Operation{Name: "Users.list"}, "list", client, server.URL, nil)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(""))
		cmd.SetArgs([]string{"-q", tc.query, "-o", tc.format})
		before := requests
		err := cmd.Execute()
		if (err != nil) != tc.fail {
			t.Fatalf("%q: %v", tc.query, err)
		}
		if tc.query == ".items[" && requests != before {
			t.Fatal("invalid query executed an operation")
		}
		if tc.fail {
			if output.Len() != 0 {
				t.Fatal("error printed partial output")
			}
			if status == 500 && strings.Contains(err.Error(), "query should not run") {
				t.Fatal("query evaluated an HTTP failure")
			}
			continue
		}
		if tc.format == "table" {
			if strings.Join(strings.Fields(output.String()), " ") != "email name a@example.com Ada" {
				t.Fatalf("table %q", output.String())
			}
		} else if output.String() != tc.want {
			t.Fatalf("got %q, want %q", output.String(), tc.want)
		}
	}
}
