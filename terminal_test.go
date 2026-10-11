package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/loopingz/webda-cli/tokenstore"
	"github.com/loopingz/webda-cli/webdaclient"
	"github.com/spf13/cobra"
)

// This helper runs the real forms and explorer in a subprocess with a PTY.
func TestExplorerTerminalHelper(t *testing.T) {
	if os.Getenv("WEBDA_CLI_TUI_HELPER") != "1" {
		return
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		data, _ := json.Marshal(body)
		if err := os.WriteFile(filepath.Join(os.Getenv("WEBDA_CLI_TEST_DIR"), "request.json"), data, 0o600); err != nil {
			t.Error(err)
		}
		_, _ = fmt.Fprintln(w, `[{"name":"alice","status":"ok"}]`)
	}))
	defer srv.Close()
	var err error
	cli, err = webdaclient.New("fixture", srv.URL, tokenstore.NewMachineStore(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	root := &cobra.Command{Use: "myapp"}
	root.PersistentFlags().Duration("timeout", 5*time.Second, "")
	op := Operation{Name: "Users.create", Description: "Create a user", Input: map[string]any{"type": "object", "required": []any{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string"}}}}
	addExplorer(root, "fixture", srv.URL, []Operation{op})
	root.SetArgs([]string{"explore"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
func TestExplorerTerminalFlow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY integration uses Unix terminals")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(executable, "-test.run=^TestExplorerTerminalHelper$")
	cmd.Env = append(os.Environ(), "WEBDA_CLI_TUI_HELPER=1", "WEBDA_CLI_TEST_DIR="+dir, "HOME="+dir, "TERM=xterm-256color", "NO_COLOR=1")
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 110})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = terminal.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	chunks := make(chan string, 100)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := terminal.Read(buf)
			if n > 0 {
				chunks <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	seen := ""
	waitFor := func(text string) {
		t.Helper()
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		for !strings.Contains(seen, text) {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					t.Fatalf("terminal closed waiting for %q: %s", text, seen)
				}
				seen += chunk
			case <-deadline.C:
				t.Fatalf("timeout waiting for %q: %s", text, seen)
			}
		}
		seen = ""
	}
	send := func(keys string) {
		t.Helper()
		if _, err := terminal.WriteString(keys); err != nil {
			t.Fatal(err)
		}
	}
	waitFor("Search operations")
	send("\r")
	waitFor("Required input")
	send("alice\r")
	waitFor("Review request")
	send("y")
	waitFor("Completed in")
	send("t")
	waitFor("table view")
	send("q")
	// Wait for the child to restore the terminal and exit, with a bounded deadline.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal did not exit")
	}
	data, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"name":"alice"}` {
		t.Fatalf("wrong request: %s", data)
	}
}
