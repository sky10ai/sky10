# Schema Versioning

Decided: 2026-03-14

## Problem

When we change encryption algorithms (SHA-256 → SHA3-256), blob formats,
key derivation, or manifest structure, existing encrypted data becomes
unreadable. Without versioning, there's no way to know which algorithms
were used to encrypt data in a bucket, and no way to detect incompatible
changes before they corrupt data.

## Solution

A `sky10.schema` file in the bucket root. Written on `skyfs init`, read on
every bucket open. Contains the algorithms and format versions used.

```json
{
  "version": 2,
  "hash_algorithm": "sha3-256",
  "kdf": "hkdf-sha3-256",
  "cipher": "aes-256-gcm",
  "key_wrap": "ephemeral-ecdh-x25519-hkdf-sha3-256-aes-256-gcm",
  "blob_format": "v2"
}
```

## Behavior

- **Schema version matches code:** proceed normally.
- **Schema newer than code:** refuse to open. Tell user to upgrade skyfs.
- **Schema older than code:** refuse to open. Tell user to run `skyfs migrate`.
- **No schema file:** legacy bucket (pre-versioning). Offer migration.

## When to increment SchemaVersion

Increment `SchemaVersion` in `schema.go` when changing:

1. Content hash algorithm (affects chunk hashes → blob keys → dedup)
2. KDF hash function (affects key derivation → can't unwrap keys)
3. Encryption cipher (affects all encrypted data)
4. Key wrapping scheme (affects namespace/file key wrapping)
5. Blob format (affects how encrypted data is laid out)
6. Manifest/ops JSON structure (breaking field changes)

Do NOT increment for:
- Adding new optional fields to manifest/ops
- Adding new CLI commands
- Performance changes that don't affect data format
