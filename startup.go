package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/loopingz/webda.cli/oauth"
	"github.com/loopingz/webda.cli/tokenstore"
	"github.com/spf13/cobra"
)

func requestedTimeout(args []string) (time.Duration, error) {
	value := "30s"
	for i, arg := range args {
		if strings.HasPrefix(arg, "--timeout=") {
			value = strings.TrimPrefix(arg, "--timeout=")
		}
		if arg == "--timeout" {
			if i+1 >= len(args) {
				return 0, fmt.Errorf("--timeout requires a duration")
			}
			value = args[i+1]
		}
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive duration, for example 30s")
	}
	return timeout, nil
}
func requestedProfile(args []string) string {
	for i, arg := range args {
		if strings.HasPrefix(arg, "--profile=") {
			return strings.TrimPrefix(arg, "--profile=")
		}
		if arg == "--profile" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func firstCommand(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--profile" || arg == "--timeout" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}
func localInvocation(args []string) bool {
	if firstCommand(args) == "" && (!terminalInput(os.Stdin) || !terminalOutput(os.Stdout)) {
		return true
	}
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "--generate-cli-skeleton" || arg == "--generate-cli-skeleton=true" {
			return true
		}
	}
	switch firstCommand(args) {
	case "help", "version", "setup", "profiles", "completion", "__complete", "__completeNoDesc":
		return true
	}
	return false
}

func addLocalCommands(root *cobra.Command, invoked string, cfg Config) {
	root.AddCommand(&cobra.Command{Use: "profiles", Short: "List configured profiles", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		names := make([]string, 0, len(cfg.Profiles))
		for name, p := range cfg.Profiles {
			if u, err := url.Parse(p.URL); err == nil && u.Host != "" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", name, cfg.Profiles[name].URL)
		}
		return nil
	}})
	setup := &cobra.Command{Use: "setup", Short: "Configure a host profile and optional shell completion", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("name")
		target, _ := cmd.Flags().GetString("url")
		install, _ := cmd.Flags().GetBool("install-completion")
		clientID, _ := cmd.Flags().GetString("client-id")
		scope, _ := cmd.Flags().GetString("scope")
		if name == "" || target == "" {
			if !terminalInput(cmd.InOrStdin()) || !terminalOutput(cmd.ErrOrStderr()) {
				return fmt.Errorf("supply --name and --url, or run setup in a terminal")
			}
			form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Profile name").Value(&name), huh.NewInput().Title("Server URL").Value(&target), huh.NewConfirm().Title("Install completion for your shell?").Value(&install)))
			if err := form.RunWithContext(cmd.Context()); err != nil {
				return err
			}
		}
		if name == "" || name == "update" || filepath.Base(name) != name || strings.ContainsAny(name, "\\/ ") || name == "." || name == ".." {
			return fmt.Errorf("choose a simple profile name without spaces or path separators")
		}
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("--url must be an http(s) server URL without credentials, query, or fragment")
		}
		if cfg.Profiles == nil {
			if existing, err := loadConfig(); err == nil {
				cfg = existing
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("cannot update existing config: %w", err)
			}
		}
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]Profile{}
		}
		next := Profile{URL: strings.TrimRight(target, "/"), ClientID: clientID, Scope: scope}
		previous, existed := cfg.Profiles[name]
		if existed {
			// re-running setup keeps the client id and scope not given again
			if next.ClientID == "" {
				next.ClientID = previous.ClientID
			}
			if next.Scope == "" {
				next.Scope = previous.Scope
			}
		}
		cfg.Profiles[name] = next
		if err := saveConfig(cfg); err != nil {
			return err
		}
		if existed && strings.TrimRight(previous.URL, "/") != next.URL {
			if err := forgetServer(name); err != nil {
				return err
			}
		}
		if install {
			if err := installCompletion(root, invoked); err != nil {
				return err
			}
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Saved profile %s. Run %s --profile %s explore to sign in and browse operations.\n", name, invoked, name)
		return err
	}}
	setup.Flags().String("name", "", "profile name")
	setup.Flags().String("url", "", "server URL")
	setup.Flags().String("client-id", "", "OAuth client id (default "+defaultClientID+")")
	setup.Flags().String("scope", "", "OAuth scope (default \""+defaultScope+"\")")
	setup.Flags().Bool("install-completion", false, "install shell completion and configure shell startup")
	root.AddCommand(setup)
	// Cobra creates its standard completion group during execution.
	completion := &cobra.Command{Use: "completion", Short: "Generate or explicitly install shell completion"}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		shell := shell
		completion.AddCommand(&cobra.Command{Use: shell, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			switch shell {
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			default:
				return root.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			}
		}})
	}
	completion.AddCommand(&cobra.Command{Use: "install", Short: "Install completion and configure shell startup", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { return installCompletion(root, invoked) }})
	root.AddCommand(completion)
}

// newTokenStore opens the token store (tests replace it).
var newTokenStore = tokenstore.NewTokenStore

// forgetServer removes what a profile kept from its previous server: the token, the OAuth metadata and the operations.
func forgetServer(profile string) error {
	store := newTokenStore(configDir())
	// backends report a missing token differently: only fail when a token remains after the delete
	if err := store.Delete(profile); err != nil {
		if _, loadErr := store.Load(profile); loadErr == nil {
			return fmt.Errorf("cannot remove the sign-in of the previous server: %w", err)
		}
	}
	for _, path := range []string{metadataCachePath(profile), opsPath(profile)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func installCompletion(root *cobra.Command, name string) error {
	shell := detectShell()
	var err error
	switch shell {
	case "zsh":
		err = installZshCompletion(root, name)
	case "bash":
		err = installBashCompletion(root, name)
	case "fish":
		err = installFishCompletion(root, name)
	default:
		return fmt.Errorf("cannot detect supported shell; use completion bash, zsh, or fish")
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(root.ErrOrStderr(), "Completion installed for %s. Restart your shell to activate it.\n", shell)
	return nil
}

// operationsCache is <profile>.operations: the operations document and the URL its operations are executed against.
type operationsCache struct {
	OperationsURL string          `json:"webda_cli_operations_url"`
	Document      json.RawMessage `json:"webda_cli_document"`
}

// cachedOperations reads the cached operations; a cache written by an older version (the raw document) executes
// against <url>/operations, and so does a cached URL that is not on the profile origin.
func cachedOperations(name, baseURL string) ([]Operation, ServerInfo, error) {
	data, err := os.ReadFile(opsPath(name))
	if err != nil {
		return nil, ServerInfo{}, err
	}
	var cache operationsCache
	if json.Unmarshal(data, &cache) == nil && len(cache.Document) > 0 {
		ops, info, err := parseOperationsResponse(cache.Document)
		if err == nil && cache.OperationsURL != "" && oauth.Origin(cache.OperationsURL) == oauth.Origin(baseURL) {
			setEndpoint(ops, cache.OperationsURL)
		}
		return ops, info, err
	}
	return parseOperationsResponse(data)
}

// setEndpoint records the operations URL on the operations of the operations transport.
func setEndpoint(ops []Operation, endpoint string) {
	for i := range ops {
		if ops[i].Source != "openapi" {
			ops[i].Endpoint = endpoint
		}
	}
}
