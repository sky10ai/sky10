package kv

import "testing"

func TestCompareCausal(t *testing.T) {
	t.Parallel()

	aAfterB := compareCausal(
		"actor-a", 2, VersionVector{"actor-a": 1, "actor-b": 1},
		"actor-b", 1, VersionVector{"actor-a": 1},
	)
	if aAfterB <= 0 {
		t.Fatalf("expected actor-a write to happen after actor-b, got %d", aAfterB)
	}

	concurrent := compareCausal(
		"actor-a", 1, nil,
		"actor-b", 1, nil,
	)
	if concurrent != 0 {
		t.Fatalf("expected concurrent writes, got %d", concurrent)
	}
}
