# Storage Schema

> This document describes how skyfs versions its encrypted storage format.
> It is the authoritative reference for the schema — update it whenever
> the schema changes.

## Current Schema: v1.0.0

```json
{
  "version": "1.0.0",
  "hash_algorithm": "sha3-256",
  "kdf": "hkdf-sha3-256",
  "cipher": "aes-256-gcm",
  "key_wrap": "ephemeral-ecdh-x25519-hkdf-sha3-256-aes-256-gcm"
}
```

### Algorithms

| Component | Algorithm | Notes |
|-----------|-----------|-------|
| Content hash | SHA3-256 | Chunk hashes, content addressing, dedup |
| Key derivation | HKDF-SHA3-256 | Wrapping keys, file keys, manifest key |
| Symmetric encryption | AES-256-GCM | Chunk encryption, manifest encryption, op encryption |
| Key wrapping | Ephemeral ECDH (X25519) + HKDF-SHA3-256 + AES-256-GCM | Namespace key wrapping for identities |
| Identity | Ed25519 | Signing, identity. Converted to X25519 for ECDH. |
| Ed25519 → X25519 | SHA-512 + clamp (RFC 8032) | Required by spec, cannot change |

### Blob Format

Every encrypted blob starts with a 4-byte header:

```
Byte 0: 'S' (0x53)
Byte 1: 'K' (0x4B)
Byte 2: 'Y' (0x59)
Byte 3: schema major version (0x01 for v1.x.x)
Byte 4+: [nonce (12 bytes) | AES-256-GCM ciphertext + auth tag]
```

The `SKY` magic bytes allow any tool to identify a skyfs blob. The major
version byte tells the tool which algorithms were used. Legacy blobs
(created before versioning) have no header — the absence of the `SKY`
magic indicates legacy format.

### Bucket Layout

```
s3://bucket/
  sky10.schema                        ← unencrypted JSON, written on init
  ops/                                ← encrypted ops (SKY header + AES-GCM)
  manifests/                          ← encrypted snapshots (SKY header + AES-GCM)
  blobs/                              ← encrypted chunks (SKY header + AES-GCM)
  packs/                              ← packed encrypted chunks
  pack-index.enc                      ← encrypted pack index
  keys/namespaces/                    ← wrapped namespace keys
```

## Versioning Rules

### Semver

The schema version follows [semantic versioning](https://semver.org/):

- **Major** (1.x.x → 2.x.x): breaking change. Old code cannot read new
  data, or new code cannot safely read old data without migration.
- **Minor** (1.0.x → 1.1.x): backward-compatible addition. New optional
  fields in manifest/ops, new CLI commands. Old code can still read data.
- **Patch** (1.0.0 → 1.0.1): bug fix. No data format change.

### What triggers a major version bump

- Changing the content hash algorithm (affects chunk hashes → blob keys → dedup)
- Changing the KDF hash function (affects key derivation → can't unwrap keys)
- Changing the symmetric cipher (affects all encrypted data)
- Changing the key wrapping scheme (affects namespace key wrapping)
- Changing the blob header format
- Changing the manifest/ops JSON structure in a breaking way

### What does NOT trigger a major version bump

- Adding new optional fields to manifest or ops JSON
- Adding new CLI commands or RPC methods
- Performance optimizations that don't change data format
- Adding new storage providers

### Validation

On every bucket open, skyfs reads `sky10.schema` and compares the major
version:

| Bucket | Code | Result |
|--------|------|--------|
| 1.0.0 | 1.0.0 | OK |
| 1.0.0 | 1.5.0 | OK (same major) |
| 1.5.0 | 1.0.0 | OK (same major, minor is backward-compatible) |
| 2.0.0 | 1.0.0 | **Error**: "bucket is v2.0.0, code supports v1.0.0 — upgrade skyfs" |
| 1.0.0 | 2.0.0 | **Error**: "bucket is v1.0.0, code expects v2.0.0 — run skyfs migrate" |
| (none) | 1.0.0 | **Error**: "no schema found" — legacy bucket, needs migration |

---

## Schema History

### v1.0.0 (2026-03-14)

Initial schema. First versioned release.

- SHA3-256 for content hashing and key derivation
- AES-256-GCM for symmetric encryption
- Ephemeral ECDH X25519 for key wrapping
- Ed25519 identity with birational map conversion to X25519
- `SKY\x01` blob header
- FastCDC content-defined chunking (min 256KB, avg 1MB, max 4MB)
- Ops log for multi-device concurrent writes
- Manifest snapshots for compaction

#### Design decisions

- **SHA3-256 over SHA-256**: Keccak sponge construction hedges against
  SHA-2 family weaknesses. Immune to length-extension attacks.
- **Blob header**: 4 bytes (`SKY` + major version). Self-describing —
  any blob can be identified and versioned without the schema file.
- **Schema file unencrypted**: `sky10.schema` is plaintext JSON. It
  contains no sensitive data (just algorithm names) and must be readable
  before decryption keys are available.
