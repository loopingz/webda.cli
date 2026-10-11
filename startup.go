package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v3"
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

func addLocalCommands(root *cobra.Command, invoked string, cfg map[string]string) {
	root.AddCommand(&cobra.Command{Use: "profiles", Short: "List configured profiles", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		names := make([]string, 0, len(cfg))
		for name, target := range cfg {
			if u, err := url.Parse(target); err == nil && u.Host != "" {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", name, cfg[name])
		}
		return nil
	}})
	setup := &cobra.Command{Use: "setup", Short: "Configure a host profile and optional shell completion", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("name")
		target, _ := cmd.Flags().GetString("url")
		install, _ := cmd.Flags().GetBool("install-completion")
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
		if cfg == nil {
			if existing, err := loadConfig(); err == nil {
				cfg = existing
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("cannot update existing config: %w", err)
			}
		}
		if cfg == nil {
			cfg = map[string]string{}
		}
		cfg[name] = strings.TrimRight(target, "/")
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(configDir(), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(configDir(), configFileName), data, 0o600); err != nil {
			return err
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
func cachedOperations(name string) ([]Operation, ServerInfo, error) {
	data, err := os.ReadFile(opsPath(name))
	if err != nil {
		return nil, ServerInfo{}, err
	}
	return parseOperationsResponse(data)
}
func authenticate(ctx context.Context, name, baseURL string) error {
	if ti, err := tokenStore.Load(name); err == nil && ti.AccessToken != "" {
		return nil
	}
	if !terminalInput(os.Stdin) || !terminalOutput(os.Stderr) {
		return fmt.Errorf("profile %s needs authentication; run auth in a terminal", name)
	}
	_, err := acquireToken(ctx, name, baseURL)
	return err
}
