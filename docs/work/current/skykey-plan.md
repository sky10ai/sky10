# skykey — Cryptographic Primitive Package

Status: not started
Created: 2026-03-15

## Goal

Extract all cryptographic operations into a standalone `skykey` package
that every sky10 package imports. Implement custom Bech32m address
encoding with `sky10` prefix. Unify CLIs into a single `sky10` binary.

After this work:
- `skykey/` is the single source of truth for keys, encryption, signing
- `skyfs/` has zero crypto code — it imports `skykey`
- `cmd/sky10/` is the unified CLI with `key` and `fs` subcommand groups
- Identity addresses use `sky10q...` Bech32m format

## Out of Scope

- skyid (profiles, credentials, reputation) — future package on top of skykey
- skydb / skylink — future packages that will import skykey
- Agent capability scoping — handled at namespace key layer, not in skykey

---

## Milestone 1: Custom Bech32m Encoder

Implement Bech32m encoding/decoding with `10` as the separator instead
of the standard `1`. No external dependencies.

### Tasks

- [ ] `skykey/bech32m.go` — encoder/decoder:
  - [ ] Standard Bech32m BCH polynomial checksum (BIP-350)
  - [ ] 32-character alphabet: `qpzry9x8gf2tvdw0s3jn54khce6mua7l`
  - [ ] Separator: `10` (instead of standard `1`)
  - [ ] `Encode(hrp string, version byte, data []byte) (string, error)`
  - [ ] `Decode(s string) (hrp string, version byte, data []byte, err error)`
  - [ ] HRP validation: lowercase ASCII, 1-83 chars
  - [ ] Checksum: 6-character BCH error-detecting code
  - [ ] 5-bit group conversion (8-bit → 5-bit and back)
- [ ] Tests:
  - [ ] Encode → decode round-trip
  - [ ] Known test vectors (adapt from BIP-350, adjusting for `10` separator)
  - [ ] Invalid checksum detected
  - [ ] Invalid HRP rejected
  - [ ] Invalid character in data rejected
  - [ ] Version byte preserved through encode/decode
  - [ ] Empty data handled

### Acceptance

Encode/decode round-trips correctly. Checksum catches single-character
errors. Custom `10` separator works.

---

## Milestone 2: Core Key Operations

The `Key` type and fundamental operations: generate, save, load, address.

### Tasks

- [ ] `skykey/key.go`:
  - [ ] `Key` struct: `PublicKey`, `PrivateKey` (nil for public-only)
  - [ ] `Generate() (*Key, error)` — new random Ed25519 keypair
  - [ ] `FromPublicKey(pub ed25519.PublicKey) *Key` — public-only key
  - [ ] `Save(k *Key, path string) error` — JSON, 0600 permissions
  - [ ] `Load(path string) (*Key, error)` — read from disk
  - [ ] `IsPrivate() bool` — true if private key is present
- [ ] `skykey/address.go`:
  - [ ] `(k *Key) Address() string` — Bech32m `sky10q...`
  - [ ] `ParseAddress(s string) (*Key, error)` — decode to public-only Key
  - [ ] Version byte 0 = Ed25519
  - [ ] HRP = `sky`
  - [ ] Separator = `10`
  - [ ] Full format: `sky10q<encoded_pubkey><checksum>`
- [ ] Tests:
  - [ ] Generate produces valid keypair
  - [ ] Address round-trips through ParseAddress
  - [ ] Save/Load round-trip with correct permissions
  - [ ] Public-only key has nil PrivateKey
  - [ ] ParseAddress rejects invalid strings
  - [ ] Different keys produce different addresses

### Acceptance

`Generate → Address → ParseAddress` round-trips. Keys save/load correctly.
Address format is `sky10q...`.

---

## Milestone 3: Seal/Open

Encrypt arbitrary data for a recipient's public key. General-purpose
version of WrapKey — works on any size payload, not just 32-byte keys.

### Tasks

