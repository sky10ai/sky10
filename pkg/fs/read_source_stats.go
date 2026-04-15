package fs

import (
	"sync"
	"time"
)

type readSourceStatsSnapshot struct {
	LocalHits  int    `json:"read_local_hits"`
	PeerHits   int    `json:"read_peer_hits"`
	S3Hits     int    `json:"read_s3_hits"`
	LastSource string `json:"last_read_source,omitempty"`
	LastAt     int64  `json:"last_read_at,omitempty"`
}

type readSourceStats struct {
	mu         sync.Mutex
	localHits  int
	peerHits   int
	s3Hits     int
	lastSource string
	lastAt     time.Time
}

func newReadSourceStats() *readSourceStats {
	return &readSourceStats{}
}

func normalizeReadSource(kind chunkSourceKind) string {
	switch kind {
	case chunkSourceLocal:
		return "local"
	case chunkSourcePeer:
		return "peer"
	case chunkSourceS3Pack, chunkSourceS3Blob:
		return "s3"
	default:
		return ""
	}
}

func (s *readSourceStats) Record(kind chunkSourceKind) {
	if s == nil {
		return
	}
	source := normalizeReadSource(kind)
	if source == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch source {
	case "local":
		s.localHits++
	case "peer":
		s.peerHits++
	case "s3":
		s.s3Hits++
	}
	s.lastSource = source
	s.lastAt = time.Now().UTC()
}

func (s *readSourceStats) Snapshot() readSourceStatsSnapshot {
	if s == nil {
		return readSourceStatsSnapshot{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return readSourceStatsSnapshot{
		LocalHits:  s.localHits,
		PeerHits:   s.peerHits,
		S3Hits:     s.s3Hits,
		LastSource: s.lastSource,
		LastAt:     s.lastAt.Unix(),
	}
}

func (s readSourceStatsSnapshot) TotalHits() int {
	return s.LocalHits + s.PeerHits + s.S3Hits
}
