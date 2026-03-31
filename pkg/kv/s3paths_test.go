package kv

import "testing"

func TestS3Paths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			"snapshot latest",
			snapshotLatestKey("abc123", "dev1"),
			"kv/abc123/snapshots/dev1/latest.enc",
		},
		{
			"snapshot history",
			snapshotHistoryKey("abc123", "dev1", 1711700000),
			"kv/abc123/snapshots/dev1/1711700000.enc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
