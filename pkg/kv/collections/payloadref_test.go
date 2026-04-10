package collections

import "testing"

func TestPayloadRefValidate(t *testing.T) {
	t.Parallel()

	ref, err := NewPayloadRef(PayloadKindChunkedKV, "payloads/123", 42, SHA256Digest([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPayloadRefValidateRejectsMissingFields(t *testing.T) {
	t.Parallel()

	if _, err := NewPayloadRef("", "payloads/123", 42, ""); err == nil {
		t.Fatal("expected missing kind error")
	}
	if _, err := NewPayloadRef(PayloadKindSkyFS, "", 42, ""); err == nil {
		t.Fatal("expected missing key error")
	}
}

func TestShouldInline(t *testing.T) {
	t.Parallel()

	if !ShouldInline(4096, 4096) {
		t.Fatal("size equal to max should stay inline")
	}
	if ShouldInline(4097, 4096) {
		t.Fatal("size above max should not stay inline")
	}
}
