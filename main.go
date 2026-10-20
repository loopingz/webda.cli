package main

import (
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
	"sort"
	"strings"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/loopingz/webda.cli/tui"
	"github.com/loopingz/webda.cli/updater"
	"github.com/loopingz/webda.cli/webdaclient"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

const (
	configDirName  = ".webdacli"
	configFileName = "config.yaml"
	opsExt         = ".operations"
)

// Operation is a simplified structure for dynamic commands.
type Operation struct {
	Name        string           `json:"name"`
	Summary     string           `json:"summary,omitempty"`
	Source      string           `json:"source,omitempty"` // "operations" or "openapi"
	Destructive bool             `json:"destructive,omitempty"`
	Method      string           `json:"method"`
	Path        string           `json:"path"`
	Description string           `json:"description"`
	Params      []map[string]any `json:"params"`
	Raw         map[string]any   `json:"-"`
	// Endpoint is the operations URL this operation is executed against (POST <Endpoint>/<name>); "" means
	// <profile URL>/operations. OpenAPI operations use their own method and path.
	Endpoint string         `json:"-"`
	Input    map[string]any `json:"input"`  // JSON Schema for operation input
	Output   map[string]any `json:"output"` // JSON Schema for operation output
}

// ServerInfo holds metadata from the operations response.
type ServerInfo struct {
	LogoURL string
	// OperationsVersion is the version of the operations listing (a hash on Webda servers), not the server version.
	OperationsVersion string
	CLIVersionRange   string
	CLIDownloadURL    string
	// Aliases maps a model plural to its singular in operation names ("MindNodes" → "MindNode").
	Aliases map[string]string
}

// operationsResponse attempts to map possible shapes.
type operationsResponse struct {
	Operations []Operation `json:"operations"`
}

var cli *webdaclient.Client
var tokenStore tokenstore.TokenStore

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func configDir() string { return filepath.Join(userHome(), configDirName) }

func opsPath(name string) string { return filepath.Join(configDir(), name+opsExt) }

func lockPath(name string) string { return filepath.Join(configDir(), name+".lock") }

// ensureConfigDir creates the configuration directory and restricts it to the user (0700).
func ensureConfigDir() error {
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return err
	}
	return os.Chmod(configDir(), 0o700)
}

// writePrivate writes a file of the configuration directory readable by the user only (0600, also when it existed).
func writePrivate(path string, data []byte) error {
	if err := ensureConfigDir(); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// User represents /me response subset.
type User struct {
	UUID  string `json:"uuid"`
	Email string `json:"email"`
}

func fetchCurrentUser(ctx context.Context) (User, error) {
	var u User
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cli.BaseURL+"/auth/me", nil)
	if err != nil {
		return u, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return u, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return u, fmt.Errorf("/auth/me failed: %s %s", resp.Status, string(body))
	}
	dec := json.NewDecoder(resp.Body)
	_ = dec.Decode(&u)
	return u, nil
}

// fetchOperations loads the operations from the advertised URI (or <url>/operations) and,
// on a 404, from the OpenAPI document served at the server root. The raw document is cached.
// Operations are executed against the URL they were listed from (the root fallback executes against <url>/operations).
func fetchOperations(ctx context.Context, name string, meta *oauth.Metadata) ([]Operation, ServerInfo, error) {
	defaultURL := cli.BaseURL + "/operations"
	opsURL := defaultURL
	if meta != nil && meta.OperationsURI != "" {
		opsURL = strings.TrimRight(meta.OperationsURI, "/")
	}
	status, body, err := getJSON(ctx, opsURL)
	if err != nil {
		return nil, ServerInfo{}, err
	}
	if status == http.StatusNotFound {
		opsURL = defaultURL
		status, body, err = getJSON(ctx, cli.BaseURL+"/")
		if err != nil {
			return nil, ServerInfo{}, err
		}
	}
	if status >= 300 {
		return nil, ServerInfo{}, fmt.Errorf("operations fetch failed: %d %s", status, http.StatusText(status))
	}
	ops, info, err := parseOperationsResponse(body)
	if err != nil {
		return nil, ServerInfo{}, err
	}
	setEndpoint(ops, opsURL)
	if data, err := json.Marshal(operationsCache{OperationsURL: opsURL, Document: body}); err == nil {
		_ = writePrivate(opsPath(name), data)
	}
	return ops, info, nil
}

