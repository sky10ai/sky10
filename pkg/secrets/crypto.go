package secrets

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	skykey "github.com/sky10/sky10/pkg/key"
)

var errNoWrappedKey = errors.New("wrapped key for current device not found")

type recipientRef struct {
	DeviceID  string
	Name      string
	PublicKey ed25519.PublicKey
}

type wrappedKey struct {
	RecipientType string `json:"recipient_type"`
	RecipientID   string `json:"recipient_id"`
	Wrapped       []byte `json:"wrapped"`
}

type sealedValue struct {
	Version     int          `json:"version"`
	WrappedKeys []wrappedKey `json:"wrapped_keys"`
	Ciphertext  []byte       `json:"ciphertext"`
}

func newRandomID(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random ID: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func chunkBytes(data []byte, size int) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for len(data) > 0 {
		n := size
		if len(data) < n {
			n = len(data)
		}
		out = append(out, data[:n])
		data = data[n:]
	}
	return out
}

func marshalSealedValue(plain, dataKey []byte, recipients []recipientRef) ([]byte, error) {
	ciphertext, err := skykey.Encrypt(plain, dataKey)
	if err != nil {
		return nil, fmt.Errorf("encrypting sealed value: %w", err)
	}

	wrappedKeys := make([]wrappedKey, 0, len(recipients))
	for _, recipient := range recipients {
		wrapped, err := skykey.WrapKey(dataKey, recipient.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("wrapping data key for %s: %w", recipient.DeviceID, err)
		}
		wrappedKeys = append(wrappedKeys, wrappedKey{
			RecipientType: "device",
			RecipientID:   recipient.DeviceID,
			Wrapped:       wrapped,
		})
	}

	return json.Marshal(sealedValue{
		Version:     1,
		WrappedKeys: wrappedKeys,
		Ciphertext:  ciphertext,
	})
}

func unsealValue(raw []byte, recipientPriv ed25519.PrivateKey) ([]byte, []byte, error) {
	var value sealedValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, nil, fmt.Errorf("parsing sealed value: %w", err)
	}

	dataKey, err := unwrapDataKey(value, recipientPriv)
	if err != nil {
		return nil, nil, err
	}

	plain, err := skykey.Decrypt(value.Ciphertext, dataKey)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypting sealed value: %w", err)
	}
	return plain, dataKey, nil
}

func unwrapDataKey(value sealedValue, recipientPriv ed25519.PrivateKey) ([]byte, error) {
	for _, wrapped := range value.WrappedKeys {
		dataKey, err := skykey.UnwrapKey(wrapped.Wrapped, recipientPriv)
		if err == nil {
			return dataKey, nil
		}
	}
	return nil, errNoWrappedKey
}
