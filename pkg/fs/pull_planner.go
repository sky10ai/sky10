package fs

import (
	"context"
	"fmt"
)

type plannedChunkSource struct {
	plan  chunkSourcePlan
	reuse bool
}

type plannedChunk struct {
	index   int
	hash    string
	sources []plannedChunkSource
}

type pullPlan struct {
	nsID   string
	nsKey  []byte
	reuse  chunkReuseProvider
	chunks []plannedChunk
}

func (s *Store) buildPullPlan(ctx context.Context, chunks []string, namespace string, reuse chunkReuseProvider) (*pullPlan, error) {
	nsID, nsKey, err := s.resolveNamespaceState(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("namespace state for %q: %w", namespace, err)
	}

	plan := &pullPlan{
		nsID:   nsID,
		nsKey:  nsKey,
		reuse:  reuse,
		chunks: make([]plannedChunk, 0, len(chunks)),
	}
	for index, hash := range chunks {
		sources := make([]plannedChunkSource, 0, len(s.planChunkSources(hash))+1)
		if reuse != nil {
			sources = append(sources, plannedChunkSource{
				plan:  chunkSourcePlan{kind: chunkSourceLocal},
				reuse: true,
			})
		}
		for _, source := range s.planChunkSources(hash) {
			sources = append(sources, plannedChunkSource{plan: source})
		}
		plan.chunks = append(plan.chunks, plannedChunk{
			index:   index,
			hash:    hash,
			sources: sources,
		})
	}
	return plan, nil
}
