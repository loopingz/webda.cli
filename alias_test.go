package main

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestLiteralShellWords(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{`alias myalias='webda.cli --profile test' # comment`, []string{"alias", "myalias=webda.cli --profile test"}},
		{`alias myalias="webda.cli --profile=test"`, []string{"alias", "myalias=webda.cli --profile=test"}},
		{`'/path with spaces/webda.cli' --profile test`, []string{"/path with spaces/webda.cli", "--profile", "test"}},
		{`webda.cli --profile te\st`, []string{"webda.cli", "--profile", "test"}},
		{`webda.cli ""`, []string{"webda.cli", ""}},
		{`"a\qb" "a\"b"`, []string{`a\qb`, `a"b`}},
	} {
		got, ok := literalShellWords(tc.line)
		if !ok || !slices.Equal(got, tc.want) {
			t.Errorf("literalShellWords(%q) = %q, %v; want %q", tc.line, got, ok, tc.want)
		}
	}
	for _, line := range []string{`webda.cli $PROFILE`, "webda.cli `hostname`", `webda.cli $(hostname)`, `webda.cli; touch marker`, `webda.cli | cat`, `webda.cli > marker`, `webda.cli && true`, `webda.cli *`, `webda.cli ~`, `webda.cli 'unterminated`, `webda.cli \`} {
		if _, ok := literalShellWords(line); ok {
			t.Errorf("accepted shell code or malformed quoting: %q", line)
		}
	}
}

func TestAliasHelpName(t *testing.T) {
	for _, tc := range []struct {
		name, aliases string
		argv          []string
		want          string
	}{
		{"profile", `alias myalias='webda.cli --profile test'`, []string{"/usr/bin/webda.cli", "--profile", "test", "--help"}, "myalias"},
		{"equals", `alias myalias="webda.cli --profile=test"`, []string{"webda.cli", "--profile=test", "help"}, "myalias"},
		{"binary only", `alias myalias='webda.cli'`, []string{"webda.cli", "--help"}, "myalias"},
		{"different profile", `alias myalias='webda.cli --profile test'`, []string{"webda.cli", "--profile", "other", "--help"}, ""},
		{"different binary", `alias myalias='other.cli --profile test'`, []string{"webda.cli", "--profile", "test", "--help"}, ""},
		{"different path", `alias myalias='/other/bin/webda.cli'`, []string{"/usr/bin/webda.cli", "--help"}, ""},
		{"absolute path", `alias myalias='/usr/bin/webda.cli --profile test'`, []string{"/usr/bin/webda.cli", "--profile", "test", "--help"}, "myalias"},
		{"spaces in path", `alias myalias="'/usr/local/my app/webda.cli' --profile test"`, []string{"/usr/local/my app/webda.cli", "--profile", "test", "--help"}, "myalias"},
		{"specific alias", "alias app='webda.cli'\nalias myalias='webda.cli --profile test'", []string{"webda.cli", "--profile", "test", "--help"}, "myalias"},
		{"ambiguous", "alias one='webda.cli --profile test'\nalias two='webda.cli --profile test'", []string{"webda.cli", "--profile", "test", "--help"}, ""},
		{"redefined", "alias myalias='webda.cli --profile old'\nalias myalias='webda.cli --profile test'", []string{"webda.cli", "--profile", "test", "--help"}, "myalias"},
		{"dynamic redefinition", "alias myalias='webda.cli'\nalias myalias='webda.cli $PROFILE'", []string{"webda.cli", "--help"}, ""},
		{"removed alias", "alias myalias='webda.cli'\nunalias myalias", []string{"webda.cli", "--help"}, ""},
		{"all aliases removed", "alias myalias='webda.cli'\nunalias -a", []string{"webda.cli", "--help"}, ""},
		{"shell code", `alias myalias='webda.cli; touch marker'`, []string{"webda.cli", "--help"}, ""},
		{"commented alias", `# alias myalias='webda.cli'`, []string{"webda.cli", "--help"}, ""},
		{"subcommand alias", `alias myalias='webda.cli explore'`, []string{"webda.cli", "explore", "--help"}, ""},
		{"incomplete option", `alias myalias='webda.cli --profile'`, []string{"webda.cli", "--profile", "test", "--help"}, ""},
		{"multiple options", `alias myalias='webda.cli --profile test --timeout=2s'`, []string{"webda.cli", "--profile", "test", "--timeout=2s", "--help"}, "myalias"},
		{"no arguments", `alias myalias='webda.cli'`, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SHELL", "/bin/bash")
			if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(tc.aliases), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := aliasHelpName(tc.argv); got != tc.want {
				t.Errorf("aliasHelpName(%q) = %q, want %q", tc.argv, got, tc.want)
			}
		})
	}
}

