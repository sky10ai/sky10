# sky10

Encrypted storage and agent coordination. Your data encrypted, your keys yours, no server sees plaintext.

Built in Go. Backed by any S3-compatible store.

## skyfs

Encrypted file storage primitives. Encrypt locally, store anywhere, decrypt locally.

```bash
skyfs init --bucket my-bucket --endpoint https://s3.us-west-004.backblazeb2.com
skyfs put ./report.pdf --as financial/q4-report.pdf
skyfs get financial/q4-report.pdf
skyfs ls financial/
skyfs rm financial/q4-report.pdf
skyfs info
```

Every file is chunked, encrypted with AES-256-GCM, and uploaded as opaque blobs. Content-addressed by SHA-256 for deduplication. Three-layer key hierarchy (user → namespace → file) makes rotation cheap.

### Identity

Every user is an Ed25519 keypair. No usernames, no passwords.

```
skyfs init
  Identity: sky://k1_abc123def456...
```

### Build

Requires Go 1.22+.

```bash
make          # check + test + build → bin/skyfs
make test     # run all tests
make reproduce  # prove build determinism (builds twice, compares SHA-256)
```

Builds are deterministic: same source + same Go version = identical binary. See [Makefile](Makefile) for flags.

### S3 Credentials

```bash
export S3_ACCESS_KEY_ID=your-key
export S3_SECRET_ACCESS_KEY=your-secret
```

Works with any S3-compatible store: Backblaze B2, Cloudflare R2, MinIO, AWS S3, etc.

## Architecture

```
sky10/
├── cmd/skyfs/         CLI
├── skyfs/             encrypted file storage library
│   ├── crypto.go      AES-256-GCM, ECDH key wrapping
│   ├── identity.go    Ed25519 keypair management
│   ├── keys.go        key hierarchy + HKDF derivation
│   ├── chunk.go       streaming chunker (4MB max, constant memory)
│   └── manifest.go    encrypted file tree
├── skyadapter/        storage backend interface
│   └── s3/            S3 implementation
└── internal/config/   ~/.skyfs/ configuration
```

## License

MIT
