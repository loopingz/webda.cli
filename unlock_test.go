package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/loopingz/webda.cli/webdaclient"
)

const unlockHeader = "X-Example-Key"

// fakeVault serves the pairing operations: approve/deny decide what the status answers once the pairing exists.
type fakeVault struct {
	mu        sync.Mutex
	outcome   string // "approve", "deny" or "" (stays pending)
	requested map[string]any
	sentKeys  []string // unlock header of every request
	statusN   int
	locked    bool
	approved  string // the key the browser approved (from the fragment)
}

func (f *fakeVault) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer at" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.sentKeys = append(f.sentKeys, r.Header.Get(unlockHeader))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/operations/Vault.Request":
			f.requested = body
			_ = json.NewEncoder(w).Encode(map[string]any{"pairingId": "p/1", "code": "BCDF-GHJK", "expiresAt": time.Now().Add(time.Minute).Format(time.RFC3339)})
		case "/operations/Vault.Status":
			f.statusN++
			if body["pairing"] != "p/1" {
				http.Error(w, "bad pairing", http.StatusBadRequest)
				return
			}
			key := r.Header.Get(unlockHeader)
			switch {
			case f.outcome == "approve" && f.statusN > 1 && key == f.approved && key != "":
				_ = json.NewEncoder(w).Encode(map[string]any{"unlocked": true, "pairing": "gone"})
			case f.outcome == "deny" && f.statusN > 1:
				_ = json.NewEncoder(w).Encode(map[string]any{"unlocked": false, "pairing": "gone"})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"unlocked": false, "pairing": "pending"})
			}
		case "/operations/Vault.Lock":
			f.locked = true
			w.WriteHeader(http.StatusNoContent)
		case "/operations/Forge.GetIssue":
			if f.locked {
				http.Error(w, `{"error":"Vault is locked"}`, http.StatusLocked)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func unlockFixture(t *testing.T, f *fakeVault) (*webdaclient.Client, *memStore, *DeviceUnlock, string, authEnv, *bytes.Buffer) {
	t.Helper()
	srv := f.server(t)
	t.Cleanup(srv.Close)
	store := newMemStore()
	_ = store.Save("p", tokenstore.TokenInfo{AccessToken: "at", RefreshToken: "rt", Server: srv.URL})
	client, err := webdaclient.New("p", srv.URL, store, nil, "webda-cli", nil)
	if err != nil {
		t.Fatal(err)
	}
	du := &DeviceUnlock{Request: "Vault.Request", Status: "Vault.Status", Lock: "Vault.Lock", ApproveURL: "https://srv.example/connect?pairing={pairing}#{session}.{key}", Header: unlockHeader}
	out := &bytes.Buffer{}
	env := authEnv{
		Hostname: func() (string, error) { return "build-01.local\x1b[31m", nil },
		Open: func(u string) error {
			parsed, err := url.Parse(u)
			if err != nil || parsed.Query().Get("pairing") != "p/1" {
				t.Errorf("approval URL %q", u)
			}
			f.mu.Lock()
			f.approved = parsed.Fragment
			f.mu.Unlock()
			return nil
		},
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
	}
	return client, store, du, srv.URL + "/operations", env, out
}

func TestUnlockStoresTheApprovedKeyAndSendsIt(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	if err := runUnlock(context.Background(), env, client, du, endpoint, out); err != nil {
		t.Fatal(err)
	}
	ti, _ := store.Load("p")
	session, key, ok := strings.Cut(ti.UnlockKey, ".")
	if !ok || ti.UnlockHeader != unlockHeader || ti.UnlockKey != f.approved || len(key) != 43 || len(session) != 36 {
		t.Fatalf("stored unlock %q %q (approved %q)", ti.UnlockHeader, ti.UnlockKey, f.approved)
	}
	if f.requested["vaultSessionId"] != session || f.requested["deviceName"] != "webda-cli on build-01.local31m" {
		t.Fatalf("pairing request %v", f.requested)
	}
	if _, sent := f.requested["key"]; sent {
		t.Fatal("the key must not be sent in the pairing request")
	}
	if f.sentKeys[0] != "" {
		t.Fatal("the pairing request must not carry an unlock")
	}
	if !strings.Contains(out.String(), "BCDF-GHJK") || strings.Contains(out.String(), key) {
		t.Fatalf("output %q", out.String())
	}
	// every later request carries it
	if err := callOperation(context.Background(), client, endpoint, "Forge.GetIssue", map[string]any{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if last := f.sentKeys[len(f.sentKeys)-1]; last != ti.UnlockKey {
		t.Fatalf("unlock not sent: %q", last)
	}
}

func TestUnlockDeniedLeavesNothing(t *testing.T) {
	f := &fakeVault{outcome: "deny"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	err := runUnlock(context.Background(), env, client, du, endpoint, out)
	if err == nil || !strings.Contains(err.Error(), "denied or expired") {
		t.Fatalf("err %v", err)
	}
	if ti, _ := store.Load("p"); ti.UnlockKey != "" || client.Unlocked() {
		t.Fatal("a denied unlock must not be stored")
	}
}

func TestUnlockExpiresWhilePending(t *testing.T) {
	f := &fakeVault{}
	client, _, du, endpoint, env, out := unlockFixture(t, f)
	env.Sleep = func(ctx context.Context, _ time.Duration) error { return errors.New("deadline") }
	err := runUnlock(context.Background(), env, client, du, endpoint, out)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err %v", err)
	}
}

func TestHeadlessUnlockPrintsTheApprovalPage(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, _, du, endpoint, env, out := unlockFixture(t, f)
	env.Open = func(u string) error {
		f.mu.Lock()
		f.approved = u[strings.Index(u, "#")+1:]
		f.mu.Unlock()
		return errors.New("no browser")
	}
	if err := runUnlock(context.Background(), env, client, du, endpoint, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "do not share") || !strings.Contains(out.String(), f.approved) {
		t.Fatalf("output %q", out.String())
	}
}

func TestLockEndsTheUnlockAnd423DropsIt(t *testing.T) {
	f := &fakeVault{outcome: "approve"}
	client, store, du, endpoint, env, out := unlockFixture(t, f)
	if err := runUnlock(context.Background(), env, client, du, endpoint, out); err != nil {
		t.Fatal(err)
	}
	cmd := newLockCmd(client, du, endpoint)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !f.locked || client.Unlocked() {
		t.Fatal("lock must end the unlock on the server and locally")
	}
	if ti, _ := store.Load("p"); ti.UnlockKey != "" || ti.RefreshToken != "rt" {
		t.Fatalf("stored %+v", ti)
	}

	// an unlock the server ended (423) is dropped
	if err := client.SetUnlock(unlockHeader, "s.k"); err != nil {
		t.Fatal(err)
	}
	err := callOperation(context.Background(), client, endpoint, "Forge.GetIssue", map[string]any{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "423") {
		t.Fatalf("err %v", err)
	}
	if client.Unlocked() {
		t.Fatal("a 423 must drop the stored unlock")
	}
}

func TestParseDeviceUnlockRefusesUnsafeAdvertisements(t *testing.T) {
	ok := map[string]any{"request": "a", "status": "b", "lock": "c", "approve_url": "https://x/c?pairing={pairing}#{session}.{key}", "header": "X-Vault-Key"}
	if parseDeviceUnlock(ok) == nil {
		t.Fatal("valid advertisement refused")
	}
	for name, change := range map[string]map[string]any{
		"http page":       {"approve_url": "http://x/c?pairing={pairing}#{session}.{key}"},
		"no fragment":     {"approve_url": "https://x/c?pairing={pairing}&k={key}"},
		"auth header":     {"header": "Authorization"},
		"bad header":      {"header": "X Key"},
		"missing request": {"request": ""},
	} {
		v := map[string]any{}
		for k, x := range ok {
			v[k] = x
		}
		for k, x := range change {
			v[k] = x
		}
		if parseDeviceUnlock(v) != nil {
			t.Errorf("%s accepted", name)
		}
	}
	local := map[string]any{}
	for k, x := range ok {
		local[k] = x
	}
	local["approve_url"] = "http://localhost:8080/c?pairing={pairing}#{session}.{key}"
	if parseDeviceUnlock(local) == nil {
		t.Fatal("localhost approval page refused")
	}
}

func TestDeviceLabel(t *testing.T) {
	if got := deviceLabel(""); got != "webda-cli" {
		t.Fatal(got)
	}
	if got := deviceLabel(strings.Repeat("a", 60)); len(got) != 40 {
		t.Fatal(got)
	}
}
