# Secure Token Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace plaintext token storage with secure backends: system keyring, SSH agent encryption, or machine-bound encryption.

**Architecture:** A `TokenStore` interface with three implementations (keyring, SSH agent, machine-bound) behind a factory that selects the best available backend. Shared AES-256-GCM crypto helpers for the two file-based backends. Main and webdaclient are updated to use the store instead of raw file I/O.

**Tech Stack:** Go, `go-keyring`, `golang.org/x/crypto/ssh/agent`, AES-256-GCM

---

## File Structure

| File | Responsibility |
|---|---|
| `tokenstore/store.go` | `TokenStore` interface, `TokenInfo` type, `NewTokenStore` factory, JSON marshal/unmarshal helpers |
| `tokenstore/store_test.go` | Factory tests, integration roundtrip tests |
| `tokenstore/crypto.go` | `Encrypt(key, plaintext)`, `Decrypt(key, ciphertext)` using AES-256-GCM |
| `tokenstore/crypto_test.go` | Encrypt/decrypt roundtrip, wrong key rejection |
| `tokenstore/keyring.go` | `KeyringStore` implementation |
| `tokenstore/keyring_test.go` | Tests using `go-keyring` mock |
| `tokenstore/sshagent.go` | `SSHAgentStore` — agent signing, key derivation, encrypted file I/O |
| `tokenstore/sshagent_test.go` | Encrypt/decrypt roundtrip (testing crypto layer directly) |
| `tokenstore/machine.go` | `MachineStore` — salt management, machine-bound key derivation, encrypted file I/O |
| `tokenstore/machine_test.go` | Full roundtrip with temp directory |
| `main.go` | Replace `parseTokenFile`/`os.WriteFile` with `TokenStore` |
| `webdaclient/client.go` | Accept `TokenStore` instead of file path, use it for persist on refresh |

---

### Task 1: AES-256-GCM crypto helpers

**Files:**
- Create: `tokenstore/crypto.go`
- Create: `tokenstore/crypto_test.go`

- [ ] **Step 1: Write failing tests**

Create `tokenstore/crypto_test.go`:

