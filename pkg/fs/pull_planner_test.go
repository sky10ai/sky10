package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreBuildPullPlanIncludesLocalReusePeerAndS3(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx := context.Background()
	store, backend := newTestStore(t)

	content := "planner uses one path"
	if err := store.Put(ctx, "shared.md", strings.NewReader(content)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	res := store.LastPutResult()
	if res == nil || len(res.Chunks) != 1 {
		t.Fatal("expected single chunk from Put")
	}

	raw := readBackendBlob(t, backend, store.blobKeyFor(res.Chunks[0]))
	store.SetPeerChunkFetcher(&stubPeerChunkFetcher{raw: raw})

	reusePath := filepath.Join(home, "reuse.txt")
	if err := os.WriteFile(reusePath, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile reuse: %v", err)
	}
	reuse, err := newLocalFileChunkReuse(reusePath, res.Chunks)
	if err != nil {
		t.Fatalf("newLocalFileChunkReuse: %v", err)
	}
	defer reuse.Close()

	plan, err := store.buildPullPlan(ctx, res.Chunks, "default", reuse)
	if err != nil {
		t.Fatalf("buildPullPlan: %v", err)
	}
	if len(plan.chunks) != 1 {
		t.Fatalf("planned chunks = %d, want 1", len(plan.chunks))
	}
	sources := plan.chunks[0].sources
	if len(sources) != 4 {
		t.Fatalf("planned sources = %d, want 4", len(sources))
	}
	if !sources[0].reuse || sources[0].plan.kind != chunkSourceLocal {
		t.Fatalf("source[0] = %+v, want local reuse", sources[0])
	}
	if sources[1].reuse || sources[1].plan.kind != chunkSourceLocal {
		t.Fatalf("source[1] = %+v, want local cache", sources[1])
	}
	if sources[2].reuse || sources[2].plan.kind != chunkSourcePeer {
		t.Fatalf("source[2] = %+v, want peer", sources[2])
	}
	if sources[3].reuse || sources[3].plan.kind != chunkSourceS3Blob {
		t.Fatalf("source[3] = %+v, want s3 blob", sources[3])
	}
}
