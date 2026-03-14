# Key Wrapping: Ed25519 → X25519 Conversion

Updated: 2026-03-14

## Problem

Sky10 uses Ed25519 for identity (signing). But ECDH key exchange requires
X25519 (Montgomery curve). We need to wrap symmetric keys (namespace keys,
file keys) so only the holder of an Ed25519 private key can unwrap them.

## Current Implementation (v2)

Ephemeral ECDH with proper curve conversion:

1. Convert Ed25519 public key → X25519 public key via birational map
   (`filippo.io/edwards25519` → `BytesMontgomery()`)
2. Generate ephemeral X25519 keypair
3. ECDH shared secret between ephemeral private + recipient X25519 public
4. HKDF-SHA256 to derive wrapping key
5. AES-256-GCM to wrap the data key

Output: `[32-byte ephemeral public key | AES-GCM encrypted data key]`

**WrapKey needs only the recipient's public key.** No private key required.
This enables wrapping for any identity — prerequisite for multi-party
sharing, key rotation, and grant/revoke access control.

### Private Key Conversion

`edPrivToX25519` uses SHA-512 of the Ed25519 seed with X25519 clamping.
This matches Ed25519's internal scalar derivation (RFC 8032). The first
32 bytes of SHA-512(seed), clamped, IS the X25519 private key that
corresponds to the Edwards-to-Montgomery public key conversion.

### Public Key Conversion

`edPubToX25519` uses the birational map from the Edwards curve to the
Montgomery curve: u = (1 + y) / (1 - y). Implemented by
`filippo.io/edwards25519` via `Point.BytesMontgomery()`.

## Hash Algorithm Analysis

### Where hashes are used in key wrapping

| Step | Algorithm | Input entropy | Purpose |
|------|-----------|--------------|---------|
| Ed25519 seed → X25519 scalar | SHA-512 | 32 bytes (random seed) | Standard conversion per RFC 8032 |
| ECDH shared secret → wrapping key | HKDF-SHA256 | 32 bytes (ECDH output) | Key derivation |
| Wrapping key → encrypted data key | AES-256-GCM | 32 bytes (HKDF output) | Authenticated encryption |

### Why SHA-256/SHA-512 is correct here

All inputs are high-entropy random values (32-byte keys, ECDH outputs).
Brute-force is already infeasible (2^256 operations). The hash function's
job is to be a PRF (pseudorandom function), not to be slow. SHA-256 in
HMAC (which HKDF uses internally) is a well-analyzed PRF used by:

- TLS 1.3 (HKDF-SHA256 for all key derivation)
- Signal Protocol (HKDF-SHA256)
- Noise Protocol Framework (SHA-256 or BLAKE2)
- WireGuard (BLAKE2s, but same security level)

SHA-3 or BLAKE2 would work equivalently but add no practical security.
Switching would diverge from established practice for zero gain.

### Where slow hashes WOULD be needed

If sky10 adds passphrase-based identity recovery (e.g., "recover from
seed phrase"), that path MUST use a memory-hard KDF:

- **Argon2id** — recommended. Memory-hard, resistant to GPU/ASIC attacks.
  Use for: seed phrase → master key derivation.
- **scrypt** — acceptable alternative. Used by many crypto wallets.
- **bcrypt** — not suitable (fixed 72-byte input, no memory hardness tuning).

This is a future concern. Current key wrapping operates entirely on
high-entropy random keys where fast hashes are correct.
