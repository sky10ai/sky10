// Package kv provides an encrypted key-value store backed by S3 with
// snapshot-exchange synchronization. State lives in memory plus a local
// JSONL log — no filesystem involvement.
package kv

import "time"

// EntryType is the kind of KV operation.
type EntryType string

const (
	Set    EntryType = "set"
	Delete EntryType = "delete"
)

// Entry is a single operation in the local KV ops log.
type Entry struct {
	Type      EntryType `json:"op"`
	Key       string    `json:"key"`
	Value     []byte    `json:"value,omitempty"` // inline, max 4KB for v1
	Namespace string    `json:"namespace,omitempty"`
	Device    string    `json:"device"`
	Timestamp int64     `json:"timestamp"`
	Seq       int       `json:"seq"`
}

// clockTuple is the comparison key for LWW conflict resolution.
// Comparison order: timestamp, then device ID (lexicographic), then seq.
type clockTuple struct {
	ts     int64
	device string
	seq    int
}

// beats returns true if c is strictly greater than other.
func (c clockTuple) beats(other clockTuple) bool {
	if c.ts != other.ts {
		return c.ts > other.ts
	}
	if c.device != other.device {
		return c.device > other.device
	}
	return c.seq > other.seq
}

// Clock represents the LWW clock for a KV entry (exported for poller).
type Clock struct {
	Ts     int64
	Device string
	Seq    int
}

// Beats returns true if c is strictly greater than other.
func (c Clock) Beats(other Clock) bool {
	if c.Ts != other.Ts {
		return c.Ts > other.Ts
	}
	if c.Device != other.Device {
		return c.Device > other.Device
	}
	return c.Seq > other.Seq
}

// ClockOf extracts the clock from a ValueInfo.
func ClockOf(vi ValueInfo) Clock {
	return Clock{Ts: vi.Modified.Unix(), Device: vi.Device, Seq: vi.Seq}
}

// ValueInfo describes a value at a point in time.
type ValueInfo struct {
	Value    []byte    `json:"value"`
	Modified time.Time `json:"modified"`
	Device   string    `json:"device,omitempty"`
	Seq      int       `json:"seq,omitempty"`
}
