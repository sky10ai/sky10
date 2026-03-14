# Schema Versioning

Decided: 2026-03-14

## Decision

Every bucket has a `sky10.schema` file (unencrypted JSON) and every
encrypted blob has a version prefix byte. Semver for the schema,
integer for the blob format.

## Why

Without versioning, changing any crypto primitive (hash, cipher, KDF)
silently breaks existing data. The SHA-256→SHA3-256 migration was the
trigger — it would have corrupted any existing bucket.

## Rules

- Bump major version for breaking changes (cipher, hash, KDF, blob format)
- Minor/patch differences are compatible (new optional fields, bug fixes)
- `ValidateSchema` refuses to open mismatched major versions
- Every encrypted blob starts with `BlobVersion` byte (0x01 currently)
- `StripBlobVersion` detects legacy unversioned data by checking if first
  byte is a known version number
