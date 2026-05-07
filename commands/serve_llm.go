package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/secrets"
)

type llmSecretResolver struct {
	store *secrets.Store
}

func (r llmSecretResolver) ResolveSecret(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("secret reference is required")
	}
	if r.store == nil {
		return "", fmt.Errorf("secrets store is not configured")
	}
	secret, err := r.store.Get(ref, secrets.Requester{Type: secrets.RequesterOwner})
	if err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return "", fmt.Errorf("secret %q not found", ref)
		}
		return "", err
	}
	return strings.TrimSpace(string(secret.Payload)), nil
}
