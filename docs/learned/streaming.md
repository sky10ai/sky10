# Streaming From Day One

Decided: 2026-03-14

## Decision

skyfs streams files at every layer. No file is ever loaded entirely into
memory. The API uses `io.Reader` for input and `io.Writer` for output.

## Why

skyfs must handle files gigabytes in size. Deferring streaming to v2 would
mean rewriting the entire API surface — every function signature, the chunker,
the S3 adapter, and the CLI. Baking it in from the start is cheaper than
retrofitting.

## Memory Model

```
Layer           Interface         Max memory
─────           ─────────         ──────────
File I/O        io.Reader/Writer  buffered (OS page cache)
Chunker         iterator          one chunk ≤ 4MB
Crypto          []byte            one chunk ≤ 4MB
S3 adapter      io.Reader + size  one chunk ≤ 4MB
```

AES-256-GCM needs the full plaintext to produce the auth tag, but it operates
per-chunk, not per-file. Each chunk is at most 4MB (FastCDC max). That's the
memory ceiling.

## What This Means for the API

```go
// skyadapter — streaming
Put(ctx context.Context, key string, r io.Reader, size int64) error
Get(ctx context.Context, key string) (io.ReadCloser, error)

// skyfs — streaming
Put(ctx context.Context, path string, r io.Reader) error
Get(ctx context.Context, path string, w io.Writer) error

// crypto — bounded []byte (chunk-level, max 4MB)
Encrypt(plaintext, key []byte) ([]byte, error)
Decrypt(ciphertext, key []byte) ([]byte, error)

// chunker — iterator over bounded chunks
NewChunker(r io.Reader) *Chunker
(*Chunker) Next() (Chunk, error)  // returns io.EOF when done
```
