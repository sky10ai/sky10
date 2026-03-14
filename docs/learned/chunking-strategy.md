# Chunking Strategy

Decided: 2026-03-14

## v1: Fixed-size chunking (4MB max)

The chunker reads up to `MaxChunkSize` (4MB) from an `io.Reader` per call
to `Next()`. This is the simplest approach that satisfies the streaming
requirement.

## Why not FastCDC in v1

FastCDC (content-defined chunking) uses a rolling hash to find split points
based on content. This means inserting a paragraph in the middle of a large
file only changes the chunks around the edit — everything else deduplicates.

We planned to use `jotfs/fastcdc-go` but deferred it because:

1. Fixed-size chunking is correct for all operations
2. Most common files are small (markdown, config, text — all < 1MB = single chunk)
3. CDC's dedup advantage only matters for edits within large files
4. The `Chunker` interface (`NewChunker` / `Next`) is the same either way

## When to add FastCDC

Add it when large file edits become common and dedup matters. The swap is
contained to `chunk.go` — the `Chunker` API stays the same, only the
split-point logic changes.

## Memory model

```
MaxChunkSize = 4MB
One chunk in memory at a time
File streams through: read chunk → encrypt → upload → next chunk
```

This is the same model Restic uses. A 10GB file uses ~4MB of memory.