- [ ] `skykey/seal.go`:
  - [ ] `Seal(message []byte, recipientPub ed25519.PublicKey) ([]byte, error)`
    - Ed25519 pub → X25519 pub (birational map)
    - Ephemeral X25519 keypair
    - ECDH shared secret
    - HKDF-SHA3-256 derive encryption key
    - AES-256-GCM encrypt message
    - Output: `[ephemeral_pub (32) | nonce (12) | ciphertext + tag]`
  - [ ] `Open(sealed []byte, recipientPriv ed25519.PrivateKey) ([]byte, error)`
    - Ed25519 priv → X25519 priv
    - ECDH with ephemeral pub
    - HKDF derive same key
    - AES-256-GCM decrypt
  - [ ] `SealFor(message []byte, recipientAddress string) ([]byte, error)`
    - ParseAddress → Seal (convenience)
- [ ] Tests:
  - [ ] Seal/Open round-trip
  - [ ] Wrong key cannot Open
  - [ ] Empty message works
  - [ ] Large message works (1MB)
  - [ ] Sealed output is non-deterministic (random ephemeral key)
  - [ ] SealFor with sky10q... address works

### Acceptance

Any data can be encrypted for any public key. Only the corresponding
private key can decrypt it.

---

## Milestone 4: WrapKey/UnwrapKey

Specialized version of Seal for symmetric keys. Moved from skyfs/crypto.go.

### Tasks

- [ ] `skykey/wrap.go`:
  - [ ] `WrapKey(dataKey []byte, recipientPub ed25519.PublicKey) ([]byte, error)`
  - [ ] `UnwrapKey(wrapped []byte, recipientPriv ed25519.PrivateKey) ([]byte, error)`
  - [ ] Same ECDH + HKDF + AES-GCM as Seal, but optimized for 32-byte keys
  - [ ] Or: just call Seal/Open internally (simplicity over optimization)
- [ ] Tests:
  - [ ] Wrap/Unwrap round-trip
  - [ ] Wrong key fails
  - [ ] Wrapped output doesn't contain plaintext key

### Acceptance

WrapKey/UnwrapKey work identically to current skyfs implementation.
All existing skyfs tests pass after switching to skykey.

---

## Milestone 5: Sign/Verify

Ed25519 digital signatures.

### Tasks

- [ ] `skykey/sign.go`:
  - [ ] `Sign(message []byte, priv ed25519.PrivateKey) []byte`
  - [ ] `Verify(message, signature []byte, pub ed25519.PublicKey) bool`
  - [ ] `SignFile(path string, priv ed25519.PrivateKey) ([]byte, error)` — stream file
  - [ ] `VerifyFile(path string, signature []byte, pub ed25519.PublicKey) (bool, error)`
- [ ] Tests:
  - [ ] Sign/Verify round-trip
  - [ ] Tampered message fails verification
  - [ ] Wrong key fails verification
  - [ ] SignFile/VerifyFile with real files

### Acceptance

Signatures are standard Ed25519, verifiable by any Ed25519 implementation.

---

## Milestone 6: Refactor skyfs

Remove all crypto from skyfs. Import skykey instead.

### Tasks

- [ ] Delete from skyfs:
  - [ ] `skyfs/crypto.go` — Encrypt/Decrypt/WrapKey/UnwrapKey/deriveKey
  - [ ] `skyfs/identity.go` — Identity/GenerateIdentity/SaveIdentity/LoadIdentity
  - [ ] Ed25519→X25519 conversion functions
  - [ ] SHA-3 imports
- [ ] skyfs imports skykey:
  - [ ] `skyfs.Store` takes `*skykey.Key` instead of `*Identity`
  - [ ] Namespace key wrapping uses `skykey.WrapKey`/`skykey.UnwrapKey`
  - [ ] Manifest key derivation uses skykey's HKDF
  - [ ] Keep AES-256-GCM Encrypt/Decrypt in skykey (used for chunk encryption)
  - [ ] Keep DeriveFileKey — calls skykey's HKDF
