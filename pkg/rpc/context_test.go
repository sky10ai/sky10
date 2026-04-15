package rpc

import (
	"context"
	"testing"
)

func TestCallerInfoContext(t *testing.T) {
	t.Parallel()

	ctx := WithCallerInfo(context.Background(), "http", "127.0.0.1:9101")
	info, ok := CallerInfoFromContext(ctx)
	if !ok {
		t.Fatal("CallerInfoFromContext should report caller info")
	}
	if info.Transport != "http" {
		t.Fatalf("transport = %q, want %q", info.Transport, "http")
	}
	if info.Remote != "127.0.0.1:9101" {
		t.Fatalf("remote = %q, want %q", info.Remote, "127.0.0.1:9101")
	}
}

func TestCallerInfoFromContextMissing(t *testing.T) {
	t.Parallel()

	if _, ok := CallerInfoFromContext(context.Background()); ok {
		t.Fatal("CallerInfoFromContext should report no caller info on a bare context")
	}
}
