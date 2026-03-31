package fs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sky10/sky10/pkg/adapter"
)

// GrantAccess wraps the namespace key for a recipient's public key,
// allowing them to decrypt files in that namespace.
func GrantAccess(ctx context.Context, backend adapter.Backend, identity *DeviceKey, namespace string, recipientPub ed25519.PublicKey) error {
	// Load namespace key (unwrap with our private key)
	nsKeyPath := "keys/namespaces/" + nsKeyName(namespace) + ".ns.enc"
	rc, err := backend.Get(ctx, nsKeyPath)
	if err != nil {
		return fmt.Errorf("loading namespace key: %w", err)
	}
	wrapped, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("reading namespace key: %w", err)
	}

	nsKey, err := UnwrapNamespaceKey(wrapped, identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("unwrapping namespace key: %w", err)
	}

	// Wrap for recipient
	recipientWrapped, err := WrapNamespaceKey(nsKey, recipientPub)
	if err != nil {
		return fmt.Errorf("wrapping for recipient: %w", err)
	}

	recipientID := shortID(recipientPub)
	recipientKeyPath := "keys/namespaces/" + nsKeyName(namespace) + "." + recipientID + ".ns.enc"

	r := bytes.NewReader(recipientWrapped)
	if err := backend.Put(ctx, recipientKeyPath, r, int64(len(recipientWrapped))); err != nil {
		return fmt.Errorf("storing recipient key: %w", err)
	}

	return nil
}

// RevokeAccess removes a recipient's wrapped namespace key and rotates
// the namespace key so the revoked party cannot decrypt future data.
func RevokeAccess(ctx context.Context, backend adapter.Backend, identity *DeviceKey, namespace string, recipientPub ed25519.PublicKey) error {
	recipientID := shortID(recipientPub)
	recipientKeyPath := "keys/namespaces/" + nsKeyName(namespace) + "." + recipientID + ".ns.enc"

	// Delete recipient's wrapped key
	err := backend.Delete(ctx, recipientKeyPath)
	if err != nil && !errors.Is(err, adapter.ErrNotFound) {
		return fmt.Errorf("deleting recipient key: %w", err)
	}

	// Rotate namespace key so revoked party can't use cached key
	return RotateNamespaceKey(ctx, backend, identity, namespace)
}

// RotateNamespaceKey generates a new namespace key and re-wraps it for
// all authorized identities. File data is NOT re-encrypted — only the
// tiny key metadata changes.
func RotateNamespaceKey(ctx context.Context, backend adapter.Backend, identity *DeviceKey, namespace string) error {
	// Load old namespace key
	nsKeyPath := "keys/namespaces/" + nsKeyName(namespace) + ".ns.enc"
	rc, err := backend.Get(ctx, nsKeyPath)
	if err != nil {
		return fmt.Errorf("loading namespace key: %w", err)
	}
	oldWrapped, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("reading namespace key: %w", err)
	}

	_, err = UnwrapNamespaceKey(oldWrapped, identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("unwrapping old namespace key: %w", err)
	}

	// TODO(snapshot-exchange): key rotation needs the local CRDT snapshot,
	// not the S3 ops log. Requires passing the local ops log in.
	return fmt.Errorf("key rotation not yet implemented in snapshot-exchange architecture")
}

// ListAuthorizedKeys returns the S3 keys of all per-identity namespace key
// wrappings (excluding the primary .ns.enc file).
func ListAuthorizedKeys(ctx context.Context, backend adapter.Backend, namespace string) ([]string, error) {
	prefix := "keys/namespaces/" + nsKeyName(namespace) + "."
	keys, err := backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	// Filter out the primary key file
	primary := "keys/namespaces/" + nsKeyName(namespace) + ".ns.enc"
	var result []string
	for _, k := range keys {
		if k != primary && strings.HasSuffix(k, ".ns.enc") {
			result = append(result, k)
		}
	}
	return result, nil
}

// shortID returns a short identifier for a public key (first 8 chars of base64).
func shortID(pub ed25519.PublicKey) string {
	encoded := base64.RawURLEncoding.EncodeToString(pub)
	if len(encoded) > 8 {
		return encoded[:8]
	}
	return encoded
}