```go
package tokenstore

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"refresh_token":"abc","access_token":"def","sequence":"1"}`)

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext should differ from plaintext")
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("roundtrip failed: got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ciphertext, err := Encrypt(key1, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Decrypt(key2, ciphertext)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestEncryptInvalidKeyLength(t *testing.T) {
	_, err := Encrypt([]byte("short"), []byte("data"))
	if err == nil {
		t.Fatal("expected error for invalid key length")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestEncrypt|TestDecrypt" -v ./tokenstore/...`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement crypto helpers**

Create `tokenstore/crypto.go`:

```go
package tokenstore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Encrypt encrypts plaintext with AES-256-GCM using the given 32-byte key.
// Returns nonce (12 bytes) || ciphertext || GCM tag (16 bytes).
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts data produced by Encrypt using the given 32-byte key.
func Decrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
```

- [ ] **Step 4: Run tests**

Run: `/usr/local/go/bin/go test -run "TestEncrypt|TestDecrypt" -v ./tokenstore/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tokenstore/crypto.go tokenstore/crypto_test.go
git commit -m "feat: add AES-256-GCM encrypt/decrypt helpers for token storage"
```

---

### Task 2: TokenStore interface and TokenInfo type

**Files:**
- Create: `tokenstore/store.go`

- [ ] **Step 1: Create the interface and types**

Create `tokenstore/store.go`:

```go
package tokenstore

import "encoding/json"

// TokenInfo holds the token data persisted across sessions.
type TokenInfo struct {
	RefreshToken string `json:"refresh_token"`
	AccessToken  string `json:"access_token"`
	Sequence     string `json:"sequence"`
}

// TokenStore abstracts token persistence across backends.
type TokenStore interface {
	Load(name string) (TokenInfo, error)
	Save(name string, ti TokenInfo) error
	Delete(name string) error
}

// MarshalTokenInfo serializes TokenInfo to JSON.
func MarshalTokenInfo(ti TokenInfo) ([]byte, error) {
	return json.Marshal(ti)
}

// UnmarshalTokenInfo deserializes TokenInfo from JSON.
func UnmarshalTokenInfo(data []byte) (TokenInfo, error) {
	var ti TokenInfo
	err := json.Unmarshal(data, &ti)
	return ti, err
}
```

- [ ] **Step 2: Verify it compiles**

Run: `/usr/local/go/bin/go build ./tokenstore/...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add tokenstore/store.go
git commit -m "feat: add TokenStore interface and TokenInfo type"
```

---

### Task 3: KeyringStore implementation

**Files:**
- Create: `tokenstore/keyring.go`
- Create: `tokenstore/keyring_test.go`

- [ ] **Step 1: Add go-keyring dependency**

Run: `/usr/local/go/bin/go get github.com/zalando/go-keyring@latest`

- [ ] **Step 2: Write failing tests**

Create `tokenstore/keyring_test.go`:

```go
package tokenstore

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeyringStore_SaveLoad(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	ti := TokenInfo{RefreshToken: "rt", AccessToken: "at", Sequence: "42"}
	if err := store.Save("test-app", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load("test-app")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "rt" || loaded.AccessToken != "at" || loaded.Sequence != "42" {
		t.Fatalf("roundtrip failed: got %+v", loaded)
	}
}

func TestKeyringStore_LoadMissing(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestKeyringStore_Delete(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}

	ti := TokenInfo{RefreshToken: "rt", AccessToken: "at", Sequence: "1"}
	store.Save("del-test", ti)

	if err := store.Delete("del-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestKeyringStore_Available(t *testing.T) {
	keyring.MockInit()
	store := &KeyringStore{}
	if !store.Available() {
		t.Fatal("expected mock keyring to be available")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestKeyringStore" -v ./tokenstore/...`
Expected: FAIL — `KeyringStore` not defined

- [ ] **Step 4: Implement KeyringStore**

Create `tokenstore/keyring.go`:

```go
package tokenstore

import (
	"github.com/zalando/go-keyring"
)

const serviceName = "webda-cli"

// KeyringStore persists tokens in the system keyring (macOS Keychain,
// Linux Secret Service, Windows Credential Manager).
type KeyringStore struct{}

// Available returns true if the system keyring is accessible.
func (s *KeyringStore) Available() bool {
	const probe = "webda-cli-probe"
	if err := keyring.Set(serviceName, probe, "test"); err != nil {
		return false
	}
	keyring.Delete(serviceName, probe)
	return true
}

func (s *KeyringStore) Load(name string) (TokenInfo, error) {
	data, err := keyring.Get(serviceName, name)
	if err != nil {
		return TokenInfo{}, err
	}
	return UnmarshalTokenInfo([]byte(data))
}

func (s *KeyringStore) Save(name string, ti TokenInfo) error {
	data, err := MarshalTokenInfo(ti)
	if err != nil {
		return err
	}
	return keyring.Set(serviceName, name, string(data))
}

func (s *KeyringStore) Delete(name string) error {
	return keyring.Delete(serviceName, name)
}
```

- [ ] **Step 5: Run tests**

Run: `/usr/local/go/bin/go test -run "TestKeyringStore" -v ./tokenstore/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tokenstore/keyring.go tokenstore/keyring_test.go go.mod go.sum
git commit -m "feat: add KeyringStore for system keyring token storage"
```

---

### Task 4: SSHAgentStore implementation

**Files:**
- Create: `tokenstore/sshagent.go`
- Create: `tokenstore/sshagent_test.go`

- [ ] **Step 1: Add x/crypto dependency**

Run: `/usr/local/go/bin/go get golang.org/x/crypto@latest`

- [ ] **Step 2: Write failing tests**

Create `tokenstore/sshagent_test.go`:

```go
package tokenstore

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestSSHAgentStore_DeriveKeyAndRoundtrip(t *testing.T) {
	// Test the crypto roundtrip with a known key (simulating what the agent would produce)
	dir := t.TempDir()
	fakeSignature := []byte("fake-ssh-agent-signature-for-test-purposes-1234")
	key := sha256.Sum256(fakeSignature)

	store := &SSHAgentStore{
		configDir:  dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}

	ti := TokenInfo{RefreshToken: "ssh-rt", AccessToken: "ssh-at", Sequence: "7"}
	if err := store.Save("ssh-test", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists and is not plaintext
	data, _ := os.ReadFile(filepath.Join(dir, "ssh-test.tok"))
	if len(data) == 0 {
		t.Fatal("expected encrypted file to exist")
	}
	if string(data) == `{"refresh_token":"ssh-rt","access_token":"ssh-at","sequence":"7"}` {
		t.Fatal("file should be encrypted, not plaintext")
	}

	loaded, err := store.Load("ssh-test")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "ssh-rt" || loaded.AccessToken != "ssh-at" || loaded.Sequence != "7" {
		t.Fatalf("roundtrip failed: got %+v", loaded)
	}
}

func TestSSHAgentStore_Delete(t *testing.T) {
	dir := t.TempDir()
	key := sha256.Sum256([]byte("test-key"))
	store := &SSHAgentStore{
		configDir: dir,
		deriveKey: func(name string) ([32]byte, error) { return key, nil },
	}

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	store.Save("del-test", ti)

	if err := store.Delete("del-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestSSHAgentStore" -v ./tokenstore/...`
Expected: FAIL — `SSHAgentStore` not defined

- [ ] **Step 4: Implement SSHAgentStore**

Create `tokenstore/sshagent.go`:

```go
package tokenstore

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// SSHAgentStore encrypts tokens using key material derived from an SSH agent signature.
type SSHAgentStore struct {
	configDir string
	deriveKey func(name string) ([32]byte, error) // injectable for testing
}

// NewSSHAgentStore creates an SSHAgentStore if an SSH agent is available.
func NewSSHAgentStore(configDir string) (*SSHAgentStore, error) {
	s := &SSHAgentStore{configDir: configDir}
	s.deriveKey = s.deriveKeyFromAgent
	// Verify agent is reachable and has keys
	if _, err := s.getAgentKey(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SSHAgentStore) getAgentKey() (ssh.PublicKey, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to ssh agent: %w", err)
	}
	defer conn.Close()
	ag := agent.NewClient(conn)
	keys, err := ag.List()
	if err != nil {
		return nil, fmt.Errorf("cannot list agent keys: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys in ssh agent")
	}
	return keys[0], nil
}

func (s *SSHAgentStore) deriveKeyFromAgent(name string) ([32]byte, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return [32]byte{}, err
	}
	defer conn.Close()
	ag := agent.NewClient(conn)
	keys, err := ag.List()
	if err != nil || len(keys) == 0 {
		return [32]byte{}, fmt.Errorf("no agent keys available")
	}
	challenge := []byte("webda-cli-token-encryption-" + name)
	sig, err := ag.Sign(keys[0], challenge)
	if err != nil {
		return [32]byte{}, fmt.Errorf("agent sign failed: %w", err)
	}
	return sha256.Sum256(sig.Blob), nil
}

func (s *SSHAgentStore) tokenFile(name string) string {
	return filepath.Join(s.configDir, name+".tok")
}

func (s *SSHAgentStore) Load(name string) (TokenInfo, error) {
	data, err := os.ReadFile(s.tokenFile(name))
	if err != nil {
		return TokenInfo{}, err
	}
	key, err := s.deriveKey(name)
	if err != nil {
		return TokenInfo{}, err
	}
	plaintext, err := Decrypt(key[:], data)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("decrypt failed: %w", err)
	}
	return UnmarshalTokenInfo(plaintext)
}

func (s *SSHAgentStore) Save(name string, ti TokenInfo) error {
	plaintext, err := MarshalTokenInfo(ti)
	if err != nil {
		return err
	}
	key, err := s.deriveKey(name)
	if err != nil {
		return err
	}
	ciphertext, err := Encrypt(key[:], plaintext)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.tokenFile(name), ciphertext, 0o600)
}

func (s *SSHAgentStore) Delete(name string) error {
	return os.Remove(s.tokenFile(name))
}
```

- [ ] **Step 5: Run tests**

Run: `/usr/local/go/bin/go test -run "TestSSHAgentStore" -v ./tokenstore/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tokenstore/sshagent.go tokenstore/sshagent_test.go go.mod go.sum
git commit -m "feat: add SSHAgentStore for SSH agent-based token encryption"
```

---

### Task 5: MachineStore implementation

**Files:**
- Create: `tokenstore/machine.go`
- Create: `tokenstore/machine_test.go`

- [ ] **Step 1: Write failing tests**

Create `tokenstore/machine_test.go`:

```go
package tokenstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMachineStore_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := NewMachineStore(dir)

	ti := TokenInfo{RefreshToken: "mrt", AccessToken: "mat", Sequence: "99"}
	if err := store.Save("machine-test", ti); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Salt file should exist
	if _, err := os.Stat(filepath.Join(dir, "machine-test.tok.salt")); err != nil {
		t.Fatalf("expected salt file: %v", err)
	}

	// Token file should be encrypted (not plaintext JSON)
	data, _ := os.ReadFile(filepath.Join(dir, "machine-test.tok"))
	if string(data) == `{"refresh_token":"mrt","access_token":"mat","sequence":"99"}` {
		t.Fatal("file should be encrypted, not plaintext")
	}

	loaded, err := store.Load("machine-test")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.RefreshToken != "mrt" || loaded.AccessToken != "mat" || loaded.Sequence != "99" {
		t.Fatalf("roundtrip failed: got %+v", loaded)
	}
}

func TestMachineStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewMachineStore(dir)

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	store.Save("del-test", ti)

	if err := store.Delete("del-test"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	_, err := store.Load("del-test")
	if err == nil {
		t.Fatal("expected error after delete")
	}
	// Salt file should also be removed
	if _, err := os.Stat(filepath.Join(dir, "del-test.tok.salt")); err == nil {
		t.Fatal("expected salt file to be removed")
	}
}

func TestMachineStore_SaltReuse(t *testing.T) {
	dir := t.TempDir()
	store := NewMachineStore(dir)

	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	store.Save("reuse-test", ti)

	salt1, _ := os.ReadFile(filepath.Join(dir, "reuse-test.tok.salt"))

	// Save again — salt should be reused
	ti.AccessToken = "b"
	store.Save("reuse-test", ti)

	salt2, _ := os.ReadFile(filepath.Join(dir, "reuse-test.tok.salt"))
	if string(salt1) != string(salt2) {
		t.Fatal("salt should be reused across saves")
	}

	loaded, _ := store.Load("reuse-test")
	if loaded.AccessToken != "b" {
		t.Fatalf("expected updated token, got %+v", loaded)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestMachineStore" -v ./tokenstore/...`
Expected: FAIL — `MachineStore` not defined

- [ ] **Step 3: Implement MachineStore**

Create `tokenstore/machine.go`:

```go
package tokenstore

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// MachineStore encrypts tokens with a key derived from machine identity + random salt.
type MachineStore struct {
	configDir string
}

// NewMachineStore creates a MachineStore.
func NewMachineStore(configDir string) *MachineStore {
	return &MachineStore{configDir: configDir}
}

func (s *MachineStore) tokenFile(name string) string {
	return filepath.Join(s.configDir, name+".tok")
}

func (s *MachineStore) saltFile(name string) string {
	return filepath.Join(s.configDir, name+".tok.salt")
}

func (s *MachineStore) deriveKey(name string) ([32]byte, error) {
	salt, err := s.loadOrCreateSalt(name)
	if err != nil {
		return [32]byte{}, err
	}
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME") // Windows
	}
	material := fmt.Sprintf("%s:%s:%s", hostname, username, salt)
	return sha256.Sum256([]byte(material)), nil
}

func (s *MachineStore) loadOrCreateSalt(name string) ([]byte, error) {
	path := s.saltFile(name)
	if data, err := os.ReadFile(path); err == nil && len(data) == 32 {
		return data, nil
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, salt, 0o600); err != nil {
		return nil, err
	}
	return salt, nil
}

func (s *MachineStore) Load(name string) (TokenInfo, error) {
	data, err := os.ReadFile(s.tokenFile(name))
	if err != nil {
		return TokenInfo{}, err
	}
	key, err := s.deriveKey(name)
	if err != nil {
		return TokenInfo{}, err
	}
	plaintext, err := Decrypt(key[:], data)
	if err != nil {
		return TokenInfo{}, fmt.Errorf("decrypt failed: %w", err)
	}
	return UnmarshalTokenInfo(plaintext)
}

func (s *MachineStore) Save(name string, ti TokenInfo) error {
	plaintext, err := MarshalTokenInfo(ti)
	if err != nil {
		return err
	}
	key, err := s.deriveKey(name)
	if err != nil {
		return err
	}
	ciphertext, err := Encrypt(key[:], plaintext)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.configDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.tokenFile(name), ciphertext, 0o600)
}

func (s *MachineStore) Delete(name string) error {
	os.Remove(s.saltFile(name)) // best-effort
	return os.Remove(s.tokenFile(name))
}
```

- [ ] **Step 4: Run tests**

Run: `/usr/local/go/bin/go test -run "TestMachineStore" -v ./tokenstore/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tokenstore/machine.go tokenstore/machine_test.go
git commit -m "feat: add MachineStore for machine-bound token encryption"
```

---

### Task 6: NewTokenStore factory

**Files:**
- Modify: `tokenstore/store.go`
- Create: `tokenstore/store_test.go`

- [ ] **Step 1: Write failing tests**

Create `tokenstore/store_test.go`:

```go
package tokenstore

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestNewTokenStore_PrefersKeyring(t *testing.T) {
	keyring.MockInit()
	store := NewTokenStore(t.TempDir())
	if _, ok := store.(*KeyringStore); !ok {
		t.Fatalf("expected KeyringStore when keyring is available, got %T", store)
	}
}

func TestNewTokenStore_FallbackToMachine(t *testing.T) {
	// Without mock keyring init and without SSH agent, should fall back to MachineStore.
	// Note: this test may get SSHAgentStore if the test runner has SSH_AUTH_SOCK.
	// We test the factory returns a non-nil store.
	dir := t.TempDir()
	store := NewTokenStore(dir)
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestMarshalUnmarshalTokenInfo(t *testing.T) {
	ti := TokenInfo{RefreshToken: "r", AccessToken: "a", Sequence: "1"}
	data, err := MarshalTokenInfo(ti)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := UnmarshalTokenInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != ti {
		t.Fatalf("roundtrip failed: got %+v, want %+v", loaded, ti)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `/usr/local/go/bin/go test -run "TestNewTokenStore|TestMarshalUnmarshal" -v ./tokenstore/...`
Expected: FAIL — `NewTokenStore` not defined

- [ ] **Step 3: Add factory function to `store.go`**

Append to `tokenstore/store.go`:

```go
// NewTokenStore returns the best available token storage backend.
// Priority: system keyring > SSH agent > machine-bound encryption.
func NewTokenStore(configDir string) TokenStore {
	// Try keyring
	ks := &KeyringStore{}
	if ks.Available() {
		return ks
	}

	// Try SSH agent
	if ss, err := NewSSHAgentStore(configDir); err == nil {
		return ss
	}

	// Fallback to machine-bound
	return NewMachineStore(configDir)
}
```

- [ ] **Step 4: Run all tokenstore tests**

Run: `/usr/local/go/bin/go test -v ./tokenstore/...`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add tokenstore/store.go tokenstore/store_test.go
git commit -m "feat: add NewTokenStore factory with keyring > ssh-agent > machine fallback"
```

---

### Task 7: Integrate TokenStore into main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Add tokenstore import and global variable**

Add `"github.com/loopingz/webda-cli/tokenstore"` to the import block in `main.go`.

Add a global variable near the existing `var cli *webdaclient.Client`:

```go
var tokenStore tokenstore.TokenStore
```

- [ ] **Step 2: Initialize tokenStore in main()**

In `main()`, after the config is loaded and `baseURL` is resolved (around line after `baseURL := cfg[invoked]`), add:

```go
	tokenStore = tokenstore.NewTokenStore(configDir())
```

- [ ] **Step 3: Replace `parseTokenFile` usage in `acquireToken`**

In `acquireToken`, replace:
```go
p := tokenPath(name)
if ti, err := parseTokenFile(p); err == nil && ti.AccessToken != "" {
    return ti.AccessToken, nil
}
```

With:
```go
if ti, err := tokenStore.Load(name); err == nil && ti.AccessToken != "" {
    return ti.AccessToken, nil
}
```

- [ ] **Step 4: Replace token persist in auth callback**

In the auth callback handler inside `acquireToken`, replace:
```go
// persist all
content := refresh + "\n" + access + "\n" + fmt.Sprintf("%d", seq) + "\n"
if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
```

With:
```go
// persist all
ti := tokenstore.TokenInfo{RefreshToken: refresh, AccessToken: access, Sequence: fmt.Sprintf("%d", seq)}
if err := tokenStore.Save(name, ti); err != nil {
```

- [ ] **Step 5: Replace `parseTokenFile` in whoami command**

In the `whoami` command handler, replace:
```go
ti, err := parseTokenFile(tokenPath(invoked))
if err != nil {
    return err
}
if ti.AccessToken == "" {
    return errors.New("no access token; run auth")
}
```

With:
```go
ti, err := tokenStore.Load(invoked)
if err != nil {
    return errors.New("no access token; run auth")
}
_ = ti // whoami uses fetchCurrentUser, not the token directly
```

- [ ] **Step 6: Replace token deletion in auth command**

In the `auth` command handler, replace:
```go
_ = os.Remove(tokenPath(invoked))
```

With:
```go
_ = tokenStore.Delete(invoked)
```

- [ ] **Step 7: Remove old `parseTokenFile` and `tokenPath` from main.go**

Delete the `parseTokenFile` function (lines ~85-105) and the `tokenPath` function (line ~74). Also remove `tokenExt` from the const block. Keep `opsPath` and `opsExt`.

Remove the now-unused `TokenInfo` struct from main.go (it's now in the `tokenstore` package).

- [ ] **Step 8: Run tests and build**

Run: `/usr/local/go/bin/go vet ./... && /usr/local/go/bin/go test -v ./... && /usr/local/go/bin/go build -o webda-cli .`
Expected: ALL PASS, builds OK

- [ ] **Step 9: Commit**

```bash
git add main.go
git commit -m "feat: integrate TokenStore into main.go, remove plaintext token handling"
```

---

### Task 8: Integrate TokenStore into webdaclient

**Files:**
- Modify: `webdaclient/client.go`

- [ ] **Step 1: Update Client struct and New function**

Replace the `tokenPath` field and file-based token loading with a `TokenStore`. Update the `Client` struct:

```go
type Client struct {
	BaseURL      string
	Name         string
	store        tokenstore.TokenStore
	mu           sync.RWMutex
	RefreshToken string
	AccessToken  string
	Sequence     string
	Expiry       time.Time
	stopCh       chan struct{}
	refreshedCh  chan struct{}
}
```

Update `New` to accept a `TokenStore`:

```go
func New(name, baseURL string, store tokenstore.TokenStore) (*Client, error) {
	c := &Client{BaseURL: strings.TrimRight(baseURL, "/"), Name: name, store: store, stopCh: make(chan struct{}), refreshedCh: make(chan struct{})}
	if ti, err := store.Load(name); err == nil {
		c.RefreshToken = ti.RefreshToken
		c.AccessToken = ti.AccessToken
		c.Sequence = ti.Sequence
		c.Expiry = time.Now().Add(defaultTTL)
	}
	go c.backgroundLoop()
	return c, nil
}
```

- [ ] **Step 2: Update refresh to use TokenStore**

In the `refresh` method, replace:
```go
content := c.RefreshToken + "\n" + c.AccessToken + "\n" + c.Sequence + "\n"
_ = os.WriteFile(c.tokenPath, []byte(content), 0o600)
```

With:
```go
ti := tokenstore.TokenInfo{RefreshToken: c.RefreshToken, AccessToken: c.AccessToken, Sequence: c.Sequence}
_ = c.store.Save(c.Name, ti)
```

- [ ] **Step 3: Remove old helpers from client.go**

Delete from `webdaclient/client.go`:
- `func userHome()` (line ~156)
- `func configDir()` (line ~157)
- `func tokenPath()` (line ~158)
- `type tokenInfo` struct (lines ~160-164)
- `func parseTokenFile()` (lines ~166-end)
- `configDirName` and `tokenExt` constants

Add the tokenstore import:
```go
"github.com/loopingz/webda-cli/tokenstore"
```

Remove now-unused imports (`path/filepath`, `strings` if unused after cleanup).

- [ ] **Step 4: Update main.go to pass tokenStore to webdaclient.New**

In `main.go`, change:
```go
cli, err = webdaclient.New(invoked, baseURL)
```

To:
```go
cli, err = webdaclient.New(invoked, baseURL, tokenStore)
```

- [ ] **Step 5: Run all tests and build**

Run: `/usr/local/go/bin/go vet ./... && /usr/local/go/bin/go test -v ./... && /usr/local/go/bin/go build -o webda-cli .`
Expected: ALL PASS, builds OK

- [ ] **Step 6: Commit**

```bash
git add webdaclient/client.go main.go
git commit -m "feat: integrate TokenStore into webdaclient, remove plaintext file handling"
```

---

### Task 9: Final verification

**Files:**
- All

- [ ] **Step 1: Run full test suite**

Run: `/usr/local/go/bin/go test -count=1 -race -v ./...`
Expected: ALL PASS

- [ ] **Step 2: Run vet**

Run: `/usr/local/go/bin/go vet ./...`
Expected: clean

- [ ] **Step 3: Build**

Run: `/usr/local/go/bin/go build -o webda-cli .`
Expected: success

- [ ] **Step 4: Verify no plaintext token code remains**

Search for old patterns:
```bash
grep -r "parseTokenFile\|\.tok\"\|tokenExt\|tokenPath" --include="*.go" . | grep -v tokenstore | grep -v _test.go | grep -v ".operations"
```
Expected: only `opsPath`/`opsExt` references remain, no token file references outside `tokenstore/`.
