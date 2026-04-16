# webda-cli

[![CI](https://github.com/loopingz/webda-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/loopingz/webda-cli/actions/workflows/ci.yml)
[![CodeQL](https://github.com/loopingz/webda-cli/actions/workflows/codeql.yml/badge.svg)](https://github.com/loopingz/webda-cli/actions/workflows/codeql.yml)
[![codecov](https://codecov.io/gh/loopingz/webda-cli/graph/badge.svg)](https://codecov.io/gh/loopingz/webda-cli)

A dynamic CLI client for [Webda](https://webda.io) applications. The binary name determines which remote host it connects to, configured via `~/.webdacli/config.yaml`.

## Installation

```bash
go install github.com/loopingz/webda-cli@latest
```

Or build from source:

```bash
go build -o myapp .
cp myapp /usr/local/bin/myapp
```

Create symlinks for each remote host you want to connect to — the binary name maps to the config key:

```bash
ln -s /usr/local/bin/myapp /usr/local/bin/mystaging
```

## Configuration

Create `~/.webdacli/config.yaml` with your host mappings:

```yaml
myapp: https://app.example.com
mystaging: https://staging.example.com
wlocal: http://localhost:18080
```

Each key is a command name. When you run `myapp`, it connects to `https://app.example.com`.

## Authentication

On first run, the CLI opens a browser for authentication. The token is stored in `~/.webdacli/<name>.tok` and automatically refreshed in the background.

```bash
myapp              # opens browser for auth on first run
myapp auth         # re-authenticate
myapp whoami       # show current user info
```

## Operations

The CLI fetches available operations from the remote host (`GET /operations`) and creates nested subcommands automatically. Operation names are split by `.` and converted to kebab-case:

| Operation | Command |
|---|---|
| `AuthorizerService.testOperations` | `myapp authorizer-service test-operations` |
| `MFA.SMS` | `myapp mfa sms` |
| `Sync.AWS` | `myapp sync aws` |

Operations are invoked via `POST /operations/<operationId>`.

### Flags from JSON Schema

If an operation defines an `input` JSON schema, flags are generated automatically:

```bash
myapp authorizer-service test-operations --user alice
```

### Interactive TUI

When required fields are missing, an interactive TUI form is displayed to collect input. You can also force the form with `--interactive` / `-i`:

```bash
myapp authorizer-service test-operations           # TUI triggers (--user is required)
myapp authorizer-service test-operations -i         # force TUI even with all flags
myapp authorizer-service test-operations --user foo # no TUI, executes directly
```

### JSON Skeleton and File Input

Generate a JSON skeleton for an operation's input schema, fill it in, then pass it back:

```bash
# Generate skeleton
myapp authorizer-service test-operations --generate-cli-skeleton > input.json

# Edit the file with your values
cat input.json
{
  "user": ""
}

# Run the operation with the file
myapp authorizer-service test-operations --input input.json
```

Flags override values from the file. The TUI form can fill remaining gaps:

```bash
# File provides some values, --user overrides, TUI fills the rest
myapp authorizer-service test-operations --input partial.json --user alice -i
```

### Refresh operations

```bash
myapp refresh-operations    # re-fetch operations from the server
```

## Shell Completion

Shell completion is **automatically installed** on first launch. The CLI detects your shell (zsh, bash, or fish) and installs the appropriate completion script.

- **zsh**: Writes to `~/.webdacli/completions/_<name>` and adds `fpath` to `~/.zshrc`
- **bash**: Writes to `~/.webdacli/completions/<name>.bash` and sources it from `~/.bashrc`
- **fish**: Writes to `~/.config/fish/completions/<name>.fish`

After the first launch, restart your shell (or `source ~/.zshrc`) to activate completion.

To manually regenerate:

```bash
myapp completion zsh > ~/.webdacli/completions/_myapp
myapp completion bash > ~/.webdacli/completions/myapp.bash
myapp completion fish > ~/.config/fish/completions/myapp.fish
```

## Logo

If the server provides a `logo` URL in the operations response, the CLI displays it inline in terminals that support it (iTerm2, Kitty, WezTerm). The logo appears in help output and interactive TUI forms.

## Files

| Path | Purpose |
|---|---|
| `~/.webdacli/config.yaml` | Host mappings |
| `~/.webdacli/<name>.tok` | Authentication tokens |
| `~/.webdacli/<name>.operations` | Cached operations spec |
| `~/.webdacli/<name>.logo` | Cached logo image |
| `~/.webdacli/completions/` | Shell completion scripts |
