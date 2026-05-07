package adapters

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestBuiltinsReturnsStableSortedNames(t *testing.T) {
	t.Parallel()

	names := Names()
	want := []string{"imap-smtp", "telegram"}
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
	if definition, ok := Lookup("telegram"); !ok || definition.Name != "telegram" {
		t.Fatalf("Lookup(telegram) = %+v, %v; want telegram", definition, ok)
	}
}

func TestBuiltinProcessSpec(t *testing.T) {
	t.Parallel()

	spec, err := BuiltinProcessSpec("/tmp/sky10", "imap-smtp")
	if err != nil {
		t.Fatalf("BuiltinProcessSpec() error = %v", err)
	}
	if spec.Path != filepath.Clean("/tmp/sky10") {
		t.Fatalf("spec.Path = %q, want %q", spec.Path, filepath.Clean("/tmp/sky10"))
	}
	if !slices.Equal(spec.Args, []string{"messaging", "imap-smtp"}) {
		t.Fatalf("spec.Args = %v, want [messaging imap-smtp]", spec.Args)
	}

	if _, err := BuiltinProcessSpec("", "imap-smtp"); err == nil {
		t.Fatal("BuiltinProcessSpec(empty executable) error = nil, want error")
	}
	spec, err = BuiltinProcessSpec("/tmp/sky10", "telegram")
	if err != nil {
		t.Fatalf("BuiltinProcessSpec(telegram) error = %v", err)
	}
	if !slices.Equal(spec.Args, []string{"messaging", "telegram"}) {
		t.Fatalf("telegram spec.Args = %v, want [messaging telegram]", spec.Args)
	}
	if _, err := BuiltinProcessSpec("/tmp/sky10", "missing"); err == nil {
		t.Fatal("BuiltinProcessSpec(missing adapter) error = nil, want error")
	}
}
