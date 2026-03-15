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
func GrantAccess(ctx context.Context, backend adapter.Backend, identity *Identity, namespace string, recipientPub ed25519.PublicKey) error {
	// Load namespace key (unwrap with our private key)
	nsKeyPath := "keys/namespaces/" + namespace + ".ns.enc"
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
	recipientKeyPath := "keys/namespaces/" + namespace + "." + recipientID + ".ns.enc"

	r := bytes.NewReader(recipientWrapped)
	if err := backend.Put(ctx, recipientKeyPath, r, int64(len(recipientWrapped))); err != nil {
		return fmt.Errorf("storing recipient key: %w", err)
	}

	return nil
}

// RevokeAccess removes a recipient's wrapped namespace key and rotates
// the namespace key so the revoked party cannot decrypt future data.
func RevokeAccess(ctx context.Context, backend adapter.Backend, identity *Identity, namespace string, recipientPub ed25519.PublicKey) error {
	recipientID := shortID(recipientPub)
	recipientKeyPath := "keys/namespaces/" + namespace + "." + recipientID + ".ns.enc"

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
func RotateNamespaceKey(ctx context.Context, backend adapter.Backend, identity *Identity, namespace string) error {
	// Load old namespace key
	nsKeyPath := "keys/namespaces/" + namespace + ".ns.enc"
	rc, err := backend.Get(ctx, nsKeyPath)
	if err != nil {
		return fmt.Errorf("loading namespace key: %w", err)
	}
	oldWrapped, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return fmt.Errorf("reading namespace key: %w", err)
	}

	oldNsKey, err := UnwrapNamespaceKey(oldWrapped, identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("unwrapping old namespace key: %w", err)
	}

	// Generate new namespace key
	newNsKey, err := GenerateNamespaceKey()
	if err != nil {
		return fmt.Errorf("generating new namespace key: %w", err)
	}

	// Load current state to find all files in this namespace
	store := New(backend, identity)
	state, err := store.loadCurrentState(ctx)
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// Re-wrap file keys: decrypt with old ns key, re-encrypt with new ns key
	// In our design, file keys are derived from namespace key + content hash
	// via HKDF, so we don't need to re-wrap individual file keys. The new
	// namespace key will derive different file keys, but since chunks are
	// content-addressed by plaintext hash (not by encryption key), the
	// existing encrypted blobs won't be readable with the new namespace key.
	//
	// For rotation to work without re-encrypting data, we need to re-encrypt
	// each chunk with the new file key. This is the per-file cost.
	for path, entry := range state.Tree {
		if entry.Namespace != namespace {
			continue
		}

		for _, chunkHash := range entry.Chunks {
			// Decrypt with old key
			oldFileKey, err := DeriveFileKey(oldNsKey, []byte(chunkHash))
			if err != nil {
				return fmt.Errorf("deriving old file key for %s: %w", path, err)
			}

			blobKey := (&Chunk{Hash: chunkHash}).BlobKey()
			blobRC, err := backend.Get(ctx, blobKey)
			if err != nil {
				return fmt.Errorf("downloading chunk %s: %w", chunkHash[:12], err)
			}
			raw, err := io.ReadAll(blobRC)
			blobRC.Close()
			if err != nil {
				return fmt.Errorf("reading chunk %s: %w", chunkHash[:12], err)
			}

			// Strip blob header, decrypt with old key
			encrypted, _, err := StripBlobHeader(raw)
			if err != nil {
				return fmt.Errorf("parsing chunk %s header: %w", chunkHash[:12], err)
			}

			compressed, err := Decrypt(encrypted, oldFileKey)
			if err != nil {
				return fmt.Errorf("decrypting chunk %s: %w", chunkHash[:12], err)
			}

			// Re-encrypt with new key, prepend header
			// Keep the same compressed payload — no need to decompress/recompress
			newFileKey, err := DeriveFileKey(newNsKey, []byte(chunkHash))
			if err != nil {
				return fmt.Errorf("deriving new file key for %s: %w", path, err)
			}

			newEncrypted, err := Encrypt(compressed, newFileKey)
			if err != nil {
				return fmt.Errorf("re-encrypting chunk %s: %w", chunkHash[:12], err)
			}

			blob := PrependBlobHeader(newEncrypted)
			r := bytes.NewReader(blob)
			if err := backend.Put(ctx, blobKey, r, int64(len(blob))); err != nil {
				return fmt.Errorf("uploading re-encrypted chunk %s: %w", chunkHash[:12], err)
			}
		}
	}

	// Wrap new namespace key for our identity
	newWrapped, err := WrapNamespaceKey(newNsKey, identity.PublicKey)
	if err != nil {
		return fmt.Errorf("wrapping new namespace key: %w", err)
	}

	r := bytes.NewReader(newWrapped)
	if err := backend.Put(ctx, nsKeyPath, r, int64(len(newWrapped))); err != nil {
		return fmt.Errorf("storing new namespace key: %w", err)
	}

	// Re-wrap for any other authorized identities
	otherKeys, err := ListAuthorizedKeys(ctx, backend, namespace)
	if err != nil {
		return fmt.Errorf("listing authorized keys: %w", err)
	}

	for _, keyPath := range otherKeys {
		// We can't re-wrap for others without their public key stored somewhere.
		// For now, delete their old wrapped key — they'll need to be re-granted.
		// TODO: store public keys alongside wrapped keys for automatic re-wrapping.
		backend.Delete(ctx, keyPath)
	}

	return nil
}

// ListAuthorizedKeys returns the S3 keys of all per-identity namespace key
// wrappings (excluding the primary .ns.enc file).
func ListAuthorizedKeys(ctx context.Context, backend adapter.Backend, namespace string) ([]string, error) {
	prefix := "keys/namespaces/" + namespace + "."
	keys, err := backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	// Filter out the primary key file
	primary := "keys/namespaces/" + namespace + ".ns.enc"
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
