package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/loopingz/webda-cli/tui"
	"github.com/loopingz/webda-cli/webdaclient"
	browser "github.com/pkg/browser"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v3"
)

const (
	configDirName  = ".webdacli"
	configFileName = "config.yaml"
	tokenExt       = ".tok"
	opsExt         = ".operations"
	callbackPort   = 18181
)

// Operation is a simplified structure for dynamic commands.
type Operation struct {
	Name        string           `json:"name"`
	Method      string           `json:"method"`
	Path        string           `json:"path"`
	Description string           `json:"description"`
	Params      []map[string]any `json:"params"`
	Raw         map[string]any   `json:"-"`
	Input       map[string]any   `json:"-"` // JSON Schema for operation input
	Output      map[string]any   `json:"-"` // JSON Schema for operation output
}

// operationsResponse attempts to map possible shapes.
type operationsResponse struct {
	Operations []Operation `json:"operations"`
}

var cli *webdaclient.Client

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func configDir() string { return filepath.Join(userHome(), configDirName) }

func loadConfig() (map[string]string, error) {
	cfgPath := filepath.Join(configDir(), configFileName)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func tokenPath(name string) string { return filepath.Join(configDir(), name+tokenExt) }
func opsPath(name string) string   { return filepath.Join(configDir(), name+opsExt) }

// TokenInfo holds refresh/access/sequence values.
type TokenInfo struct {
	RefreshToken string
	AccessToken  string
	Sequence     string
}

// TODO Encrypt token file contents on disk.
func parseTokenFile(path string) (TokenInfo, error) {
	var ti TokenInfo
	b, err := os.ReadFile(path)
	if err != nil {
		return ti, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) >= 1 {
		ti.RefreshToken = strings.TrimSpace(lines[0])
	}
	if len(lines) >= 2 {
		ti.AccessToken = strings.TrimSpace(lines[1])
	}
	if len(lines) >= 3 {
		ti.Sequence = strings.TrimSpace(lines[2])
	}
	if ti.RefreshToken == "" || ti.Sequence == "" {
		return ti, errors.New("invalid token file (missing refresh_token or sequence)")
	}
	return ti, nil
}

// acquireToken ensures tokens exist, returning the access token (and persisting all values).
func acquireToken(ctx context.Context, name, baseURL string) (string, error) {
	p := tokenPath(name)
	if ti, err := parseTokenFile(p); err == nil && ti.AccessToken != "" {
		return ti.AccessToken, nil
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return "", err
	}
	type authResult struct {
		Access string
		Err    error
	}
	resultCh := make(chan authResult, 1)
	srv := &http.Server{Addr: fmt.Sprintf(":%d", callbackPort)}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", baseURL)
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		refresh, seq := extractToken(body)
		if refresh == "" || seq == 0 {
			http.Error(w, "invalid body", http.StatusBadRequest)
			resultCh <- authResult{Err: errors.New("missing refresh_token or sequence")}
			return
		}
		access, seq, err := exchangeToken(ctx, baseURL, refresh, seq)
		if err != nil {
			http.Error(w, "failed to exchange", http.StatusInternalServerError)
			resultCh <- authResult{Err: err}
			return
		}
		// fetch current user (cli may not be initialized yet during initial auth)
		if cli != nil {
			if user, err := fetchCurrentUser(ctx); err == nil {
				fmt.Printf("Authenticated as user: %s (ID: %s)\n", user.Email, user.UUID)
			}
		}
		// persist all
		content := refresh + "\n" + access + "\n" + fmt.Sprintf("%d", seq) + "\n"
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			http.Error(w, "cannot persist token", http.StatusInternalServerError)
			resultCh <- authResult{Err: err}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		go func() { resultCh <- authResult{Access: access} }()
		go func() { _ = srv.Shutdown(context.Background()) }()
	})
	srv.Handler = mux
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			resultCh <- authResult{Err: err}
		}
	}()
	host, _ := os.Hostname()
	callback := fmt.Sprintf("http://localhost:%d/auth", callbackPort)
	authURL := strings.TrimRight(baseURL, "/") + "/auth/cli?callback=" + urlQueryEscape(callback) + "&name=" + urlQueryEscape(name+"-cli") + "&hostname=" + urlQueryEscape(host)
	fmt.Printf("Opening browser for CLI authentication: %s\n", authURL)
	_ = browser.OpenURL(authURL)
	fmt.Println("Waiting for authentication (Ctrl+C to cancel)...")
	select {
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return "", ctx.Err()
	case res := <-resultCh:
		return res.Access, res.Err
	case <-time.After(5 * time.Minute):
		_ = srv.Shutdown(context.Background())
		return "", errors.New("auth timeout")
	}
}

