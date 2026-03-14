# Key Wrapping: Ed25519 → X25519 Conversion

Decided: 2026-03-14

## Problem

Sky10 uses Ed25519 for identity (signing). But ECDH key exchange requires
X25519 (Montgomery curve). We need to wrap symmetric keys (namespace keys,
file keys) so only the holder of an Ed25519 private key can unwrap them.

## Solution

Ephemeral ECDH with Ed25519 → X25519 conversion:

1. Convert Ed25519 private key seed to X25519 private key via SHA-256
2. Derive X25519 public key from the converted private key
3. Generate ephemeral X25519 keypair
4. ECDH shared secret between ephemeral private + recipient public
5. HKDF-SHA256 to derive wrapping key
6. AES-256-GCM to wrap the data key

Output: `[32-byte ephemeral public key | AES-GCM encrypted data key]`

## Why SHA-256 for the conversion

Ed25519 internally uses SHA-512 of the seed, then clamps the first 32 bytes
for the scalar. We use SHA-256 of the seed instead, which produces a different
but equally valid X25519 private key. The important thing is that the conversion
is deterministic and one-way from the seed.

The alternative — using the `crypto/ed25519` internal scalar directly — would
require reaching into unexported fields. SHA-256 of the seed is clean, portable,
and doesn't depend on Ed25519 implementation details.

## Trade-off

This means `WrapKey` needs both the recipient's public key AND private key
(to derive the X25519 public key from the private key's seed). In practice
this is fine for single-user v1 where you're always wrapping for yourself.
For multi-party wrapping (v2/skydb), we'll need to either:

- Store X25519 public keys alongside Ed25519 public keys in profiles
- Use a proper Edwards-to-Montgomery point conversion (the birational map)

The second approach is more elegant but requires either a library or manual
implementation of the curve arithmetic.
