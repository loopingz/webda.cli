# webda-cli

[![CI](https://github.com/loopingz/webda.cli/actions/workflows/ci.yml/badge.svg)](https://github.com/loopingz/webda.cli/actions/workflows/ci.yml)
[![CodeQL](https://github.com/loopingz/webda.cli/actions/workflows/codeql.yml/badge.svg)](https://github.com/loopingz/webda.cli/actions/workflows/codeql.yml)
[![codecov](https://codecov.io/gh/loopingz/webda.cli/graph/badge.svg)](https://codecov.io/gh/loopingz/webda.cli)

A dynamic CLI client for [Webda](https://webda.io) applications. The binary name determines which remote host it connects to, configured via `~/.webdacli/config.yaml`.

## Installation

### Release installer (no Go SDK)

For macOS and Linux on Intel/AMD or ARM64, run:

```bash
curl -fsSL https://github.com/loopingz/webda.cli/releases/latest/download/install.sh | sh
```

Install and configure a client for a server in one step:

```bash
curl -fsSL https://github.com/loopingz/webda.cli/releases/latest/download/install.sh | sh -s -- --server https://app.example.com --name myapp
myapp explore
```

The installer verifies the binary's SHA-256 checksum and installs it to
`~/.local/bin` without requiring root access. Add this directory to your `PATH`
if it is not already there. `--name` sets both the executable and profile name;
`--server` saves the server URL through the CLI's setup command. Authentication
happens when you first use the client, so installation does not open a browser.

Additional options:

```bash
curl -fsSL https://github.com/loopingz/webda.cli/releases/latest/download/install.sh -o install.sh
sh install.sh --version v1.2.0 --install-dir "$HOME/bin"
sh install.sh --server https://staging.example.com --name staging --install-completion
```

Shell completion installation is opt-in. The script requires `curl` and either
`sha256sum` (Linux) or `shasum` (macOS), and supports `--help`.
The installer and `checksums.txt` will be included in releases containing this
change; older releases such as v1.1.0 do not provide them.

### Windows (no Go SDK)

In Windows PowerShell 5.1 or PowerShell 7, run:

```powershell
& ([scriptblock]::Create((irm https://github.com/loopingz/webda.cli/releases/latest/download/install.ps1)))
```

Install and configure a named client in one command:

```powershell
& ([scriptblock]::Create((irm https://github.com/loopingz/webda.cli/releases/latest/download/install.ps1))) -Server https://app.example.com -Name myapp
```

The installer selects the native AMD64 or ARM64 binary, verifies its SHA-256
checksum, and installs it to `%LOCALAPPDATA%\WebdaCLI` without administrator
access. It adds the directory to `PATH` for the current session. For future
terminals, open **Edit environment variables for your account**, edit the user
**Path**, and add `%LOCALAPPDATA%\WebdaCLI`.

Options include `-Version v1.2.0`, `-InstallDir "$HOME\bin"`, and
`-InstallCompletion` (requires `-Server`). Authentication happens when you first
use the client, for example `myapp explore`. The one-liner invokes the downloaded
script as a script block so these parameters can be passed directly.

The PowerShell installer will be available in the next release containing this
change. You can also download `webda-cli-windows-amd64.exe` or
`webda-cli-windows-arm64.exe` manually from the
[latest release](https://github.com/loopingz/webda.cli/releases/latest).

### Install with Go

Requires Go 1.25 or newer. Once a release containing the corrected module path is
published, install with:

```bash
go install github.com/loopingz/webda.cli@latest
```

This installs the command as `webda.cli` in `GOBIN`, or `$(go env GOPATH)/bin` when
`GOBIN` is unset. Add that directory to your `PATH`, then run `webda.cli setup`.

**The v1.1.0 release cannot be installed with this command:** it declares
`github.com/loopingz/webda-cli` as its module path, which differs from the actual
repository path. Use a source build until a corrected release is available:

```bash
git clone https://github.com/loopingz/webda.cli.git
cd webda.cli
go build -o webda-cli .
./webda-cli setup
```

To install the source-built binary under a custom command name:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 webda-cli "$HOME/.local/bin/myapp"
```

Add `$HOME/.local/bin` to your `PATH`. The binary name is the default profile name;
configure it with `myapp setup --name myapp --url https://app.example.com`.
You can also select an existing profile with `myapp --profile staging explore`.

Optional aliases retain the binary-name profile behavior:

```bash
ln -s "$HOME/.local/bin/myapp" "$HOME/.local/bin/mystaging"
```

## Configuration

Configure a named profile using the setup wizard, or supply values directly:

```bash
webda.cli setup
webda.cli setup --name staging --url https://staging.example.com
webda.cli profiles
webda.cli --profile staging explore
```

Host mappings are stored in `~/.webdacli/config.yaml`:

```yaml
myapp: https://app.example.com
staging: https://staging.example.com
wlocal: http://localhost:18080
lc:
  url: https://my.loopingz.com
  client_id: webda-cli
  scope: "* offline_access"
update: prompt
```

A profile is either a URL string or an object with `url` and optional `client_id`
(default `webda-cli`) and `scope` (default `"* offline_access"`). Set them with
`setup`:

```bash
myapp setup --name lc --url https://my.loopingz.com --client-id webda-cli --scope "* offline_access"
```

Running `setup` again for an existing profile keeps its `client_id` and `scope` unless
you pass them. Changing the URL of a profile deletes its stored sign-in and its cached
OAuth metadata and operations, so a token is never sent to another server.

The binary name remains the default profile, so existing symlinks work. Use
`--profile` to select another host without creating a symlink. Setup changes shell
configuration only when you choose completion installation or pass
`--install-completion`.

Help, version, setup, profiles, completion, and cached input skeletons work without
signing in or contacting the server. Operation help uses the last cached schema;
run `refresh-operations` to update it.

## Authentication

Sign-in uses OAuth 2.0 with PKCE. On first use, or with `myapp auth`, the CLI opens
your browser so you can sign in and choose which operations it may call. Tokens are
refreshed automatically (refresh tokens rotate, and concurrent requests share a
single refresh, also across CLI processes through `~/.webdacli/<profile>.lock`). The
access token is only ever sent to the profile's server: a redirect to another
origin (scheme, host or port) drops it, and a token saved for another server is
discarded.

```bash
myapp auth                         # sign in (or sign in again)
myapp auth --scope "Task.* offline_access"   # request a narrower scope
myapp auth --device                # sign in with a code instead of a browser
myapp auth --prompt consent        # browser flow only: login, consent or none
myapp whoami                       # show current user info
myapp logout                       # revoke the sign-in and forget it
```

- `--scope` overrides the profile scope for this sign-in.
- The device flow shows a verification URL, a code and a terminal QR code; approve it
  on any device. It is used automatically over SSH (`SSH_CONNECTION`), on Linux
  without `DISPLAY` or `WAYLAND_DISPLAY`, and when no browser can be opened.
  `--prompt` applies to the browser flow only.
- Without a terminal (scripts, CI), a first use that needs a sign-in fails fast with
  `run myapp auth`; run it once interactively.
- A failed re-authentication keeps your previous sign-in. A successful one revokes
  the previous refresh token.
- `logout` revokes the refresh token on the server, then deletes it locally.
- Sign-ins saved by older versions of the CLI are discarded once; sign in again.
- Connected apps can also be revoked from the web app.
- When a token does not cover an operation, the CLI prints
  `this token does not cover <operation>; run myapp auth --scope "<current scope> <operation>"`
  so you can request it without losing what the token already grants.
- The CLI prints `Sign in at: <url>` before opening the browser; open it yourself if no
  browser appears.

### Token storage

Tokens go to the OS keychain (service `webda-cli`, one entry per profile). Without a
keychain they are stored in an encrypted file, `~/.webdacli/<profile>.tok`, keyed with
the SSH agent or, failing that, the machine (with a random salt in
`~/.webdacli/<profile>.tok.salt`). Each token records the server it was issued for.

OAuth server metadata is cached in `~/.webdacli/<profile>.oauth.json`. It is checked
to be same-origin with the profile URL and is discovered again when invalid. A
`webda_result_uri` on another origin is ignored and the CLI shows its own page after
the browser sign-in.

### Server requirements

OAuth sign-in needs a Webda server running `@webda/oauth` (`OAuthServer`); against
other servers (discovery answers 404) `auth`, and any command that contacts the server
while no sign-in is stored, stops with `this server does not support OAuth sign-in`
(without a terminal, commands ask you to run `myapp auth` instead).
Operations come from the advertised `webda_operations_uri` (default `/operations`,
`Webda/OperationsTransport`) or, when that answers 404, from the REST OpenAPI
document served when `exposeOpenAPI` is enabled.

## Operations

The CLI fetches available operations from the remote host (`GET <operations URL>`: the
`webda_operations_uri` advertised by the OAuth metadata, `/operations` by default) and creates nested subcommands automatically. Operation names are split by `.` and converted to kebab-case:

| Operation | Command |
|---|---|
| `AuthorizerService.testOperations` | `myapp authorizer-service test-operations` |
| `MFA.SMS` | `myapp mfa sms` |
| `Sync.AWS` | `myapp sync aws` |

Operations are invoked via `POST <operations URL>/<operationId>`, against the URL they
were listed from (also when loaded from the cache). Operations from the OpenAPI
document use their own method and path.

### Flags from JSON Schema

If an operation defines an `input` JSON schema, flags are generated automatically:

```bash
myapp authorizer-service test-operations --user alice
```

### Interactive TUI

Run `myapp explore`, or run `myapp` without arguments in a terminal, to browse
operations. The explorer provides fuzzy search across names and descriptions,
operation details, favorites, and recent operation names. Wide terminals show
list and details side by side; narrow terminals stack them.

- `/`: search operations; `↑` / `↓`: navigate; `Enter`: configure.
- `f`: toggle a favorite; `c`: copy a shell command; `q`: quit.
- Forms show required fields first, optional fields under **More options**, schema
  defaults/examples, validation, and a redacted request review before execution.
- In results, use `↑` / `↓` or page keys to scroll, `/` to search lines, `t` for
  tables, `j` for JSON, `e` to edit retained inputs, and `Esc` to go back.
- During execution, `Esc` cancels the local request. Cancellation does not undo
  work that the server may already have started. Failed mutations are not
  automatically retried.

When required flags are missing, direct commands also offer a form if stdin and
stderr are terminals. Force it with `--interactive` / `-i`, or disable prompting
with `--no-interactive`. Missing input in scripts produces an error with the
required flag names. `--accessible` uses plain prompts suitable for screen
readers; `NO_COLOR=1` disables color.

```bash
myapp authorizer-service test-operations -i
myapp authorizer-service test-operations --user alice --no-interactive
myapp explore --accessible
```

Forms preserve numeric and enum types, support JSON arrays/objects, and omit
untouched optional fields. Optional booleans offer **Leave unset**, **Yes**, and
**No**, preserving the distinction between omission and explicit false. Local
validation covers inline types, required fields, enums, numeric ranges, string
length/pattern, array sizes/items, and additionalProperties=false; the server
validates schema references and other constraints.

Operations with `destructive: true` metadata require a review in interactive mode
or an explicit `--yes` on a direct command. Request previews and copied commands
redact password/writeOnly fields and names containing password, token, or secret.
History contains operation names and favorites only, never request inputs.

### Output and automation

```bash
myapp sync aws --input input.json --output json --timeout 15s
cat input.json | myapp sync aws --input - --no-interactive --output raw
myapp users list --output table
```

Output formats are `json`, `table`, `raw`, and the default `pretty`. Tables accept
an object or an array of objects; `pretty` formats JSON and passes other responses
through. HTTP failures return a nonzero exit status and diagnostics go to stderr.
Authentication refresh messages do not pollute stdout. `--timeout` bounds operation
requests and operation discovery.

Array and object flags accept JSON. Schema fields that conflict with built-in
flags use a `field-` prefix, for example `--field-input` or `--field-output`.

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

Install completion explicitly:

```bash
myapp completion install
# Or opt in during setup:
myapp setup --name myapp --url https://app.example.com --install-completion
```

This detects zsh, bash, or fish, writes completion scripts, and configures shell
startup. Restart your shell to activate it. For manual installation:

```bash
myapp completion zsh > ~/.webdacli/completions/_myapp
myapp completion bash > ~/.webdacli/completions/myapp.bash
myapp completion fish > ~/.config/fish/completions/myapp.fish
```

## Updates

By default the CLI reports incompatible server version requirements and suggests
`myapp update`. Set `update: prompt` in config to ask before updating in an
interactive terminal, or `update: silent` to explicitly enable automatic updates.
Local help and version commands never trigger updates.

## Logo

If the server provides a `logo` URL in the operations response, the CLI displays it inline in terminals that support it (iTerm2, Kitty, WezTerm). The logo appears in help output and interactive TUI forms.

## Development

After cloning, enable the pre-push hook to run linting and tests before each push:

```bash
git config core.hooksPath .githooks
```

This runs `go vet`, `golangci-lint`, and `go test -race` automatically.

## Files

| Path | Purpose |
|---|---|
| `~/.webdacli/config.yaml` | Host mappings |
| `~/.webdacli/<name>.tok` | Encrypted tokens (only when no OS keychain is available) |
| `~/.webdacli/<name>.tok.salt` | Salt of the machine-bound token encryption (no keychain nor SSH agent) |
| `~/.webdacli/<name>.lock` | Lock held while a token is refreshed, shared by CLI processes |
| `~/.webdacli/<name>.oauth.json` | Cached OAuth server metadata |
| `~/.webdacli/<name>.operations` | Cached operations spec and the URL they run against |
| `~/.webdacli/<name>.logo` | Cached logo image |
| `~/.webdacli/<profile>.history.json` | Favorite and recent operation names |
| `~/.webdacli/completions/` | Shell completion scripts |