func TestAliasHelpFiles(t *testing.T) {
	for _, tc := range []struct{ shell, file string }{{"bash", ".bash_aliases"}, {"zsh", ".zshrc"}, {"fish", ".zshrc"}} {
		t.Run(tc.shell, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("SHELL", "/bin/"+tc.shell)
			t.Setenv("ZDOTDIR", "")
			if tc.shell == "zsh" {
				home = t.TempDir()
				t.Setenv("ZDOTDIR", home)
			}
			if err := os.WriteFile(filepath.Join(home, tc.file), []byte("alias myalias='webda.cli'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			want := "myalias"
			if tc.shell == "fish" {
				want = ""
			}
			if got := aliasHelpName([]string{"webda.cli", "--help"}); got != want {
				t.Fatalf("alias = %q, want %q", got, want)
			}
		})
	}
}

func TestAliasHelpRestoresDisplayName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/bash")
	if err := os.WriteFile(filepath.Join(userHome(), ".bashrc"), []byte("alias myalias='webda.cli'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldArgs := os.Args
	os.Args = []string{"webda.cli", "--help"}
	defer func() { os.Args = oldArgs }()
	root := buildRootCommand("webda.cli", "")
	root.Annotations = map[string]string{cobra.CommandDisplayNameAnnotation: "previous"}
	showAliasHelp(root, func() {
		if root.DisplayName() != "myalias" || root.Name() != "webda.cli" {
			t.Fatalf("display = %q, command = %q", root.DisplayName(), root.Name())
		}
	})
	if root.DisplayName() != "previous" {
		t.Fatal("help changed the command display name permanently")
	}
}

func TestAliasCLIHelper(t *testing.T) {
	if os.Getenv("WEBDA_CLI_ALIAS_HELPER") != "1" {
		return
	}
	os.Args = flag.Args()
	if err := runCLI(); err != nil {
		t.Fatal(err)
	}
}

func TestAliasCLIHelp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("alias myalias='webda.cli --profile test'\ntouch "+shellQuote(filepath.Join(home, "executed"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(Config{Profiles: map[string]Profile{"test": {URL: "http://127.0.0.1:1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(opsPath("test"), []byte(`{"operations":{"Operation.Id":{"input":{"type":"object"}}}}`)); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "myalias [command]"},
		{[]string{"operation", "--help"}, "myalias operation [command]"},
		{[]string{"operation", "id", "--help"}, "myalias operation id [flags]"},
		{[]string{"help", "operation", "id"}, "myalias operation id [flags]"},
		{[]string{"completion", "bash"}, "webda.cli"},
		{[]string{"operation", "id", "--generate-cli-skeleton"}, "{}"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			args := []string{"-test.run=^TestAliasCLIHelper$", "--", "webda.cli", "--profile", "test"}
			cmd := exec.Command(executable, append(args, tc.args...)...)
			cmd.Env = append(os.Environ(), "WEBDA_CLI_ALIAS_HELPER=1", "SHELL=/bin/bash", "NO_COLOR=1")
			out, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(out, []byte(tc.want)) {
				t.Fatalf("CLI output = %s, err = %v; want %q", out, err, tc.want)
			}
			if tc.args[0] == "completion" && bytes.Contains(out, []byte("myalias")) {
				t.Fatal("help inference changed generated shell completion")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(home, "executed")); !os.IsNotExist(err) {
		t.Fatal("rendering help executed shell startup code")
	}
}
