package kv

import "testing"

func TestIsInternalKeyTreatsMailboxCollectionsAsSystemData(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{key: "_sys/secrets/head", want: true},
		{key: "_sys/mailbox/private/items/item-1", want: true},
		{key: "mailbox/private/items/item-1", want: false},
		{key: "settings/theme", want: false},
	}

	for _, tc := range tests {
		if got := IsInternalKey(tc.key); got != tc.want {
			t.Fatalf("IsInternalKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}
