# Versioning & Auto-Update Design

## Summary

Bake a version into the CLI binary at build time, expose server version from the `/operations` response, and support configurable auto-update when the server declares a required CLI version range.

## CLI Version

Set at build time via `-ldflags`:

- `version` — semver string (e.g., `1.2.3`), defaults to `dev`
- `commit` — git short SHA, defaults to `unknown`
- `buildDate` — ISO date, defaults to `unknown`

### Version Command

`myapp version` outputs:

```
myapp version 1.2.3 (commit: abc1234, built: 2026-04-15)
server: 2.1.0 (https://app.example.com)
```

If the server is unreachable or no token exists, only the local line is shown.

### Build Integration

The release workflow sets ldflags:

```
go build -ldflags="-s -w -X main.version=${TAG} -X main.commit=${SHA} -X main.buildDate=${DATE}"
```

release-please provides the tag. The CI workflow extracts commit SHA and date.

## Server Response Extensions

The `/operations` response gains optional fields:

```json
{
  "operations": { ... },
  "logo": "https://...",
  "version": "2.1.0",
  "cli": {
    "version_range": ">=1.2.0",
    "download_url": "https://github.com/loopingz/webda-cli/releases"
  }
}
```

- `version` — server's own version (informational)
- `cli.version_range` — semver constraint the CLI must satisfy (e.g., `>=1.2.0`, `>=1.0.0 <2.0.0`)
- `cli.download_url` — base URL for downloading updates. Defaults to `https://github.com/loopingz/webda-cli/releases` if not provided.

All fields are optional. Missing `cli` or `cli.version_range` means no version check.

## Update Mode

Configured per-client in `~/.webdacli/config.yaml`:

```yaml
myapp: https://app.example.com
update: silent
```

Three modes:

- **silent** (default) — download, replace binary, re-exec the same command seamlessly
- **prompt** — ask `Update available (1.1.0 → 1.2.3). Update now? [Y/n]`; if declined, continue on old version
- **warn** — print `Warning: CLI version 1.1.0 is outdated (server requires >=1.2.0). Run 'myapp update' to upgrade.` and continue on old version

The server does not control the update mode — only the client config does. Default is `silent` if not specified.

## Update Flow

Triggered after fetching operations, before executing any command:

1. Parse `cli.version_range` from the operations response
2. Compare against the baked-in `version` using semver constraint matching
3. If `version` is `dev`, skip the check (development build)
4. If version satisfies the range: continue normally
5. If version does not satisfy the range:
   a. Determine update mode from client config (default: `silent`)
   b. For `warn` mode: print warning, continue
   c. For `prompt` mode: ask user, if declined continue
   d. For `silent` or `prompt` (accepted): perform update

### Performing the Update

1. Determine download source: `cli.download_url` from server, or default GitHub releases URL
2. If GitHub releases: query `GET https://api.github.com/repos/loopingz/webda-cli/releases/latest` to find the latest release tag and assets
3. Find asset matching `webda-cli-{GOOS}-{GOARCH}` (append `.exe` on Windows)
4. Download the asset to a temp file in the same directory as the running binary
5. Make the temp file executable (`chmod 0755`)
6. Replace the running binary: rename temp file over the current executable path (`os.Executable()`)
7. Re-exec: `syscall.Exec` with the same `os.Args` and `os.Environ()` — the current process is replaced by the new binary running the same command

### Edge Cases

- Binary path not writable (e.g., installed via package manager): print error, suggest manual update
- `version == "dev"`: skip version check entirely
- No `cli` field in response: skip version check
- GitHub API rate limit: print warning, continue on old version
- Download failure: print error, continue on old version

## Explicit Update Command

`myapp update` — manually checks for and installs the latest version regardless of server version range. Uses the same download mechanism.

## File Structure

| File | Responsibility |
|---|---|
| `version.go` | `version`, `commit`, `buildDate` vars, `versionCmd` cobra command, `printVersion` |
| `updater/updater.go` | `CheckAndUpdate(currentVersion, constraint, mode, downloadURL)`, download/replace/re-exec logic |
| `updater/updater_test.go` | Tests with httptest for GitHub API mock |
| `main.go` | Parse `cli`/`version` from operations response, call updater, add `version` and `update` commands |

## New Dependency

- `github.com/Masterminds/semver/v3` — semver parsing and constraint evaluation

## Testing

- Version command output formatting (with and without server version)
- Semver constraint matching (satisfied, not satisfied, dev version skip)
- Update flow with mock GitHub API (download asset selection by OS/arch)
- Re-exec is not tested in unit tests (it replaces the process); tested manually
- Config parsing for update mode
