# sky10

Encrypted storage and agent coordination. Your data encrypted, your keys yours, no server sees plaintext.

Built in Go. Backed by any S3-compatible store.

## CLI

```bash
sky10 key generate                              # new Ed25519 keypair
sky10 key address                               # show your sky10q... address
sky10 key seal secret.txt --for sky10q...        # encrypt for a recipient
sky10 key open secret.txt.sealed                 # decrypt
sky10 key sign doc.pdf                           # sign a file
sky10 key verify doc.pdf --sig <hex> --from sky10q...

sky10 fs init --bucket my-bucket --endpoint https://s3.us-west-004.backblazeb2.com
sky10 fs put ./report.pdf --as financial/q4-report.pdf
sky10 fs get financial/q4-report.pdf
sky10 fs ls financial/
sky10 fs rm financial/q4-report.pdf
sky10 fs sync ~/Documents
sky10 fs versions report.pdf
sky10 fs restore report.pdf --at 2026-03-14T10:00:00Z
sky10 fs compact
sky10 fs gc --dry-run
```

Every file is chunked (FastCDC), compressed (zstd), encrypted (AES-256-GCM), and uploaded as opaque blobs. Content-addressed by SHA3-256. Three-layer key hierarchy (user → namespace → file) makes rotation cheap.

### Identity

Every user and agent is an Ed25519 keypair. Addresses are Bech32m-encoded with error detection.

```
sky10 key generate
  Address: sky10qvx2mz9...
```

### Build

```bash
make          # check + test + build → bin/sky10
make test     # Go + Swift tests
make web-dev  # Vite dev UI on :5173, opens browser, defaults RPC proxy to :9102
make reproduce  # prove build determinism
```

Deterministic builds: same source + same Go version = identical binary.

For a specific route or daemon target:

```bash
make web-dev WEB_DEV_PATH=/bucket WEB_RPC_TARGET=http://localhost:9101
```

### S3 Credentials

```bash
export S3_ACCESS_KEY_ID=your-key
export S3_SECRET_ACCESS_KEY=your-secret
```

Works with any S3-compatible store: Backblaze B2, Cloudflare R2, DigitalOcean Spaces, Wasabi, MinIO, AWS S3.

## Architecture

```
sky10/
├── main.go               CLI entry point
├── commands/              CLI wiring (cobra)
│   ├── key.go             sky10 key *
│   └── fs.go              sky10 fs *
├── pkg/
│   ├── key/               crypto primitives (Ed25519, Bech32m, Seal/Open, Sign/Verify)
│   ├── fs/                encrypted file storage (chunking, encryption, sync, ops log)
│   ├── adapter/           storage backend interface + S3 implementation
│   └── config/            ~/.sky10/ configuration
├── cirrus/              desktop apps
│   └── macos/           SwiftUI macOS app
└── docs/                  guides, work logs
```

## License

MIT