- [ ] Update all tests to use `skykey.Generate()` etc.
- [ ] Update skyshare Swift code if Key type names changed
- [ ] All existing tests pass — zero behavior change

### Acceptance

`skyfs/` has no crypto imports except through `skykey/`. All 184 tests
pass. No behavior change — purely structural refactor.

---

## Milestone 7: Unified CLI

Merge `cmd/skyfs/` into `cmd/sky10/` with subcommand groups.

### Tasks

- [ ] `cmd/sky10/main.go` — top-level dispatch:
  - [ ] `sky10 key <subcommand>` → key operations
  - [ ] `sky10 fs <subcommand>` → file storage operations
  - [ ] `sky10 version`
  - [ ] `sky10 help`
- [ ] `cmd/sky10/key.go` — key subcommands:
  - [ ] `sky10 key generate` — new keypair, save to ~/.sky10/key.json
  - [ ] `sky10 key address` — print sky10q... address
  - [ ] `sky10 key address --parse sky10q...` — decode to hex public key
  - [ ] `sky10 key seal <file> --for sky10q...` — encrypt file for recipient
  - [ ] `sky10 key open <file>` — decrypt sealed file
  - [ ] `sky10 key sign <file>` — sign, output signature
  - [ ] `sky10 key verify <file> --sig <sig> --from sky10q...`
  - [ ] `sky10 key export` — export keypair
  - [ ] `sky10 key import <file>` — import keypair
- [ ] `cmd/sky10/fs.go` — file storage subcommands:
  - [ ] All current `skyfs` commands moved here
  - [ ] `sky10 fs init`, `sky10 fs put`, `sky10 fs get`, etc.
  - [ ] `sky10 fs serve`, `sky10 fs sync`, `sky10 fs compact`, `sky10 fs gc`
  - [ ] `sky10 fs versions`, `sky10 fs restore`, `sky10 fs snapshots`
- [ ] Delete `cmd/skyfs/` (replaced by `cmd/sky10/`)
- [ ] Update Makefile:
  - [ ] `build` target produces `bin/sky10` instead of `bin/skyfs`
  - [ ] Cross-compilation targets updated
- [ ] Update README with new CLI syntax
- [ ] Update skyshare DaemonManager to launch `sky10 fs serve`

### Acceptance

`sky10 key generate` and `sky10 fs put` both work. Old `skyfs` binary
gone. Makefile, README, skyshare all updated.

---

## Milestone 8: Config Migration

Move config from `~/.skyfs/` to `~/.sky10/`.

### Tasks

- [ ] New config directory: `~/.sky10/`
  - [ ] `~/.sky10/key.json` — keypair (was `~/.skyfs/identity.key`)
  - [ ] `~/.sky10/config.json` — storage config (was `~/.skyfs/config.json`)
  - [ ] `~/.sky10/skyfs.sock` — RPC socket
  - [ ] `~/.sky10/index.db` — local index
- [ ] Auto-migration: if `~/.skyfs/` exists and `~/.sky10/` doesn't, move it
- [ ] Update all paths in `internal/config/`
- [ ] Update skyshare RPCClient socket path
- [ ] Tests:
  - [ ] New paths work
  - [ ] Migration from old paths works
  - [ ] Config round-trip

### Acceptance

Everything uses `~/.sky10/`. Old `~/.skyfs/` auto-migrates.

---

## Order of Implementation

```
1. Bech32m encoder     no dependencies, standalone
2. Core key ops        depends on 1 (address encoding)
3. Seal/Open           depends on 2 (key types)
4. WrapKey             depends on 3 (uses same ECDH+HKDF)
5. Sign/Verify         depends on 2 (key types)
├── 3-5 can parallelize after 2
6. Refactor skyfs      depends on 2-4 (imports skykey)
7. Unified CLI         depends on 6 (new binary structure)
8. Config migration    depends on 7 (new paths)
```

Milestones 1-2 are the foundation. 3-5 are independent operations.
6-8 are the integration and cleanup.
