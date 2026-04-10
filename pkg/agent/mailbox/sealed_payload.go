package mailbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv/collections"
)

const defaultSealedPayloadRoot = "mailbox/payloads"

// SealedPayloadStore persists mailbox payload bytes as recipient-sealed opaque
// objects in indirect storage.
type SealedPayloadStore struct {
	backend adapter.Backend
	root    string
}

// NewSealedPayloadStore creates an indirect storage helper for sky10-network
// payload refs.
func NewSealedPayloadStore(backend adapter.Backend, root string) *SealedPayloadStore {
	root = strings.TrimSuffix(strings.TrimSpace(root), "/")
	if root == "" {
		root = defaultSealedPayloadRoot
	}
	return &SealedPayloadStore{backend: backend, root: root}
}

// PutForRecipient seals payload bytes to the recipient's sky10 address and
// stores the ciphertext as an opaque object.
func (s *SealedPayloadStore) PutForRecipient(ctx context.Context, recipient string, payload []byte) (PayloadRef, error) {
	if s == nil || s.backend == nil {
		return PayloadRef{}, fmt.Errorf("sealed payload backend is required")
	}
	sealed, err := skykey.SealFor(payload, recipient)
	if err != nil {
		return PayloadRef{}, fmt.Errorf("seal payload for %s: %w", recipient, err)
	}

	key := s.objectKey(sealed)
	if err := s.backend.Put(ctx, key, bytes.NewReader(sealed), int64(len(sealed))); err != nil {
		return PayloadRef{}, fmt.Errorf("store sealed payload %s: %w", key, err)
	}
	return collections.NewPayloadRef(
		collections.PayloadKindSealedObject,
		key,
		len(sealed),
		collections.SHA256Digest(payload),
	)
}

// Open reads a sealed payload ref and decrypts it with the recipient key.
func (s *SealedPayloadStore) Open(ctx context.Context, ref PayloadRef, recipient *skykey.Key) ([]byte, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("sealed payload backend is required")
	}
	if recipient == nil || !recipient.IsPrivate() {
		return nil, fmt.Errorf("recipient private key is required")
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	if ref.Kind != collections.PayloadKindSealedObject {
		return nil, fmt.Errorf("payload ref kind %q is not sealed_object", ref.Kind)
	}

	rc, err := s.backend.Get(ctx, ref.Key)
	if err != nil {
		return nil, fmt.Errorf("load sealed payload %s: %w", ref.Key, err)
	}
	defer rc.Close()

	sealed, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read sealed payload %s: %w", ref.Key, err)
	}
	plain, err := skykey.Open(sealed, recipient.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("open sealed payload %s: %w", ref.Key, err)
	}
	return plain, nil
}

func (s *SealedPayloadStore) objectKey(sealed []byte) string {
	return s.root + "/" + strings.TrimPrefix(collections.SHA256Digest(sealed), "sha256:")
}
