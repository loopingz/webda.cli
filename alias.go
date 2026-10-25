package main

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// aliasHelpName infers an alias from literal definitions in shell startup files.
// Shells do not pass the original alias to child processes. Never start a shell
// or evaluate startup code just to render help; ambiguous matches keep argv[0].
func aliasHelpName(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	aliases := map[string][]string{}
	for _, path := range aliasFiles() {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			words, ok := literalShellWords(scanner.Text())
			if !ok || len(words) < 2 {
				continue
			}
			if words[0] == "unalias" {
				for _, name := range words[1:] {
					if name == "-a" {
						clear(aliases)
					} else {
						delete(aliases, name)
					}
				}
				continue
			}
			if words[0] != "alias" {
				continue
			}
			for _, definition := range words[1:] {
				name, value, ok := strings.Cut(definition, "=")
				if !ok || name == "" || strings.ContainsAny(name, " /\\\t\r\n='\"") {
					continue
				}
				// Overwriting also removes an earlier literal definition when
				// a later definition contains expansions we cannot interpret.
				aliases[name], _ = literalShellWords(value)
			}
		}
		_ = file.Close()
	}
	best, length, ambiguous := "", 0, false
	for name, expansion := range aliases {
		if len(expansion) == 0 || len(expansion) > len(argv) || !sameAliasCommand(expansion[0], argv[0]) || !aliasGlobalOptions(expansion[1:]) {
			continue
		}
		if !slices.Equal(expansion[1:], argv[1:len(expansion)]) {
			continue
		}
		switch {
		case len(expansion) > length:
			best, length, ambiguous = name, len(expansion), false
		case len(expansion) == length:
			ambiguous = true
		}
	}
	if ambiguous {
		return ""
	}
	return best
}

func aliasFiles() []string {
	home := userHome()
	var names []string
	switch detectShell() {
	case "bash":
		names = []string{".profile", ".bash_profile", ".bashrc", ".bash_aliases"}
	case "zsh":
		if dir := os.Getenv("ZDOTDIR"); dir != "" {
			home = dir
		}
		names = []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"}
	}
	for i := range names {
		names[i] = filepath.Join(home, names[i])
	}
	return names
}

func sameAliasCommand(command, invoked string) bool {
	if !strings.ContainsAny(command, "/\\") {
		return command == filepath.Base(invoked)
	}
	a, errA := filepath.Abs(command)
	b, errB := filepath.Abs(invoked)
	return errA == nil && errB == nil && a == b
}

// Restrict inference to aliases for the CLI and its global options. An alias
// containing a subcommand needs a different command path, not just a root name.
func aliasGlobalOptions(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--profile" || arg == "--timeout":
			i++
			if i == len(args) || args[i] == "" || strings.HasPrefix(args[i], "-") {
				return false
			}
		case strings.HasPrefix(arg, "--profile=") || strings.HasPrefix(arg, "--timeout="):
		default:
			return false
		}
	}
	return true
}

// literalShellWords handles quoting and escapes, but rejects expansions and
// shell operators. It is deliberately a parser for literal aliases, not a shell.
func literalShellWords(line string) ([]string, bool) {
	var words []string
	var word strings.Builder
	var quote byte
	started := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		switch {
		case c == '\\':
			i++
			if i == len(line) {
				return nil, false
			}
			if quote == '"' && !strings.ContainsRune("$`\"\\", rune(line[i])) {
				word.WriteByte('\\')
			}
			word.WriteByte(line[i])
			started = true
		case c == quote:
			quote = 0
		case c == '$' || c == '`':
			return nil, false
		case quote != 0:
			word.WriteByte(c)
		case c == '\'' || c == '"':
			quote, started = c, true
		case strings.ContainsRune(" \t\r", rune(c)):
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		case c == '#' && !started:
			i = len(line)
		case strings.ContainsRune(";|&<>()*?[]{}~\n", rune(c)):
			return nil, false
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	if started {
		words = append(words, word.String())
	}
	return words, true
}

func showAliasHelp(root *cobra.Command, help func()) {
	name := aliasHelpName(os.Args)
	if name == "" {
		help()
		return
	}
	if root.Annotations == nil {
		root.Annotations = map[string]string{}
	}
	key := cobra.CommandDisplayNameAnnotation
	previous, existed := root.Annotations[key]
	root.Annotations[key] = name
	defer func() {
		if existed {
			root.Annotations[key] = previous
		} else {
			delete(root.Annotations, key)
		}
	}()
	help()
}
