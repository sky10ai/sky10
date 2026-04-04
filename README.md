# sky10

Encrypted storage and agent coordination over P2P. Your data encrypted, your keys yours, no server sees plaintext.

Built in Go. Runs on macOS and Linux. S3 optional.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/sky10ai/sky10/main/install.sh | bash
```

Installs to `~/.bin/sky10`, sets up the background daemon (launchd on
macOS, systemd on Linux), no sudo required.

**Migrating from Homebrew?**

```bash
brew uninstall sky10
brew untap sky10ai/tap        # optional, removes the tap
curl -fsSL https://raw.githubusercontent.com/sky10ai/sky10/main/install.sh | bash
```

## Quick Start

```bash
sky10 serve              # start the daemon (generates identity automatically)
sky10 invite             # generate an invite code for another device
sky10 join <code>        # join from the other device
```

The daemon starts in P2P-only mode — no S3 needed. Devices discover each other via Nostr relays and sync KV data directly over libp2p.

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

### S3 (Optional)

S3 is an opt-in storage backend for durable file storage. The daemon runs fully without it.

```bash
export S3_ACCESS_KEY_ID=your-key
export S3_SECRET_ACCESS_KEY=your-secret
sky10 fs init --bucket my-bucket --endpoint https://s3.example.com
```

Works with any S3-compatible store: Backblaze B2, Cloudflare R2, DigitalOcean Spaces, Wasabi, MinIO, AWS S3.

### Build

```bash
make          # check + test + build → bin/sky10
make test     # Go tests
make web-dev  # Vite dev UI on :5173
make platforms  # cross-compile for linux/macOS amd64/arm64
make reproduce  # prove build determinism
```

Deterministic builds: same source + same Go version = identical binary.

## Architecture

```
sky10/
├── main.go               CLI entry point
├── commands/              CLI wiring (cobra)
│   ├── serve.go           sky10 serve (daemon)
│   ├── invite.go          sky10 invite
│   ├── join.go            sky10 join
│   ├── key.go             sky10 key *
│   └── fs.go              sky10 fs *
├── pkg/
│   ├── key/               crypto primitives (Ed25519, Bech32m, Seal/Open, Sign/Verify)
│   ├── id/                identity + device keys, manifest signing
│   ├── join/              invite/join flows (P2P + S3)
│   ├── kv/                replicated key-value store (LWW-Register-Map)
│   ├── link/              P2P communication (libp2p, Nostr discovery)
│   ├── fs/                encrypted file storage (chunking, encryption, sync)
│   ├── adapter/           storage backend interface + S3 implementation
│   └── config/            ~/.sky10/ configuration
├── web/                   React web UI (Vite + TypeScript)
└── docs/                  guides, work logs
```

## License

MIT
