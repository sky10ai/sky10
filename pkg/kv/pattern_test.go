package kv

import (
	"reflect"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		key     string
		want    bool
	}{
		{name: "exact", pattern: "users/alice", key: "users/alice", want: true},
		{name: "star matches slash", pattern: "users/*", key: "users/team/alice", want: true},
		{name: "question mark matches single rune", pattern: "job-??", key: "job-ab", want: true},
		{name: "question mark rejects missing rune", pattern: "job-??", key: "job-a", want: false},
		{name: "suffix mismatch", pattern: "users/*", key: "admins/alice", want: false},
		{name: "empty pattern", pattern: "", key: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := matchPattern(tt.pattern, tt.key); got != tt.want {
				t.Fatalf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.key, got, tt.want)
			}
		})
	}
}

func TestFilterKeysByPattern(t *testing.T) {
	t.Parallel()

	keys := []string{
		"config",
		"users/alice",
		"users/team/bob",
		"users/team/carol",
	}

	got := filterKeysByPattern(keys, "users/*")
	want := []string{
		"users/alice",
		"users/team/bob",
		"users/team/carol",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterKeysByPattern() = %v, want %v", got, want)
	}
}
