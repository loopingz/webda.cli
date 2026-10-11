package webdaclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loopingz/webda-cli/tokenstore"
)

func TestRefreshReplaysJSONBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/refresh" {
			_, _ = fmt.Fprintln(w, `{"access_token":"fresh","sequence":2,"expires_in":3600}`)
			return
		}
		data, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(data))
		if r.Header.Get("Authorization") == "Bearer expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintln(w, `{"result":"ok"}`)
	}))
	defer srv.Close()
	store := tokenstore.NewMachineStore(t.TempDir())
	if err := store.Save("fixture", tokenstore.TokenInfo{AccessToken: "expired", RefreshToken: "refresh", Sequence: "1"}); err != nil {
		t.Fatal(err)
	}
	client, err := New("fixture", srv.URL, store)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	payload := `{"name":"alice","active":false}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/operations/Test.run", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || len(bodies) != 2 || bodies[0] != payload || bodies[1] != payload {
		t.Fatalf("request not replayed intact: %d %#v", resp.StatusCode, bodies)
	}
}
func TestRefreshFailureReturnsUsefulError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	store := tokenstore.NewMachineStore(t.TempDir())
	if err := store.Save("fixture", tokenstore.TokenInfo{AccessToken: "expired", RefreshToken: "invalid", Sequence: "1"}); err != nil {
		t.Fatal(err)
	}
	client, err := New("fixture", srv.URL, store)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/operations", nil)
	_, err = client.Do(req)
	if err == nil || !strings.Contains(err.Error(), "authentication refresh failed") {
		t.Fatalf("expected refresh error, got %v", err)
	}
}
