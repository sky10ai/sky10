package skyfs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/skyadapter"
)

// OpType represents the type of operation.
type OpType string

const (
	OpPut    OpType = "put"
	OpDelete OpType = "delete"
)

// Op is a single operation in the append-only log.
type Op struct {
	Type         OpType   `json:"op"`
	Path         string   `json:"path"`
	Chunks       []string `json:"chunks,omitempty"`
	Size         int64    `json:"size,omitempty"`
	Checksum     string   `json:"checksum,omitempty"`
	PrevChecksum string   `json:"prev_checksum,omitempty"`
	Namespace    string   `json:"namespace,omitempty"`
	Device       string   `json:"device"`
	Timestamp    int64    `json:"timestamp"`
	Seq          int      `json:"seq"`
}

// OpKey returns the S3 key for this op.
// Format: ops/{timestamp}-{device}-{seq}.enc
func (o *Op) OpKey() string {
	return fmt.Sprintf("ops/%d-%s-%04d.enc", o.Timestamp, o.Device, o.Seq)
}

// WriteOp encrypts and uploads an op to the ops/ prefix.
func WriteOp(ctx context.Context, backend skyadapter.Backend, op *Op, encKey []byte) error {
	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("marshaling op: %w", err)
	}

	encrypted, err := Encrypt(data, encKey)
	if err != nil {
		return fmt.Errorf("encrypting op: %w", err)
	}

	r := bytes.NewReader(encrypted)
	if err := backend.Put(ctx, op.OpKey(), r, int64(len(encrypted))); err != nil {
		return fmt.Errorf("uploading op: %w", err)
	}

	return nil
}

// ReadOps reads and decrypts all ops with timestamps after since.
// Returns ops sorted by (timestamp, device, seq).
func ReadOps(ctx context.Context, backend skyadapter.Backend, since int64, encKey []byte) ([]Op, error) {
	keys, err := backend.List(ctx, "ops/")
	if err != nil {
		return nil, fmt.Errorf("listing ops: %w", err)
	}

	var ops []Op
	for _, key := range keys {
		// Parse timestamp from key to filter early
		ts := parseOpTimestamp(key)
		if ts <= since {
			continue
		}

		rc, err := backend.Get(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("downloading op %s: %w", key, err)
		}

		encrypted, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading op %s: %w", key, err)
		}

		data, err := Decrypt(encrypted, encKey)
		if err != nil {
			return nil, fmt.Errorf("decrypting op %s: %w", key, err)
		}

		var op Op
		if err := json.Unmarshal(data, &op); err != nil {
			return nil, fmt.Errorf("parsing op %s: %w", key, err)
		}

		ops = append(ops, op)
	}

	sortOps(ops)
	return ops, nil
}

// ReadAllOps reads and decrypts all ops in the log.
func ReadAllOps(ctx context.Context, backend skyadapter.Backend, encKey []byte) ([]Op, error) {
	return ReadOps(ctx, backend, 0, encKey)
}

// sortOps sorts ops by (timestamp, device, seq) for deterministic replay.
func sortOps(ops []Op) {
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Timestamp != ops[j].Timestamp {
			return ops[i].Timestamp < ops[j].Timestamp
		}
		if ops[i].Device != ops[j].Device {
			return ops[i].Device < ops[j].Device
		}
		return ops[i].Seq < ops[j].Seq
	})
}

// BuildState replays ops on top of a base manifest to produce current state.
func BuildState(base *Manifest, ops []Op) *Manifest {
	m := NewManifest()
	if base != nil {
		m.Version = base.Version
		m.Created = base.Created
		for k, v := range base.Tree {
			m.Tree[k] = v
		}
	}

	for _, op := range ops {
		switch op.Type {
		case OpPut:
			m.Tree[op.Path] = FileEntry{
				Chunks:    op.Chunks,
				Size:      op.Size,
				Modified:  time.Unix(op.Timestamp, 0).UTC(),
				Checksum:  op.Checksum,
				Namespace: op.Namespace,
			}
		case OpDelete:
			delete(m.Tree, op.Path)
		}
	}

	m.Updated = time.Now().UTC()
	return m
}

// Conflict represents a concurrent edit detected in the ops log.
type Conflict struct {
	Path    string
	DeviceA string
	DeviceB string
	OpA     Op
	OpB     Op
}

// DetectConflicts finds concurrent edits: two ops for the same path
// with the same prev_checksum from different devices.
func DetectConflicts(ops []Op) []Conflict {
	// Group put ops by (path, prev_checksum)
	type key struct {
		path         string
		prevChecksum string
	}
	groups := make(map[key][]Op)

	for _, op := range ops {
		if op.Type != OpPut || op.PrevChecksum == "" {
			continue
		}
		k := key{op.Path, op.PrevChecksum}
		groups[k] = append(groups[k], op)
	}

	var conflicts []Conflict
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		// Multiple ops branched from the same prev_checksum = conflict
		for i := 1; i < len(group); i++ {
			if group[i].Device != group[0].Device {
				conflicts = append(conflicts, Conflict{
					Path:    group[0].Path,
					DeviceA: group[0].Device,
					DeviceB: group[i].Device,
					OpA:     group[0],
					OpB:     group[i],
				})
			}
		}
	}

	return conflicts
}

// parseOpTimestamp extracts the timestamp from an op key.
// Key format: ops/{timestamp}-{device}-{seq}.enc
func parseOpTimestamp(key string) int64 {
	name := strings.TrimPrefix(key, "ops/")
	name = strings.TrimSuffix(name, ".enc")
	parts := strings.SplitN(name, "-", 2)
	if len(parts) < 1 {
		return 0
	}
	var ts int64
	fmt.Sscanf(parts[0], "%d", &ts)
	return ts
}
