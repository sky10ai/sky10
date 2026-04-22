package broker

import (
	"context"
	"errors"
	"testing"

	"github.com/sky10/sky10/pkg/secrets"
)

func TestSecretsResolverUsesOwnerRequesterByDefault(t *testing.T) {
	t.Parallel()

	store := &stubSecretsGetter{
		secret: &secrets.Secret{
			SecretSummary: secrets.SecretSummary{
				ID:          "secret-id",
				ContentType: "application/json",
			},
			Payload: []byte(`{"token":"abc"}`),
		},
	}
	resolver := SecretsResolver{Store: store}

	got, err := resolver.ResolveMessagingCredential(context.Background(), "secret://mail/work")
	if err != nil {
		t.Fatalf("ResolveMessagingCredential() error = %v", err)
	}
	if store.requester.Type != secrets.RequesterOwner {
		t.Fatalf("requester type = %q, want %q", store.requester.Type, secrets.RequesterOwner)
	}
	if got.Ref != "secret://mail/work" {
		t.Fatalf("resolved ref = %q, want secret://mail/work", got.Ref)
	}
	if string(got.Payload) != `{"token":"abc"}` {
		t.Fatalf("resolved payload = %q", string(got.Payload))
	}
}

func TestSecretsResolverPropagatesStoreErrors(t *testing.T) {
	t.Parallel()

	store := &stubSecretsGetter{err: errors.New("boom")}
	resolver := SecretsResolver{Store: store}

	_, err := resolver.ResolveMessagingCredential(context.Background(), "secret://mail/work")
	if err == nil || err.Error() != `get messaging credential "secret://mail/work": boom` {
		t.Fatalf("ResolveMessagingCredential() error = %v, want wrapped store error", err)
	}
}

type stubSecretsGetter struct {
	requester secrets.Requester
	secret    *secrets.Secret
	err       error
}

func (s *stubSecretsGetter) Get(_ string, requester secrets.Requester) (*secrets.Secret, error) {
	s.requester = requester
	if s.err != nil {
		return nil, s.err
	}
	return s.secret, nil
}
