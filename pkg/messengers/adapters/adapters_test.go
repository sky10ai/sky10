package adapters

import (
	"slices"
	"testing"
)

func TestBuiltinsReturnsStableSortedNames(t *testing.T) {
	t.Parallel()

	names := Names()
	want := []string{"imap-smtp"}
	if !slices.Equal(names, want) {
		t.Fatalf("Names() = %v, want %v", names, want)
	}

	builtins := Builtins()
	if len(builtins) != len(want) {
		t.Fatalf("Builtins() len = %d, want %d", len(builtins), len(want))
	}
	for idx, definition := range builtins {
		if definition.Name != want[idx] {
			t.Fatalf("Builtins()[%d].Name = %q, want %q", idx, definition.Name, want[idx])
		}
		if definition.Serve == nil {
			t.Fatalf("Builtins()[%d].Serve = nil", idx)
		}
	}
}

func TestLookupFindsRegisteredAdapter(t *testing.T) {
	t.Parallel()

	definition, ok := Lookup("imap-smtp")
	if !ok {
		t.Fatal("Lookup(imap-smtp) = false, want true")
	}
	if definition.Name != "imap-smtp" {
		t.Fatalf("Lookup(imap-smtp).Name = %q, want imap-smtp", definition.Name)
	}
	if _, ok := Lookup("missing"); ok {
		t.Fatal("Lookup(missing) = true, want false")
	}
}
