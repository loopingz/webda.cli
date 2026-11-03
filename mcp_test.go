package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/loopingz/webda.cli/webdaclient"
)

// mcpFixture is a server answering operations, and the MCP server over it.
type mcpFixture struct {
	mu     sync.Mutex
	calls  []string // "<operation> <body> <unlock header>"
	server *httptest.Server
	client *webdaclient.Client
	ops    []Operation
}

func newMCPFixture(t *testing.T, unlock string) *mcpFixture {
	t.Helper()
	f := &mcpFixture{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		data, _ := json.Marshal(body)
		f.mu.Lock()
		f.calls = append(f.calls, strings.TrimPrefix(r.URL.Path, "/operations/")+" "+string(data)+" "+r.Header.Get("X-Example-Key"))
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer at" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/operations/Forge.GetIssue":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"number":1,"title":"Bug"}`))
		case "/operations/Forge.CloseIssue":
			http.Error(w, `{"error":{"message":"Vault is locked"}}`, http.StatusLocked)
		case "/operations/Task.Text":
			_, _ = w.Write([]byte("plain text"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "at", RefreshToken: "rt", Server: f.server.URL, UnlockHeader: "X-Example-Key", UnlockKey: unlock})
	client, err := webdaclient.New("p", f.server.URL, store, nil, "webda-cli", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.client = client
	issue := map[string]any{"type": "object", "properties": map[string]any{"number": map[string]any{"type": "number"}}, "required": []any{"number"}}
	f.ops = []Operation{
		{Name: "Forge.GetIssue", Summary: "Read an issue", Input: issue},
		{Name: "Forge.CloseIssue", Input: issue, Destructive: true},
		{Name: "Task.Text"},
		{Name: "Vault.Request"},
		{Name: "Vault.Status"},
		{Name: "Vault.Lock"},
	}
	return f
}

// run sends the lines to a server selecting `include` and returns its answers by id.
func (f *mcpFixture) run(t *testing.T, include string, lines ...string) map[string]map[string]any {
	t.Helper()
	out := &bytes.Buffer{}
	du := &DeviceUnlock{Request: "Vault.Request", Status: "Vault.Status", Lock: "Vault.Lock"}
	s := newMCPServer("p", "1.0.0", f.client, f.server.URL, f.ops, du, toolPatterns(include), nil, out)
	if err := s.serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n")); err != nil {
		t.Fatal(err)
	}
	answers := map[string]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON on stdout: %q", line)
		}
		id, _ := json.Marshal(m["id"])
		answers[string(id)] = m
	}
	return answers
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", m)
	}
	return r
}

func TestMCPInitializeAndListTools(t *testing.T) {
	f := newMCPFixture(t, "")
	got := f.run(t, "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`not json`,
	)
	init := result(t, got["1"])
	if init["protocolVersion"] != "2025-06-18" || init["serverInfo"].(map[string]any)["name"] != "p" {
		t.Fatalf("initialize %v", init)
	}
	var names []string
	for _, tool := range result(t, got["2"])["tools"].([]any) {
		tm := tool.(map[string]any)
		names = append(names, tm["name"].(string))
		if tm["name"] == "Forge.GetIssue" && (tm["description"] != "Read an issue" || tm["inputSchema"].(map[string]any)["required"] == nil) {
			t.Fatalf("tool %v", tm)
		}
		if tm["name"] == "Forge.CloseIssue" && tm["annotations"].(map[string]any)["destructiveHint"] != true {
			t.Fatalf("tool %v", tm)
		}
		if tm["name"] == "Task.Text" && tm["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("tool %v", tm)
		}
	}
	// the device unlock plumbing is never a tool
	if strings.Join(names, ",") != "Forge.CloseIssue,Forge.GetIssue,Task.Text" {
		t.Fatalf("tools %v", names)
	}
	if got["4"]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method %v", got["4"])
	}
	if got["null"]["error"].(map[string]any)["code"].(float64) != -32700 {
		t.Fatalf("parse error %v", got["null"])
	}
	if len(got) != 5 {
		t.Fatalf("notifications must not be answered: %v", got)
	}
	if len(f.calls) != 0 {
		t.Fatal("listing must not call the server")
	}
}

func TestMCPToolFilters(t *testing.T) {
	f := newMCPFixture(t, "")
	got := f.run(t, "Forge.*", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Task.Text","arguments":{}}}`)
	if n := len(result(t, got["1"])["tools"].([]any)); n != 2 {
		t.Fatalf("%d tools", n)
	}
	if got["2"]["error"] == nil {
		t.Fatal("a filtered-out tool must not be callable")
	}
}

func TestMCPCallsCarryTheCredentials(t *testing.T) {
	f := newMCPFixture(t, "s.k")
	got := f.run(t, "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"Forge.GetIssue","arguments":{"number":1}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"Forge.CloseIssue","arguments":{"number":1}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"Forge.GetIssue","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"Task.Text"}}`,
	)
	ok := result(t, got["1"])
	if ok["isError"] != nil || ok["structuredContent"].(map[string]any)["title"] != "Bug" {
		t.Fatalf("call %v", ok)
	}
	locked := result(t, got["2"])
	text := locked["content"].([]any)[0].(map[string]any)["text"].(string)
	if locked["isError"] != true || !strings.Contains(text, "423") || !strings.Contains(text, "unlock") {
		t.Fatalf("locked %v", locked)
	}
	invalid := result(t, got["3"])
	if invalid["isError"] != true {
		t.Fatalf("missing required input must be a tool error: %v", invalid)
	}
	plain := result(t, got["4"])
	if plain["content"].([]any)[0].(map[string]any)["text"] != "plain text" || plain["structuredContent"] != nil {
		t.Fatalf("text result %v", plain)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := map[string]bool{}
	for _, c := range f.calls {
		seen[c] = true
	}
	if !seen[`Forge.GetIssue {"number":1} s.k`] || !seen[`Task.Text null s.k`] {
		t.Fatalf("calls %v", f.calls)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "Forge.GetIssue {}") {
			t.Fatal("an invalid call must not reach the server")
		}
	}
}
