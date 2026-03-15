---
created: 2026-03-15
model: claude-opus-4-6
---

# SkyKey — Cryptographic Primitive Package

## Problems Solved

### Milestone 1: Custom Bech32m Encoder
- Implemented BIP-350 Bech32m with custom "10" separator (instead of standard "1")
- BCH polynomial checksum for error detection
- 5-bit/8-bit group conversion
- sky10q... address format: HRP="sky", separator="10", version byte + encoded key + checksum

### Milestone 2: Core Key Operations
- Key type: Ed25519 PublicKey + optional PrivateKey
- Generate, Save (0600 perms), Load
- Address() → sky10q... Bech32m encoding
- ParseAddress() → decode back to public-only Key
- Version byte 0 = Ed25519

### Milestone 3: Seal/Open
- Encrypt arbitrary data for a recipient's public key
- Ephemeral ECDH (X25519) + HKDF-SHA3-256 + AES-256-GCM
- Ed25519 → X25519 conversion (birational map for pub, SHA-512+clamp for priv)
- SealFor() convenience: seal for a sky10q... address string

### Milestone 4: WrapKey/UnwrapKey
- Thin wrappers around Seal/Open for symmetric keys
- Also: Encrypt/Decrypt (symmetric AES-256-GCM)
- GenerateSymmetricKey, DeriveKey (HKDF-SHA3-256)

### Milestone 5: Sign/Verify
- Ed25519 Sign/Verify for messages
- SignFile/VerifyFile for streaming file signatures

### Milestone 6: Refactor skyfs
- skyfs/crypto.go → thin wrappers delegating to skykey
- skyfs/identity.go → Identity = skykey.Key type alias
- skyfs/keys.go → uses skykey.DeriveKey
- All .ID() calls → .Address() (Bech32m)
- Address format: sky10://k1_<base64> → sky10q<bech32m>

### Milestone 7: Unified CLI
- cmd/sky10/ replaces cmd/skyfs/
- sky10 key generate|address|seal|open|sign|verify|export|import
- sky10 fs init|put|get|ls|rm|info|serve|sync|compact|gc|versions|restore|snapshots
- Makefile builds bin/sky10

### Milestone 8: Config Migration
- ~/.skyfs/ → ~/.sky10/ with auto-migration
- identity.key → key.json
- skyshare updated: launches "sky10 fs serve", connects to ~/.sky10/skyfs.sock

## Files Created

```
skykey/bech32m.go, bech32m_test.go    custom Bech32m encoder/decoder
skykey/key.go, key_test.go            Key type, Generate, Save, Load, Address
skykey/seal.go, seal_test.go          Seal/Open, WrapKey, Encrypt/Decrypt, DeriveKey
skykey/sign.go, sign_test.go          Sign/Verify, SignFile/VerifyFile
cmd/sky10/main.go                     unified CLI entry point
cmd/sky10/key.go                      key subcommands
cmd/sky10/fs.go                       fs subcommands (moved from cmd/skyfs/)
```

## Test Count

219 tests (176 Go + 43 Swift). All passing.