func getJSON(ctx context.Context, target string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// parseOperationsResponse parses the full /operations response, returning
// operations and a ServerInfo with metadata.
func parseOperationsResponse(body []byte) ([]Operation, ServerInfo, error) {
	var info ServerInfo
	var gen map[string]any
	if err := json.Unmarshal(body, &gen); err == nil {
		info.LogoURL, _ = gen["logo"].(string)
		info.OperationsVersion, _ = gen["version"].(string)
		if aliases, ok := gen["aliases"].(map[string]any); ok {
			info.Aliases = make(map[string]string, len(aliases))
			for plural, singular := range aliases {
				if singular, ok := singular.(string); ok {
					info.Aliases[plural] = singular
				}
			}
		}
		if cli, ok := gen["cli"].(map[string]any); ok {
			info.CLIVersionRange, _ = cli["version_range"].(string)
			info.CLIDownloadURL, _ = cli["download_url"].(string)
		}
		ops, err := parseOperations(body)
		return ops, info, err
	}
	ops, err := parseOperations(body)
	return ops, info, err
}

// parseOperations parses the operations specification JSON body into a slice of Operation.
// It supports several shapes:
// - top-level array of operations
// - object with `operations` array
// - object with `operations` map (the 'normal' format used by the user)
// - openapi-like `paths` map
func parseOperations(body []byte) ([]Operation, error) {
	ops, err := parseOperationsRaw(body)
	if err != nil {
		return nil, err
	}
	for i := range ops {
		if ops[i].Source == "" {
			ops[i].Source = "operations"
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Path < b.Path
	})
	unique := ops[:0]
	for i := range ops {
		op := &ops[i]
		if n := len(unique); n > 0 && unique[n-1].Name == op.Name {
			fmt.Fprintf(os.Stderr, "Warning: skipping duplicate operation %q (%s %s)\n", op.Name, op.Method, op.Path)
			continue
		}
		unique = append(unique, *op)
	}
	return unique, nil
}

func parseOperationsRaw(body []byte) ([]Operation, error) {
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
			if ops, ok := opsAny.(map[string]any); ok {
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
						op.Summary, _ = m["summary"].(string)
						op.Destructive, _ = m["destructive"].(bool)
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

		// shape 4: OpenAPI document
		if list := parseOpenAPI(gen); len(list) > 0 {
			return list, nil
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
	short := "Webda dynamic CLI"
	if baseURL != "" {
		short = fmt.Sprintf("CLI for %s", baseURL)
	}
	cmd := &cobra.Command{Use: name, Short: short, Long: short + "\n\nRun setup to configure a profile, explore to browse operations, or use a command directly."}
	return cmd
}

func main() {
	if err := runCLI(); err != nil {
		if errors.Is(err, webdaclient.ErrSignedOut) {
			fmt.Fprintln(os.Stderr, signedOutMessage(filepath.Base(os.Args[0])))
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// signedOutMessage is printed when the server refused the refresh token.
func signedOutMessage(bin string) string {
	return fmt.Sprintf("Your sign-in expired or was revoked; run %s auth.", bin)
}

func runCLI() error {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	invoked := filepath.Base(os.Args[0])
	args := os.Args[1:]
	profile := requestedProfile(args)
	if profile == "" {
		profile = invoked
	}
	if filepath.Base(profile) != profile || profile == "." || profile == ".." || strings.ContainsAny(profile, "\\/ ") {
		return fmt.Errorf("invalid profile name %q", profile)
	}
	cfg, configErr := loadConfig()
	prof := cfg.Profiles[profile]
	baseURL := prof.URL
	root := buildRootCommand(invoked, baseURL)
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.PersistentFlags().String("profile", profile, "named server profile (defaults to binary name)")
	timeout, err := requestedTimeout(args)
	if err != nil {
		return err
	}
	root.PersistentFlags().Duration("timeout", timeout, "request timeout")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if timeout <= 0 {
			return fmt.Errorf("--timeout must be positive")
		}
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	root.SetContext(ctx)
	addLocalCommands(root, invoked, cfg)
	ops, serverInfo, _ := cachedOperations(profile, baseURL)
	logoData, _ := os.ReadFile(tui.LogoCachePath(configDir(), profile))
	originalHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if terminalOutput(cmd.OutOrStdout()) {
			tui.RenderLogo(cmd.OutOrStdout(), logoData)
		}
		originalHelp(cmd, args)
	})
	root.AddCommand(newVersionCmd(baseURL, &serverInfo))
	if localInvocation(args) {
		buildCommandTree(root, nil, baseURL, ops, serverInfo.Aliases, logoData)
		addExplorer(root, profile, baseURL, ops)
		root.AddCommand(stubCommand(newAuthCmd(authEnv{}, profile, prof)), stubCommand(newLogoutCmd(authEnv{}, profile, prof)), &cobra.Command{Use: "whoami", Short: "Show current user"}, &cobra.Command{Use: "update", Short: "Update CLI"}, &cobra.Command{Use: "refresh-operations", Short: "Refresh cached operation definitions"})
		return root.Execute()
	}
	if configErr != nil {
		return fmt.Errorf("cannot load configuration: %w; run %s setup", configErr, invoked)
	}
	if baseURL == "" {
		return fmt.Errorf("unknown profile %q; run %s profiles or %s setup", profile, invoked, invoked)
	}
	tokenStore = tokenstore.NewTokenStore(configDir())
	env := defaultAuthEnv()
	switch firstCommand(args) {
	case "auth":
		root.AddCommand(newAuthCmd(env, profile, prof))
		return root.Execute()
	case "logout":
		root.AddCommand(newLogoutCmd(env, profile, prof))
		return root.Execute()
	}
	if err := authenticate(ctx, env, tokenStore, invoked, profile, prof, terminalInput(os.Stdin) && terminalOutput(os.Stderr)); err != nil {
		return err
	}
	meta := optionalMetadata(ctx, env, profile, prof)
	cli, err = webdaclient.New(profile, baseURL, tokenStore, meta, prof.EffectiveClientID(), nil)
	if err != nil {
		return err
	}
	cli.LockFile = lockPath(profile)
	fetchCtx, fetchCancel := context.WithTimeout(ctx, timeout)
	remoteOps, info, fetchErr := fetchOperations(fetchCtx, profile, meta)
	fetchCancel()
	switch {
	case fetchErr == nil:
		ops = remoteOps
		serverInfo = info
	case len(ops) == 0, errors.Is(fetchErr, webdaclient.ErrSignedOut):
		return fetchErr
	default:
		fmt.Fprintf(os.Stderr, "Using cached operations: %v\n", fetchErr)
	}
	if serverInfo.LogoURL != "" && terminalOutput(os.Stderr) {
		logoCtx, cancel := context.WithTimeout(ctx, timeout)
		data, err := tui.FetchAndCacheLogoWithContext(logoCtx, serverInfo.LogoURL, tui.LogoCachePath(configDir(), profile))
		cancel()
		if err == nil {
			logoData = data
		}
	}
	buildCommandTree(root, cli, baseURL, ops, serverInfo.Aliases, logoData)
	root.AddCommand(&cobra.Command{Use: "whoami", Short: "Show current user info", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		timeout, _ := cmd.Flags().GetDuration("timeout")
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		user, err := fetchCurrentUser(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(user)
	}})
	root.AddCommand(&cobra.Command{Use: "refresh-operations", Short: "Re-fetch operations spec", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		timeout, _ := cmd.Flags().GetDuration("timeout")
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		ops, _, err := fetchOperations(ctx, profile, meta)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Fetched %d operations\n", len(ops))
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "update", Short: "Update CLI explicitly", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		tag, err := updater.Update(serverInfo.CLIDownloadURL)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Updated to %s\n", tag)
		return err
	}})
	if needs, _ := updater.NeedsUpdate(version, serverInfo.CLIVersionRange); needs {
		mode := cfg.Update
		if mode == "prompt" && terminalInput(os.Stdin) && terminalOutput(os.Stderr) {
			accepted := false
			form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Update CLI to match server requirements?").Value(&accepted)))
			if err := form.RunWithContext(ctx); err != nil {
				return err
			}
			if accepted {
				mode = "silent"
			}
		}
		if mode == "silent" {
			if _, err := updater.Update(serverInfo.CLIDownloadURL); err != nil {
				return err
			}
			return updater.SelfReExec()
		}
		fmt.Fprintf(os.Stderr, "CLI update available (requires %s). Run %s update.\n", serverInfo.CLIVersionRange, invoked)
	}
	root.AddCommand(stubCommand(newAuthCmd(env, profile, prof)), stubCommand(newLogoutCmd(env, profile, prof)))
	addExplorer(root, profile, baseURL, ops)
	return root.Execute()
}

const completionMarker = ".completion-installed"

// installShellCompletion auto-detects the user's shell and installs completion
// scripts on first launch. A marker file prevents re-installation on subsequent runs.
func installShellCompletion(root *cobra.Command, name string) {
	markerPath := filepath.Join(configDir(), name+completionMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return // already installed
	}

	shell := detectShell()
	if shell == "" {
		return
	}

	var err error
	switch shell {
	case "zsh":
		err = installZshCompletion(root, name)
	case "bash":
		err = installBashCompletion(root, name)
	case "fish":
		err = installFishCompletion(root, name)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Note: could not auto-install %s completion: %v\n", shell, err)
		return
	}

	// Write marker
	_ = writePrivate(markerPath, []byte(shell+"\n"))
	fmt.Fprintf(os.Stderr, "Shell completion installed for %s. Restart your shell or run: source ~/.%src to activate.\n", shell, shell)
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}
	base := filepath.Base(shell)
	switch base {
	case "zsh":
		return "zsh"
	case "bash":
		return "bash"
	case "fish":
		return "fish"
	}
	return ""
}

func installZshCompletion(root *cobra.Command, name string) error {
	// Install to ~/.webdacli/completions and add source line to ~/.zshrc
	compDir := filepath.Join(configDir(), "completions")
	if err := os.MkdirAll(compDir, 0o700); err != nil {
		return err
	}
	compFile := filepath.Join(compDir, "_"+name)
	f, err := os.Create(compFile)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := root.GenZshCompletion(f); err != nil {
		return err
	}

	// Add fpath + compinit to .zshrc if not already present
	zshrc := filepath.Join(userHome(), ".zshrc")
	sourceLine := fmt.Sprintf("fpath=(%s $fpath); autoload -Uz compinit; compinit -C", compDir)
	return appendLineIfMissing(zshrc, sourceLine, "webdacli/completions")
}

func installBashCompletion(root *cobra.Command, name string) error {
	compDir := filepath.Join(configDir(), "completions")
	if err := os.MkdirAll(compDir, 0o700); err != nil {
		return err
	}
	compFile := filepath.Join(compDir, name+".bash")
	f, err := os.Create(compFile)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := root.GenBashCompletionV2(f, true); err != nil {
		return err
	}

	bashrc := filepath.Join(userHome(), ".bashrc")
	sourceLine := fmt.Sprintf("source %s", compFile)
	return appendLineIfMissing(bashrc, sourceLine, "webdacli/completions")
}

func installFishCompletion(root *cobra.Command, name string) error {
	compDir := filepath.Join(userHome(), ".config", "fish", "completions")
	if err := os.MkdirAll(compDir, 0o700); err != nil {
		return err
	}
	compFile := filepath.Join(compDir, name+".fish")
	f, err := os.Create(compFile)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return root.GenFishCompletion(f, true)
}

// appendLineIfMissing adds line to file if a marker string is not already present.
func appendLineIfMissing(filePath, line, marker string) error {
	data, err := os.ReadFile(filePath)
	if err == nil && strings.Contains(string(data), marker) {
		return nil // already configured
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "\n# webda-cli shell completion\n%s\n", line)
	return err
}

func mapKeys[K comparable, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, fmt.Sprint(k))
	}
	return out
}
