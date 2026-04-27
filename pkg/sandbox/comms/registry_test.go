package comms

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func validSpec() TypeSpec {
	return TypeSpec{
		Name:           "test.echo",
		Direction:      DirectionRequestResponse,
		MaxPayloadSize: 1024,
		RateLimit: RateLimit{
			PerAgent: 10,
			Burst:    5,
			Window:   time.Second,
		},
		NonceWindow: time.Minute,
		AuditLevel:  AuditFull,
		Handler: func(_ context.Context, _ Envelope) (json.RawMessage, error) {
			return json.RawMessage("null"), nil
		},
	}
}

func TestTypeSpecValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*TypeSpec)
		wantSub string
	}{
		{"missing Name", func(s *TypeSpec) { s.Name = "" }, "TypeSpec.Name is required"},
		{"missing Direction", func(s *TypeSpec) { s.Direction = 0 }, "missing Direction"},
		{"missing MaxPayloadSize", func(s *TypeSpec) { s.MaxPayloadSize = 0 }, "missing MaxPayloadSize"},
		{"missing PerAgent", func(s *TypeSpec) { s.RateLimit.PerAgent = 0 }, "missing RateLimit.PerAgent"},
		{"missing Burst", func(s *TypeSpec) { s.RateLimit.Burst = 0 }, "missing RateLimit.Burst"},
		{"missing Window", func(s *TypeSpec) { s.RateLimit.Window = 0 }, "missing RateLimit.Window"},
		{"missing NonceWindow", func(s *TypeSpec) { s.NonceWindow = 0 }, "missing NonceWindow"},
		{"missing Handler", func(s *TypeSpec) { s.Handler = nil }, "missing Handler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := validSpec()
			tc.mutate(&spec)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("validate did not panic for %s", tc.name)
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("validate panicked with non-string: %T %v", r, r)
				}
				if !contains(msg, tc.wantSub) {
					t.Fatalf("validate panic %q does not contain %q", msg, tc.wantSub)
				}
			}()
			spec.validate()
		})
	}
}

func TestTypeSpecValidateAccepts(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("valid spec panicked: %v", r)
		}
	}()
	validSpec().validate()
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
