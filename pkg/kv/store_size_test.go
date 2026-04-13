package kv

import (
	"bytes"
	"context"
	"errors"
	"testing"

	skyconfig "github.com/sky10/sky10/pkg/config"
	skykey "github.com/sky10/sky10/pkg/key"
)

func TestStoreSetHonorsMaxValueSize(t *testing.T) {
	t.Setenv(skyconfig.EnvHome, t.TempDir())

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	store := New(nil, identity, Config{
		Namespace: "test-inline-limit",
		DataDir:   t.TempDir(),
	}, nil)

	ctx := context.Background()
	if err := store.Set(ctx, "ok", bytes.Repeat([]byte("a"), MaxValueSize)); err != nil {
		t.Fatalf("Set(MaxValueSize): %v", err)
	}

	err = store.Set(ctx, "too-large", bytes.Repeat([]byte("a"), MaxValueSize+1))
	if !errors.Is(err, ErrValueTooLarge) {
		t.Fatalf("Set(MaxValueSize+1) error = %v, want ErrValueTooLarge", err)
	}
}
