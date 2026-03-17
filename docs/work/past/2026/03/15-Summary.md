---
created: 2026-03-15
model: claude-opus-4-6
---

# March 15 — SkyKey: Cryptographic Primitive Package

Extracted all crypto into standalone `skykey` package. Implemented custom Bech32m
addresses. Unified CLIs into single `sky10` binary.

## What Was Built

### Bech32m Encoder
- BIP-350 Bech32m with custom "10" separator (instead of standard "1")
- BCH polynomial checksum, 5-bit/8-bit group conversion
- Address format: `sky10q<version_byte><encoded_pubkey><checksum>`

### Core Key Operations
- `Key` type: Ed25519 PublicKey + optional PrivateKey
- Generate, Save (0600 perms), Load
- `Address()` → `sky10q...` Bech32m encoding; `ParseAddress()` → public-only Key

### Seal/Open
- Encrypt arbitrary data for a recipient's public key
- Ephemeral ECDH (X25519) + HKDF-SHA3-256 + AES-256-GCM
- Ed25519→X25519 conversion (birational map for pub, SHA-512+clamp for priv)
- `SealFor()` convenience: seal for a `sky10q...` address string

### WrapKey/UnwrapKey + Symmetric Crypto
- Thin wrappers around Seal/Open for symmetric keys
- Encrypt/Decrypt (symmetric AES-256-GCM), GenerateSymmetricKey, DeriveKey (HKDF-SHA3-256)

### Sign/Verify
- Ed25519 Sign/Verify for messages
- SignFile/VerifyFile for streaming file signatures

### Refactor skyfs
- `skyfs/crypto.go` → thin wrappers delegating to skykey
- `skyfs/identity.go` → Identity = skykey.Key type alias
- Address format changed: `sky10://k1_<base64>` → `sky10q<bech32m>`

### Unified CLI
- `cmd/sky10/` replaces `cmd/skyfs/`
- `sky10 key generate|address|seal|open|sign|verify|export|import`
- `sky10 fs init|put|get|ls|rm|info|serve|sync|compact|gc|versions|restore|snapshots`
- Makefile builds `bin/sky10`

### Config Migration
- `~/.skyfs/` → `~/.sky10/` with auto-migration
- `identity.key` → `key.json`
- cirrus updated: launches `sky10 fs serve`, connects to `~/.sky10/skyfs.sock`

## Files Created

```
skykey/bech32m.go, bech32m_test.go
skykey/key.go, key_test.go
skykey/seal.go, seal_test.go
skykey/sign.go, sign_test.go
cmd/sky10/main.go, key.go, fs.go
```

## Tests: 219 (176 Go + 43 Swift)
