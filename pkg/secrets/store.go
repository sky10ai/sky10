package secrets

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/kv"
)

const maxChunkSize = kv.MaxValueSize

var (
	// ErrNotFound indicates the requested secret is not present.
	ErrNotFound = errors.New("secret not found")

	// ErrAccessDenied indicates the current device cannot decrypt the secret.
	ErrAccessDenied = errors.New("secret access denied")

	// ErrApprovalRequired indicates the policy requires an out-of-band approval.
	ErrApprovalRequired = errors.New("secret access requires human approval")
)

// Config controls the internal KV transport used by the secrets service.
type Config struct {
	Namespace    string
	DataDir      string
	PollInterval time.Duration
}

// Store manages encrypted secret artifacts on top of KV transport.
type Store struct {
	bundle    *id.Bundle
	deviceID  string
	namespace string
	transport *kv.Store
	logger    *slog.Logger
}

type headPayload struct {
	SecretID           string       `json:"secret_id"`
	Name               string       `json:"name"`
	Kind               string       `json:"kind"`
	ContentType        string       `json:"content_type"`
	LatestVersionID    string       `json:"latest_version_id"`
	Size               int64        `json:"size"`
	SHA256             string       `json:"sha256"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
	RecipientDeviceIDs []string     `json:"recipient_device_ids"`
	Policy             AccessPolicy `json:"policy,omitempty"`
}

type versionPayload struct {
	SecretID    string    `json:"secret_id"`
	VersionID   string    `json:"version_id"`
	Kind        string    `json:"kind"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ChunkCount  int       `json:"chunk_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// New creates a secrets store backed by a private KV namespace.
func New(backend adapter.Backend, bundle *id.Bundle, config Config, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	if config.Namespace == "" {
		config.Namespace = DefaultNamespace
	}
	if config.DataDir == "" {
		home, _ := os.UserHomeDir()
		config.DataDir = filepath.Join(home, ".sky10", "secrets", "stores", config.Namespace)
	}
	expectedPeers := 0
	if bundle.Manifest != nil && len(bundle.Manifest.Devices) > 1 {
		expectedPeers = len(bundle.Manifest.Devices) - 1
	}

	transport := kv.New(backend, bundle.Identity, kv.Config{
		Namespace:          config.Namespace,
		DeviceID:           bundle.DeviceID(),
		ActorID:            bundle.DevicePubKeyHex(),
		DataDir:            filepath.Join(config.DataDir, "transport"),
		PollInterval:       config.PollInterval,
		RequireExistingKey: backend == nil && bundle.Manifest != nil && len(bundle.Manifest.Devices) > 1,
		ExpectedPeers:      expectedPeers,
	}, logger)

	return &Store{
		bundle:    bundle,
		deviceID:  bundle.DeviceID(),
		namespace: config.Namespace,
		transport: transport,
		logger:    logger,
	}
}

// Run starts the internal KV transport.
func (s *Store) Run(ctx context.Context) error {
	return s.transport.Run(ctx)
}

// SyncOnce performs one transport sync cycle.
func (s *Store) SyncOnce(ctx context.Context) error {
	return s.transport.SyncOnce(ctx)
}

// SetNotifier forwards upload notifications from the transport.
func (s *Store) SetNotifier(fn func(namespace string)) {
	s.transport.SetNotifier(fn)
}

// SetP2PSync attaches a shared KV P2P sync handler.
func (s *Store) SetP2PSync(sync *kv.P2PSync) {
	s.transport.SetP2PSync(sync)
}

// Poke triggers an immediate poll of remote snapshots.
func (s *Store) Poke() {
	s.transport.Poke()
}

// Transport returns the underlying KV transport store.
func (s *Store) Transport() *kv.Store {
	return s.transport
}

// Namespace returns the internal namespace name.
func (s *Store) Namespace() string {
	return s.namespace
}

// Devices returns the devices available for recipient selection.
func (s *Store) Devices() []Device {
	devices := s.manifestDevices()
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].Current != devices[j].Current {
			return devices[i].Current
		}
		return devices[i].Name < devices[j].Name
	})
	return devices
}

// List returns all secrets this device can decrypt.
func (s *Store) List() ([]SecretSummary, error) {
	keys := s.transport.List(headPrefix)
	summaries := make([]SecretSummary, 0, len(keys))
	for _, key := range keys {
		raw, ok := s.transport.Get(key)
		if !ok {
			continue
		}
		head, err := s.decryptHead(raw)
		if err != nil {
			if errors.Is(err, ErrAccessDenied) {
				continue
			}
			return nil, err
		}
		summaries = append(summaries, summaryFromHead(*head))
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Name != summaries[j].Name {
			return summaries[i].Name < summaries[j].Name
		}
		return summaries[i].ID < summaries[j].ID
	})
	return summaries, nil
}

// Status summarizes the current secrets store.
func (s *Store) Status() (map[string]interface{}, error) {
	items, err := s.List()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"namespace": s.namespace,
		"device_id": s.deviceID,
		"count":     len(items),
	}, nil
}

// Put writes a new secret or a new version of an existing secret.
func (s *Store) Put(ctx context.Context, params PutParams) (*SecretSummary, error) {
	if len(params.Payload) == 0 {
		return nil, fmt.Errorf("payload is required")
	}

	existing, err := s.findExisting(params.ID, params.Name)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		if params.ID == "" {
			params.ID = existing.SecretID
		}
		if params.Name == "" {
			params.Name = existing.Name
		}
		if params.Kind == "" {
			params.Kind = existing.Kind
		}
		if params.ContentType == "" {
			params.ContentType = existing.ContentType
		}
		if len(params.RecipientDeviceIDs) == 0 {
			params.RecipientDeviceIDs = append([]string(nil), existing.RecipientDeviceIDs...)
		}
		if params.Policy.IsZero() {
			params.Policy = existing.Policy
		}
	}

	if params.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if params.Kind == "" {
		params.Kind = KindBlob
	}
	if params.ContentType == "" {
		params.ContentType = "application/octet-stream"
	}
	if len(params.RecipientDeviceIDs) == 0 {
		params.RecipientDeviceIDs = []string{s.deviceID}
	}

	recipients, err := s.resolveRecipients(params.RecipientDeviceIDs)
	if err != nil {
		return nil, err
	}

	secretID := params.ID
	createdAt := time.Now().UTC()
	if existing != nil {
		createdAt = existing.CreatedAt
	} else if secretID == "" {
		secretID, err = newRandomID(16)
		if err != nil {
			return nil, err
		}
	}

	dataKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		return nil, fmt.Errorf("generating data key: %w", err)
	}

	ciphertext, err := skykey.Encrypt(params.Payload, dataKey)
	if err != nil {
		return nil, fmt.Errorf("encrypting secret payload: %w", err)
	}

	versionID, err := newRandomID(16)
	if err != nil {
		return nil, err
	}
	chunks := chunkBytes(ciphertext, maxChunkSize)
	for i, chunk := range chunks {
		if err := s.transport.Set(ctx, chunkKey(secretID, versionID, i), chunk); err != nil {
			return nil, fmt.Errorf("storing chunk %d: %w", i, err)
		}
	}

	sha := checksumHex(params.Payload)
	now := time.Now().UTC()
	versionPlain := versionPayload{
		SecretID:    secretID,
		VersionID:   versionID,
		Kind:        params.Kind,
		ContentType: params.ContentType,
		Size:        int64(len(params.Payload)),
		SHA256:      sha,
		ChunkCount:  len(chunks),
		CreatedAt:   now,
	}
	versionJSON, err := json.Marshal(versionPlain)
	if err != nil {
		return nil, fmt.Errorf("marshaling version metadata: %w", err)
	}
	versionValue, err := marshalSealedValue(versionJSON, dataKey, recipients)
	if err != nil {
		return nil, err
	}
	if err := s.transport.Set(ctx, versionMetaKey(secretID, versionID), versionValue); err != nil {
		return nil, fmt.Errorf("storing version metadata: %w", err)
	}

	headPlain := headPayload{
		SecretID:           secretID,
		Name:               params.Name,
		Kind:               params.Kind,
		ContentType:        params.ContentType,
		LatestVersionID:    versionID,
		Size:               int64(len(params.Payload)),
		SHA256:             sha,
		CreatedAt:          createdAt,
		UpdatedAt:          now,
		RecipientDeviceIDs: recipientIDs(recipients),
		Policy:             params.Policy,
	}
	headJSON, err := json.Marshal(headPlain)
	if err != nil {
		return nil, fmt.Errorf("marshaling head metadata: %w", err)
	}
	headValue, err := marshalSealedValue(headJSON, dataKey, recipients)
	if err != nil {
		return nil, err
	}
	if err := s.transport.Set(ctx, headKey(secretID), headValue); err != nil {
		return nil, fmt.Errorf("storing head metadata: %w", err)
	}

	summary := summaryFromHead(headPlain)
	return &summary, nil
}

// Get decrypts the latest version of a secret.
func (s *Store) Get(idOrName string, requester Requester) (*Secret, error) {
	head, err := s.resolveHead(idOrName)
	if err != nil {
		return nil, err
	}
	if err := s.authorize(head.Policy, requester); err != nil {
		return nil, err
	}

	rawMeta, ok := s.transport.Get(versionMetaKey(head.SecretID, head.LatestVersionID))
	if !ok {
		return nil, ErrNotFound
	}
	metaJSON, dataKey, err := unsealValue(rawMeta, s.bundle.Device.PrivateKey)
	if err != nil {
		if errors.Is(err, errNoWrappedKey) {
			return nil, ErrAccessDenied
		}
		return nil, err
	}

	var version versionPayload
	if err := json.Unmarshal(metaJSON, &version); err != nil {
		return nil, fmt.Errorf("parsing version metadata: %w", err)
	}

	ciphertext := make([]byte, 0, version.Size)
	for i := 0; i < version.ChunkCount; i++ {
		chunk, ok := s.transport.Get(chunkKey(head.SecretID, version.VersionID, i))
		if !ok {
			return nil, fmt.Errorf("secret chunk missing: %d", i)
		}
		ciphertext = append(ciphertext, chunk...)
	}

	payload, err := skykey.Decrypt(ciphertext, dataKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting payload: %w", err)
	}

	return &Secret{
		SecretSummary: summaryFromHead(*head),
		VersionID:     version.VersionID,
		Payload:       payload,
	}, nil
}

// Rewrap rotates a secret to a fresh data key and recipient set.
func (s *Store) Rewrap(ctx context.Context, params RewrapParams) (*SecretSummary, error) {
	head, err := s.resolveHead(params.IDOrName)
	if err != nil {
		return nil, err
	}
	secret, err := s.Get(params.IDOrName, Requester{Type: RequesterOwner})
	if err != nil {
		return nil, err
	}

	if len(params.RecipientDeviceIDs) == 0 {
		params.RecipientDeviceIDs = append([]string(nil), head.RecipientDeviceIDs...)
	}
	if params.Policy.IsZero() {
		params.Policy = head.Policy
	}

	return s.Put(ctx, PutParams{
		ID:                 head.SecretID,
		Name:               head.Name,
		Kind:               head.Kind,
		ContentType:        head.ContentType,
		Payload:            secret.Payload,
		RecipientDeviceIDs: params.RecipientDeviceIDs,
		Policy:             params.Policy,
	})
}

func (s *Store) findExisting(idValue, name string) (*headPayload, error) {
	if idValue != "" {
		head, err := s.resolveHead(idValue)
		if err == nil {
			return head, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if name == "" {
		return nil, nil
	}
	head, err := s.findHeadByName(name)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return head, err
}

func (s *Store) resolveHead(idOrName string) (*headPayload, error) {
	if idOrName == "" {
		return nil, ErrNotFound
	}
	if raw, ok := s.transport.Get(headKey(idOrName)); ok {
		head, err := s.decryptHead(raw)
		if err != nil {
			return nil, err
		}
		return head, nil
	}
	return s.findHeadByName(idOrName)
}

func (s *Store) decryptHead(raw []byte) (*headPayload, error) {
	headJSON, _, err := unsealValue(raw, s.bundle.Device.PrivateKey)
	if err != nil {
		if errors.Is(err, errNoWrappedKey) {
			return nil, ErrAccessDenied
		}
		return nil, err
	}
	var head headPayload
	if err := json.Unmarshal(headJSON, &head); err != nil {
		return nil, fmt.Errorf("parsing head metadata: %w", err)
	}
	return &head, nil
}

func (s *Store) findHeadByName(name string) (*headPayload, error) {
	keys := s.transport.List(headPrefix)
	for _, key := range keys {
		raw, ok := s.transport.Get(key)
		if !ok {
			continue
		}
		head, err := s.decryptHead(raw)
		if err != nil {
			if errors.Is(err, ErrAccessDenied) {
				continue
			}
			return nil, err
		}
		if head.Name == name {
			return head, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) authorize(policy AccessPolicy, requester Requester) error {
	switch requester.Type {
	case "", RequesterOwner:
		return nil
	case RequesterAgent:
		if requester.ID == "" {
			return ErrAccessDenied
		}
		if policy.RequireApproval {
			return ErrApprovalRequired
		}
		if len(policy.AllowedAgents) == 0 {
			return ErrAccessDenied
		}
		for _, allowed := range policy.AllowedAgents {
			if allowed == requester.ID {
				return nil
			}
		}
		return ErrAccessDenied
	default:
		return ErrAccessDenied
	}
}

func (s *Store) resolveRecipients(ids []string) ([]recipientRef, error) {
	all := s.manifestRecipientMap()
	if len(ids) == 1 && strings.EqualFold(ids[0], "all") {
		ids = ids[:0]
		for id := range all {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		ids = []string{s.deviceID}
	}

	seen := make(map[string]bool, len(ids))
	recipients := make([]recipientRef, 0, len(ids))
	for _, idValue := range ids {
		idValue = strings.TrimSpace(idValue)
		if idValue == "" || seen[idValue] {
			continue
		}
		recipient, ok := all[idValue]
		if !ok {
			return nil, fmt.Errorf("unknown recipient device: %s", idValue)
		}
		seen[idValue] = true
		recipients = append(recipients, recipient)
	}
	sort.Slice(recipients, func(i, j int) bool {
		return recipients[i].DeviceID < recipients[j].DeviceID
	})
	return recipients, nil
}

func (s *Store) manifestDevices() []Device {
	all := s.manifestRecipientMap()
	out := make([]Device, 0, len(all))
	for _, recipient := range all {
		out = append(out, Device{
			ID:      recipient.DeviceID,
			Name:    recipient.Name,
			Current: recipient.DeviceID == s.deviceID,
		})
	}
	return out
}

func (s *Store) manifestRecipientMap() map[string]recipientRef {
	out := make(map[string]recipientRef)
	for _, entry := range s.bundle.Manifest.Devices {
		if len(entry.PublicKey) != ed25519.PublicKeySize {
			continue
		}
		pub := ed25519.PublicKey(entry.PublicKey)
		idValue := deviceIDFromPublicKey(pub)
		out[idValue] = recipientRef{
			DeviceID:  idValue,
			Name:      entry.Name,
			PublicKey: pub,
		}
	}

	currentID := s.deviceID
	if _, ok := out[currentID]; !ok {
		out[currentID] = recipientRef{
			DeviceID:  currentID,
			Name:      "current",
			PublicKey: s.bundle.Device.PublicKey,
		}
	}
	return out
}

func recipientIDs(recipients []recipientRef) []string {
	ids := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		ids = append(ids, recipient.DeviceID)
	}
	return ids
}

func summaryFromHead(head headPayload) SecretSummary {
	return SecretSummary{
		ID:                 head.SecretID,
		Name:               head.Name,
		Kind:               head.Kind,
		ContentType:        head.ContentType,
		Size:               head.Size,
		SHA256:             head.SHA256,
		CreatedAt:          head.CreatedAt,
		UpdatedAt:          head.UpdatedAt,
		RecipientDeviceIDs: append([]string(nil), head.RecipientDeviceIDs...),
		Policy:             head.Policy,
	}
}

func deviceIDFromPublicKey(pub ed25519.PublicKey) string {
	return "D-" + skykey.FromPublicKey(pub).ShortID()
}

const (
	headPrefix = "heads/"
)

func headKey(secretID string) string {
	return headPrefix + secretID
}

func versionMetaKey(secretID, versionID string) string {
	return fmt.Sprintf("versions/%s/%s/meta", secretID, versionID)
}

func chunkKey(secretID, versionID string, idx int) string {
	return fmt.Sprintf("versions/%s/%s/chunk/%06d", secretID, versionID, idx)
}
