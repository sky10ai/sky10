package broker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	"github.com/sky10/sky10/pkg/secrets"
)

const defaultCredentialContentType = "application/octet-stream"

// CredentialMaterial is one resolved credential payload ready to be staged for
// an adapter process.
type CredentialMaterial struct {
	Ref         string
	ContentType string
	Payload     []byte
	Metadata    map[string]string
}

// CredentialResolver resolves a durable `credential_ref` into broker-owned
// secret material for one adapter invocation.
type CredentialResolver interface {
	ResolveMessagingCredential(ctx context.Context, ref string) (CredentialMaterial, error)
}

// CredentialResolverFunc adapts a function into a CredentialResolver.
type CredentialResolverFunc func(ctx context.Context, ref string) (CredentialMaterial, error)

// ResolveMessagingCredential implements CredentialResolver.
func (fn CredentialResolverFunc) ResolveMessagingCredential(ctx context.Context, ref string) (CredentialMaterial, error) {
	if fn == nil {
		return CredentialMaterial{}, fmt.Errorf("credential resolver is nil")
	}
	return fn(ctx, ref)
}

// SecretsGetter is the minimal secrets store surface used by the broker.
type SecretsGetter interface {
	Get(idOrName string, requester secrets.Requester) (*secrets.Secret, error)
}

// SecretsResolver resolves messaging credentials through pkg/secrets.
type SecretsResolver struct {
	Store     SecretsGetter
	Requester secrets.Requester
}

// ResolveMessagingCredential implements CredentialResolver.
func (r SecretsResolver) ResolveMessagingCredential(ctx context.Context, ref string) (CredentialMaterial, error) {
	_ = ctx
	if r.Store == nil {
		return CredentialMaterial{}, fmt.Errorf("secrets store is required")
	}
	requester := r.Requester
	if strings.TrimSpace(requester.Type) == "" {
		requester.Type = secrets.RequesterOwner
	}
	secret, err := r.Store.Get(ref, requester)
	if err != nil {
		return CredentialMaterial{}, fmt.Errorf("get messaging credential %q: %w", ref, err)
	}
	return CredentialMaterial{
		Ref:         ref,
		ContentType: secret.ContentType,
		Payload:     append([]byte(nil), secret.Payload...),
	}, nil
}

func (b *Broker) resolveConnectionCredential(ctx context.Context, connection messaging.Connection, paths protocol.RuntimePaths) (*protocol.ResolvedCredential, error) {
	ref := strings.TrimSpace(connection.Auth.CredentialRef)
	if ref == "" {
		return nil, nil
	}
	if b.credentialResolver == nil {
		return nil, fmt.Errorf("messaging connection %s requires a credential resolver for %q", connection.ID, ref)
	}
	material, err := b.credentialResolver.ResolveMessagingCredential(ctx, ref)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(material.Ref) == "" {
		material.Ref = ref
	}
	blob, err := stageCredential(paths, material)
	if err != nil {
		return nil, err
	}
	return &protocol.ResolvedCredential{
		Ref:         material.Ref,
		AuthMethod:  connection.Auth.Method,
		ContentType: blob.ContentType,
		Blob:        blob,
		Metadata:    cloneStringMap(material.Metadata),
	}, nil
}

func stageCredential(paths protocol.RuntimePaths, material CredentialMaterial) (protocol.BlobRef, error) {
	ref := strings.TrimSpace(material.Ref)
	if ref == "" {
		return protocol.BlobRef{}, fmt.Errorf("credential ref is required")
	}
	if len(material.Payload) == 0 {
		return protocol.BlobRef{}, fmt.Errorf("credential payload for %q is empty", ref)
	}
	if strings.TrimSpace(paths.SecretsDir) == "" {
		return protocol.BlobRef{}, fmt.Errorf("credential staging requires a secrets_dir")
	}
	if err := os.MkdirAll(paths.SecretsDir, 0o700); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("create credential staging dir: %w", err)
	}
	fileName := encodePathSegment(ref) + ".bin"
	localPath := filepath.Join(paths.SecretsDir, fileName)
	if err := os.WriteFile(localPath, material.Payload, 0o600); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("write staged credential: %w", err)
	}
	if err := os.Chmod(localPath, 0o600); err != nil {
		return protocol.BlobRef{}, fmt.Errorf("chmod staged credential: %w", err)
	}
	contentType := strings.TrimSpace(material.ContentType)
	if contentType == "" {
		contentType = defaultCredentialContentType
	}
	digest := sha256.Sum256(material.Payload)
	return protocol.BlobRef{
		ID:          "credential:" + encodePathSegment(ref),
		LocalPath:   localPath,
		ContentType: contentType,
		SizeBytes:   int64(len(material.Payload)),
		SHA256:      hex.EncodeToString(digest[:]),
	}, nil
}
