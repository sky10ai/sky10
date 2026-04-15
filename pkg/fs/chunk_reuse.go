package fs

import (
	"fmt"
	"io"
	"os"
)

type chunkReuseProvider interface {
	LookupChunk(chunkHash string) ([]byte, bool, error)
	Close() error
}

type localFileChunkReuse struct {
	file    *os.File
	matches map[string]chunkReuseSlice
}

type chunkReuseSlice struct {
	offset int64
	length int
}

func newLocalFileChunkReuse(path string, wanted []string) (chunkReuseProvider, error) {
	if path == "" || len(wanted) == 0 {
		return nil, nil
	}

	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat local reuse file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local reuse file: %w", err)
	}

	wantedSet := make(map[string]struct{}, len(wanted))
	for _, hash := range wanted {
		if hash != "" {
			wantedSet[hash] = struct{}{}
		}
	}
	if len(wantedSet) == 0 {
		_ = file.Close()
		return nil, nil
	}

	chunker := NewChunker(file)
	matches := make(map[string]chunkReuseSlice, len(wantedSet))
	for {
		chunk, err := chunker.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("chunk local reuse file: %w", err)
		}
		if _, ok := wantedSet[chunk.Hash]; ok {
			if _, exists := matches[chunk.Hash]; !exists {
				matches[chunk.Hash] = chunkReuseSlice{
					offset: chunk.Offset,
					length: chunk.Length,
				}
			}
			if len(matches) == len(wantedSet) {
				break
			}
		}
	}
	if len(matches) == 0 {
		_ = file.Close()
		return nil, nil
	}

	return &localFileChunkReuse{
		file:    file,
		matches: matches,
	}, nil
}

func (r *localFileChunkReuse) LookupChunk(chunkHash string) ([]byte, bool, error) {
	if r == nil || r.file == nil {
		return nil, false, nil
	}
	loc, ok := r.matches[chunkHash]
	if !ok {
		return nil, false, nil
	}

	buf := make([]byte, loc.length)
	n, err := r.file.ReadAt(buf, loc.offset)
	if err != nil && err != io.EOF {
		return nil, false, fmt.Errorf("read local reuse chunk: %w", err)
	}
	if n != len(buf) {
		return nil, false, io.ErrUnexpectedEOF
	}
	if ContentHash(buf) != chunkHash {
		return nil, false, nil
	}
	return buf, true, nil
}

func (r *localFileChunkReuse) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}
