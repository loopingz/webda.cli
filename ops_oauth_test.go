package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/loopingz/webda.cli/webdaclient"
)

const testOpenAPIDoc = `{
  "openapi": "3.0.0",
  "paths": {
    "/tasks/{uuid}": {"get": {"operationId": "Task.Get", "summary": "Read a task",
      "parameters": [{"name": "uuid", "in": "path", "required": true, "schema": {"type": "string"}}]}},
    "/tasks": {
      "post": {"operationId": "Task.Create", "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/NewTask"}}}}},
      "get": {"parameters": [{"name": "q", "in": "query", "schema": {"type": "string"}}]}
    }
  },
  "components": {"schemas": {"NewTask": {"type": "object", "properties": {"title": {"type": "string"}}}}}
}`

func testClient(t *testing.T, url string) *webdaclient.Client {
	t.Helper()
	store := tokenstore.NewMachineStore(t.TempDir())
	if err := store.Save("test", tokenstore.TokenInfo{AccessToken: "at", RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
	c, err := webdaclient.New("test", url, store, nil, "webda-cli", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func withCli(t *testing.T, c *webdaclient.Client) {
	t.Helper()
	orig := cli
	cli = c
	t.Cleanup(func() { cli = orig })
}

func TestParseOperationsSummaryAndDestructive(t *testing.T) {
	ops, err := parseOperations([]byte(`{"operations":{"Task.Get":{"summary":"Read a task","destructive":true}}}`))
	if err != nil || len(ops) != 1 {
		t.Fatalf("ops=%v err=%v", ops, err)
	}
	if ops[0].Summary != "Read a task" || !ops[0].Destructive || ops[0].Source != "operations" {
		t.Fatalf("unexpected %+v", ops[0])
	}
}

func TestParseOpenAPI(t *testing.T) {
	ops, err := parseOperations([]byte(testOpenAPIDoc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 {
		t.Fatalf("expected 3 operations, got %+v", ops)
	}
	by := map[string]Operation{}
	for _, o := range ops {
		if o.Source != "openapi" {
			t.Fatalf("source = %q", o.Source)
		}
		by[o.Name] = o
	}
	get := by["Task.Get"]
	if get.Method != "GET" || get.Path != "/tasks/{uuid}" || get.Summary != "Read a task" {
		t.Fatalf("unexpected %+v", get)
	}
	props, _ := get.Input["properties"].(map[string]any)
	if _, ok := props["uuid"]; !ok {
		t.Fatalf("missing uuid: %v", get.Input)
	}
	if req, _ := get.Input["required"].([]any); len(req) != 1 || req[0] != "uuid" {
		t.Fatalf("uuid must be required: %v", get.Input["required"])
	}
	create := by["Task.Create"]
	if create.Method != "POST" || create.Path != "/tasks" {
		t.Fatalf("unexpected %+v", create)
	}
	if props, _ := create.Input["properties"].(map[string]any); props["title"] == nil {
		t.Fatalf("missing title: %v", create.Input)
	}
	derived, ok := by[deriveOpName("get", "/tasks")]
	if !ok {
		t.Fatalf("missing derived operation in %v", by)
	}
	if props, _ := derived.Input["properties"].(map[string]any); props["q"] == nil {
		t.Fatalf("missing q: %v", derived.Input)
	}
}

func TestExecuteOpenAPIGet(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.RequestURI()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	op := Operation{Name: "Task.Get", Source: "openapi", Method: "GET", Path: "/tasks/{uuid}",
		Input: map[string]any{"type": "object", "required": []any{"uuid"}, "properties": map[string]any{"uuid": map[string]any{"type": "string"}, "q": map[string]any{"type": "string"}}}}
	if _, err := executeOperation(context.Background(), op, testClient(t, srv.URL), srv.URL, map[string]any{"uuid": "abc", "q": "x"}); err != nil {
		t.Fatal(err)
	}
	if got != "GET /tasks/abc?q=x" {
		t.Fatalf("got %q", got)
	}
	// missing path parameter
	if _, err := executeOperation(context.Background(), Operation{Name: "x", Source: "openapi", Method: "GET", Path: "/tasks/{uuid}"}, testClient(t, srv.URL), srv.URL, nil); err == nil {
		t.Fatal("missing path parameter must fail")
	}
}

func TestExecuteOpenAPIPost(t *testing.T) {
	var method, path, body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		method, path, body = r.Method, r.URL.Path, string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	op := Operation{Name: "Task.Create", Source: "openapi", Method: "POST", Path: "/tasks"}
	if _, err := executeOperation(context.Background(), op, testClient(t, srv.URL), srv.URL, map[string]any{"title": "t"}); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || path != "/tasks" || body != `{"title":"t"}` {
		t.Fatalf("got %s %s %s", method, path, body)
	}
}

func setupFetch(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".webdacli"), 0o700); err != nil {
		t.Fatal(err)
	}
	withCli(t, testClient(t, srv.URL))
	return dir
}

func TestFetchOperationsFallsBackToOpenAPI(t *testing.T) {
	var acceptRoot string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			acceptRoot = r.Header.Get("Accept")
			_, _ = w.Write([]byte(testOpenAPIDoc))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := setupFetch(t, srv)
	ops, _, err := fetchOperations(context.Background(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 3 || ops[0].Source != "openapi" {
		t.Fatalf("unexpected %+v", ops)
	}
	if acceptRoot != "application/json" {
		t.Fatalf("Accept = %q", acceptRoot)
	}
	cached, _, err := cachedOperations("test")
	if err != nil || len(cached) != 3 {
		t.Fatalf("cache: %v %v", cached, err)
	}
	_ = dir
}

func TestFetchOperationsUsesMetadataURI(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/custom/ops" {
			_, _ = w.Write([]byte(`{"operations":{"A.b":{"id":"A.b"}}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	setupFetch(t, srv)
	ops, _, err := fetchOperations(context.Background(), "test", &oauth.Metadata{OperationsURI: srv.URL + "/custom/ops"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Name != "A.b" || len(paths) != 1 {
		t.Fatalf("ops=%v paths=%v", ops, paths)
	}
}

func TestInsufficientScopeHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="Task.Delete"`)
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "insufficient_scope", "operation": "Task.Delete"})
	}))
	defer srv.Close()
	_, err := executeOperation(context.Background(), Operation{Name: "Task.Delete"}, testClient(t, srv.URL), srv.URL, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "this token does not cover Task.Delete") || !strings.Contains(msg, `auth --scope "Task.Delete offline_access"`) {
		t.Fatalf("unexpected message %q", msg)
	}
}

func TestOptionalMetadataFallsBackToNil(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // server down, no <profile>.oauth.json cache
	env := authEnv{HTTP: &http.Client{}}
	if m := optionalMetadata(context.Background(), env, "p", Profile{URL: url}); m != nil {
		t.Fatalf("expected nil metadata, got %+v", m)
	}
}

func TestOperationsSortedAndDeduplicated(t *testing.T) {
	doc := `{"openapi":"3.0.0","paths":{
	  "/b":{"post":{"operationId":"Dup"},"get":{"operationId":"Dup"}},
	  "/a":{"get":{"operationId":"Dup"},"delete":{"operationId":"Alpha"}}}}`
	ops, err := parseOperations([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].Name != "Alpha" || ops[1].Name != "Dup" {
		t.Fatalf("unexpected %+v", ops)
	}
	// ordered by method then path: GET /a comes first and is kept.
	if ops[1].Method != "GET" || ops[1].Path != "/a" {
		t.Fatalf("expected GET /a to be kept, got %+v", ops[1])
	}
}

func TestParseOpenAPIRejectsRelativePaths(t *testing.T) {
	doc := `{"openapi":"3.0.0","paths":{"tasks":{"get":{"operationId":"Bad"}},"/ok":{"get":{"operationId":"Good"}}}}`
	ops, err := parseOperations([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Name != "Good" {
		t.Fatalf("relative path must be skipped: %+v", ops)
	}
}

func TestOpenAPIRejectsDotPathParameters(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	op := Operation{Name: "Task.Get", Source: "openapi", Method: "GET", Path: "/tasks/{uuid}"}
	for _, v := range []string{".", ".."} {
		if _, err := executeOperation(context.Background(), op, testClient(t, srv.URL), srv.URL, map[string]any{"uuid": v}); err == nil {
			t.Fatalf("%q must be rejected", v)
		}
	}
	if called {
		t.Fatal("no request must be sent")
	}
}