// extractToken returns refresh_token and sequence.
func extractToken(body []byte) (refresh string, seq int) {
	var obj map[string]any
	err := json.Unmarshal(body, &obj)
	if err != nil {
		return "", 0
	}
	if v, ok := obj["refresh_token"].(string); ok {
		refresh = v
	}
	if seq == 0 { // maybe numeric
		if v, ok := obj["sequence"].(float64); ok {
			seq = int(v)
		}
	}
	return refresh, seq
}

// exchangeToken exchanges the refresh token + sequence for an access token.
func exchangeToken(ctx context.Context, baseURL string, refresh string, sequence int) (string, int, error) {
	payload := map[string]any{"token": refresh, "sequence": sequence}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(baseURL, "/")+"/auth/refresh", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("exchange failed: %s %s", resp.Status, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return "", 0, err
	}
	access := ""
	if v, ok := obj["access_token"].(string); ok && v != "" {
		access = v
	}
	seq := 0
	if v, ok := obj["sequence"].(float64); ok {
		seq = int(v)
	}
	return access, seq, nil
}

// User represents /me response subset.
type User struct {
	UUID  string `json:"uuid"`
	Email string `json:"email"`
}

func fetchCurrentUser(ctx context.Context) (User, error) {
	var u User
	resp, err := cli.Request("GET", "/auth/me", nil)
	if err != nil {
		return u, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return u, fmt.Errorf("/auth/me failed: %s %s", resp.Status, string(body))
	}
	dec := json.NewDecoder(resp.Body)
	_ = dec.Decode(&u)
	return u, nil
}

func fetchOperations(ctx context.Context, name string) ([]Operation, string, error) {
	resp, err := cli.Request("GET", "/operations", nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("operations fetch failed: %s", resp.Status)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = os.WriteFile(opsPath(name), body, 0o600)
	return parseOperationsResponse(body)
}

// parseOperationsResponse parses the full /operations response, returning
// operations and an optional logo URL.
func parseOperationsResponse(body []byte) ([]Operation, string, error) {
	var gen map[string]any
	if err := json.Unmarshal(body, &gen); err == nil {
		logoURL, _ := gen["logo"].(string)
		ops, err := parseOperations(body)
		return ops, logoURL, err
	}
	ops, err := parseOperations(body)
	return ops, "", err
}

// parseOperations parses the operations specification JSON body into a slice of Operation.
// It supports several shapes:
// - top-level array of operations
// - object with `operations` array
// - object with `operations` map (the 'normal' format used by the user)
// - openapi-like `paths` map
func parseOperations(body []byte) ([]Operation, error) {
	// shape 1: top-level array
	var list []Operation
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		return list, nil
	}

	// shape 2: object with operations (array)
	var respWrap operationsResponse
	if err := json.Unmarshal(body, &respWrap); err == nil && len(respWrap.Operations) > 0 {
		return respWrap.Operations, nil
	}

	// shape 3: object with operations as a map (expected normal format)
	var gen map[string]any
	if err := json.Unmarshal(body, &gen); err == nil {
		if opsAny, ok := gen["operations"]; ok {
			switch ops := opsAny.(type) {
			case map[string]any:
				out := make([]Operation, 0, len(ops))
				// each key is the operation name, value may contain id, input, permission, etc.
				for key, v := range ops {
					op := Operation{Name: key}
					if m, ok := v.(map[string]any); ok {
						if id, ok := m["id"].(string); ok && id != "" {
							op.Name = id
						}
						if desc, ok := m["description"].(string); ok {
							op.Description = desc
						}
						op.Raw = m
						// Input can be an inline JSON schema (map) or a string reference
						switch inp := m["input"].(type) {
						case map[string]any:
							op.Input = inp
						case string:
							op.Params = []map[string]any{{"$ref": inp}}
						}
						if out, ok := m["output"].(map[string]any); ok {
							op.Output = out
						}
					} else {
						op.Raw = map[string]any{"value": v}
					}
					out = append(out, op)
				}
				if len(out) > 0 {
					return out, nil
				}
			}
		}

		// shape 4: openapi-like
		if paths, ok := gen["paths"].(map[string]any); ok {
			for pth, v := range paths {
				if mm, ok := v.(map[string]any); ok {
					for method, mv := range mm {
						mUpper := strings.ToUpper(method)
						op := Operation{Path: pth, Method: mUpper, Name: deriveOpName(method, pth), Raw: map[string]any{"openapi": mv}}
						list = append(list, op)
					}
				}
			}
			if len(list) > 0 {
				return list, nil
			}
		}
	}

	return nil, errors.New("unable to parse operations specification")
}

