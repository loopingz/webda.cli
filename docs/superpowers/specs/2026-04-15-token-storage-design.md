# Token Storage Design

## Summary

Replace plaintext token file storage with secure alternatives: system keyring (preferred), SSH agent-based encryption, or machine-bound encryption as fallback.

## Storage Priority

Tried in order during `NewTokenStore()`. The first backend that initializes successfully is used for all subsequent operations.

### 1. System Keyring

Uses macOS Keychain, Linux Secret Service (libsecret/GNOME Keyring), or Windows Credential Manager via `go-keyring`.

- Service name: `webda-cli`
- Key: `<name>` (e.g., `myapp`)
- Value: JSON blob `{"refresh_token":"...","access_token":"...","sequence":"..."}`
- Detection: attempt a test write/read/delete cycle with a sentinel key `webda-cli-probe` on first use

### 2. SSH Agent Encryption

If no system keyring is available, use SSH agent for key material.

- Connect to `SSH_AUTH_SOCK`
- List keys, use the first available key
- Sign a deterministic challenge: `"webda-cli-token-encryption-<name>"` using the agent
- Derive AES-256 key: `SHA-256(signature_bytes)`
- Encrypt token JSON with AES-256-GCM (random 12-byte nonce)
- Store as binary file `<name>.tok`: `nonce (12 bytes) || ciphertext || GCM tag (16 bytes)`
- Detection: check `SSH_AUTH_SOCK` is set and agent responds with at least one key

### 3. Machine-Bound Encryption

If no SSH agent is available, derive key from machine identity.

- Generate random 32-byte salt on first use, store in `<name>.tok.salt`
- Derive AES-256 key: `SHA-256(hostname + username + salt_bytes)`
- Same AES-256-GCM encryption and file format as SSH agent store
- Detection: always available (final fallback)

## Token Format

Current plaintext 3-line format is abandoned. New format is a JSON object:

```json
{
  "refresh_token": "...",
  "access_token": "...",
  "sequence": "..."
}
```

This JSON is stored as-is in the keyring, or encrypted on disk for SSH agent / machine-bound backends.

## No Migration

Existing plaintext `.tok` files are not read or migrated. Users re-authenticate after upgrade. Old `.tok` files are overwritten on next successful auth.

## Interface

```go
// TokenStore abstracts token persistence across backends.
type TokenStore interface {
    Load(name string) (TokenInfo, error)
    Save(name string, ti TokenInfo) error
    Delete(name string) error
}
```

Three implementations:
- `KeyringStore` — uses `go-keyring`
- `SSHAgentStore` — signs challenge via SSH agent, encrypts to disk
- `MachineStore` — derives key from machine identity, encrypts to disk

A factory function selects the best available backend:

```go
func NewTokenStore() TokenStore
```

Tries keyring, then SSH agent, then machine-bound. Returns the first that succeeds initialization.

## File Structure

| File | Responsibility |
|---|---|
| `tokenstore/store.go` | `TokenStore` interface, `TokenInfo` type, `NewTokenStore` factory |
| `tokenstore/keyring.go` | `KeyringStore` implementation |
| `tokenstore/sshagent.go` | `SSHAgentStore` — agent connection, challenge signing, AES-GCM encrypt/decrypt |
| `tokenstore/machine.go` | `MachineStore` — salt management, machine-bound key derivation, AES-GCM encrypt/decrypt |
| `tokenstore/crypto.go` | Shared AES-256-GCM encrypt/decrypt helpers (used by both SSH and machine stores) |
| `tokenstore/store_test.go` | Tests for all backends |

## Integration Points

- `main.go`: Replace `parseTokenFile` / `os.WriteFile` calls with `TokenStore.Load` / `TokenStore.Save`
- `main.go`: Replace `tokenPath()` usage in `acquireToken` and auth callback with `TokenStore`
- `webdaclient/client.go`: Replace internal `parseTokenFile` / `os.WriteFile` with `TokenStore` (passed in or token values passed at construction)

## New Dependency

- `github.com/zalando/go-keyring` — cross-platform keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager)

No new dependency for SSH agent — `golang.org/x/crypto/ssh/agent` is in the standard extended library.

## Testing

- `KeyringStore`: test with mock keyring (go-keyring provides `keyring.MockInit()` for testing)
- `SSHAgentStore`: test encrypt/decrypt roundtrip with a mock agent or by directly testing the crypto layer
- `MachineStore`: test encrypt/decrypt roundtrip with temp directory for salt/token files
- Shared crypto: test AES-GCM encrypt then decrypt produces original plaintext, test wrong key fails
- Factory: test fallback chain (mock keyring failure → check SSH agent → machine store)