var pathVarRegexp = regexp.MustCompile(`[{:]([a-zA-Z0-9_]+)[}]?`)

func deriveOpName(method, path string) string {
	// Example: GET /users/{id} -> getUsersId
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, p := range parts {
		if p == "" {
			continue
		}
		p2 := pathVarRegexp.ReplaceAllString(p, "$1")
		if len(p2) > 0 {
			b.WriteString(strings.ToUpper(p2[:1]) + p2[1:])
		}
	}
	return b.String()
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}

func buildRootCommand(name, baseURL string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: fmt.Sprintf("CLI for %s", baseURL)}
	return cmd
}

func main() {
	invoked := filepath.Base(os.Args[0])
	// If invoked via 'go run', fallback to first arg after program name as logical name
	if strings.HasPrefix(invoked, "go-build") || strings.HasSuffix(invoked, ".tmp") {
		if len(os.Args) > 1 {
			invoked = os.Args[1]
		}
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot load config: %v\nCreate %s with mappings like: wc: https://demo.webda.io\n", err, filepath.Join(configDir(), configFileName))
		os.Exit(1)
	}
	baseURL := cfg[invoked]
	if baseURL == "" {
		fmt.Fprintf(os.Stderr, "Command name '%s' not found in config. Available: %s\n", invoked, strings.Join(mapKeys(cfg), ", "))
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigCh; cancel(); os.Exit(1) }()
	// Ensure initial token (auth flow) prior to constructing dynamic operations; we keep legacy acquire for interactive auth.
	_, err = acquireToken(ctx, invoked, baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v\n", err)
		os.Exit(1)
	}
	cli, err = webdaclient.New(invoked, baseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot initialize client: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()
	root := buildRootCommand(invoked, baseURL)
	root.PersistentFlags().Duration("timeout", 30*time.Second, "request timeout")
	root.AddCommand(&cobra.Command{Use: "auth", Short: "Re-run authentication flow", RunE: func(cmd *cobra.Command, args []string) error {
		// Delete token and reacquire
		_ = os.Remove(tokenPath(invoked))
		_, err := acquireToken(ctx, invoked, baseURL)
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "whoami", Short: "Show current user info", RunE: func(cmd *cobra.Command, args []string) error {
		ti, err := parseTokenFile(tokenPath(invoked))
		if err != nil {
			return err
		}
		if ti.AccessToken == "" {
			return errors.New("no access token; run auth")
		}
		user, err := fetchCurrentUser(cmd.Context())
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(user, "", "  ")
		fmt.Println(string(b))
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "refresh-operations", Short: "Re-fetch operations spec", RunE: func(cmd *cobra.Command, args []string) error {
		ops, _, err := fetchOperations(cmd.Context(), invoked)
		if err != nil {
			return err
		}
		fmt.Printf("Fetched %d operations\n", len(ops))
		return nil
	}})
	// Fetch operations for dynamic commands
	var logoData []byte
	if ops, logoURL, err := fetchOperations(ctx, invoked); err == nil {
		// Fetch and cache logo if URL provided
		if logoURL != "" {
			cachePath := tui.LogoCachePath(configDir(), invoked)
			logoData, _ = tui.FetchAndCacheLogo(logoURL, cachePath)
		}
		buildCommandTree(root, cli, baseURL, ops, logoData)
	} else {
		fmt.Fprintf(os.Stderr, "Warning: cannot fetch operations: %v\n", err)
	}

	// Set up logo display in help
	if len(logoData) > 0 {
		originalHelp := root.HelpFunc()
		root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
			tui.RenderLogo(os.Stdout, logoData)
			originalHelp(cmd, args)
		})
	}
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func mapKeys[K comparable, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, fmt.Sprint(k))
	}
	return out
}
