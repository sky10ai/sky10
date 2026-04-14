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
	Scope              string       `json:"scope,omitempty"`
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
	keys := s.listHeadKeys()
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
	if rawScope := strings.TrimSpace(params.Scope); rawScope != "" {
		params.Scope = NormalizeSecretScope(rawScope)
		if params.Scope == "" {
			return nil, fmt.Errorf("unknown scope: %s", rawScope)
		}
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
		if params.Scope == "" {
			params.Scope = existing.Scope
		}
		if len(params.RecipientDeviceIDs) == 0 && params.Scope == ScopeExplicit {
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
	if params.Scope == "" {
		switch {
		case len(params.RecipientDeviceIDs) == 1 && strings.EqualFold(strings.TrimSpace(params.RecipientDeviceIDs[0]), "all"):
			params.Scope = ScopeTrusted
			params.RecipientDeviceIDs = nil
		case len(params.RecipientDeviceIDs) > 0:
			params.Scope = ScopeExplicit
		default:
			params.Scope = ScopeCurrent
		}
	}
	if params.Scope == ScopeCurrent && len(params.RecipientDeviceIDs) > 0 {
		return nil, fmt.Errorf("scope %q does not accept explicit recipient devices", ScopeCurrent)
	}
	if params.Scope == ScopeTrusted && len(params.RecipientDeviceIDs) > 0 {
		return nil, fmt.Errorf("scope %q does not accept explicit recipient devices", ScopeTrusted)
	}
	if params.Scope == ScopeExplicit && len(params.RecipientDeviceIDs) == 0 {
		return nil, fmt.Errorf("scope %q requires at least one recipient device", ScopeExplicit)
	}

	recipients, err := s.resolveRecipients(params.Scope, params.RecipientDeviceIDs)
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
		Scope:              params.Scope,
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

	rawMeta, ok := s.resolveVersionMeta(head.SecretID, head.LatestVersionID)
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
		chunk, ok := s.resolveChunk(head.SecretID, version.VersionID, i)
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
		if head.Scope == ScopeExplicit {
			params.RecipientDeviceIDs = append([]string(nil), head.RecipientDeviceIDs...)
		}
	}
	if params.Scope == "" {
		params.Scope = head.Scope
	}
	if params.Policy.IsZero() {
		params.Policy = head.Policy
	}

	return s.Put(ctx, PutParams{
		ID:                 head.SecretID,
		Name:               head.Name,
		Kind:               head.Kind,
		ContentType:        head.ContentType,
		Scope:              params.Scope,
		Payload:            secret.Payload,
		RecipientDeviceIDs: params.RecipientDeviceIDs,
		Policy:             params.Policy,
	})
}

// ReconcileTrustedScope rewraps trusted-scope secrets whose resolved recipient
// set no longer matches the current trusted-device manifest.
func (s *Store) ReconcileTrustedScope(ctx context.Context) (int, error) {
	keys := s.listHeadKeys()
	trustedRecipients, err := s.resolveTrustedRecipients()
	if err != nil {
		return 0, err
	}
	trustedIDs := recipientIDs(trustedRecipients)

	rewrapped := 0
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
			return rewrapped, err
		}
		if head.Scope != ScopeTrusted {
			continue
		}
		if sameRecipientIDSet(head.RecipientDeviceIDs, trustedIDs) {
			continue
		}
		if _, err := s.Rewrap(ctx, RewrapParams{
			IDOrName: head.SecretID,
			Scope:    ScopeTrusted,
		}); err != nil {
			return rewrapped, fmt.Errorf("rewrapping trusted secret %s: %w", head.SecretID, err)
		}
		rewrapped++
	}
	return rewrapped, nil
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
	if raw, ok := s.transport.Get(legacyHeadKey(idOrName)); ok {
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
	head.Scope = InferSecretScope(head.Scope, head.RecipientDeviceIDs, s.deviceID)
	return &head, nil
}

func (s *Store) findHeadByName(name string) (*headPayload, error) {
	keys := s.listHeadKeys()
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

func (s *Store) resolveRecipients(scope string, ids []string) ([]recipientRef, error) {
	switch scope {
	case ScopeCurrent:
		return s.resolveExplicitRecipients([]string{s.deviceID})
	case ScopeTrusted:
		return s.resolveTrustedRecipients()
	case ScopeExplicit:
		return s.resolveExplicitRecipients(ids)
	default:
		return nil, fmt.Errorf("unknown scope: %s", scope)
	}
}

func (s *Store) resolveExplicitRecipients(ids []string) ([]recipientRef, error) {
	all := s.manifestRecipientMap()
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

func (s *Store) resolveTrustedRecipients() ([]recipientRef, error) {
	all := s.manifestRecipientMap()
	recipients := make([]recipientRef, 0, len(all))
	for _, recipient := range all {
		if recipient.Role != id.DeviceRoleTrusted {
			continue
		}
		recipients = append(recipients, recipient)
	}
	sort.Slice(recipients, func(i, j int) bool {
		return recipients[i].DeviceID < recipients[j].DeviceID
	})
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no trusted recipient devices available")
	}
	return recipients, nil
}

func (s *Store) manifestDevices() []Device {
	all := s.manifestRecipientMap()
	out := make([]Device, 0, len(all))
	for _, recipient := range all {
		out = append(out, Device{
			ID:      recipient.DeviceID,
			Name:    recipient.Name,
			Role:    recipient.Role,
			Current: recipient.DeviceID == s.deviceID,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
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
			Role:      id.NormalizeDeviceRole(entry.Role),
			PublicKey: pub,
		}
	}

	currentID := s.deviceID
	if _, ok := out[currentID]; !ok {
		out[currentID] = recipientRef{
			DeviceID:  currentID,
			Name:      "current",
			Role:      id.DeviceRoleTrusted,
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

func sameRecipientIDSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func summaryFromHead(head headPayload) SecretSummary {
	return SecretSummary{
		ID:                 head.SecretID,
		Name:               head.Name,
		Kind:               head.Kind,
		ContentType:        head.ContentType,
		Scope:              head.Scope,
		Size:               head.Size,
		SHA256:             head.SHA256,
		CreatedAt:          head.CreatedAt,
		UpdatedAt:          head.UpdatedAt,
		RecipientDeviceIDs: append([]string(nil), head.RecipientDeviceIDs...),
		Policy:             head.Policy,
	}
}

func (s *Store) listHeadKeys() []string {
	keysByID := make(map[string]string)
	for _, key := range s.transport.List(legacyHeadPrefix) {
		if secretID, ok := secretIDFromHeadKey(key); ok {
			keysByID[secretID] = key
		}
	}
	for _, key := range s.transport.List(headPrefix) {
		if secretID, ok := secretIDFromHeadKey(key); ok {
			keysByID[secretID] = key
		}
	}
	keys := make([]string, 0, len(keysByID))
	for _, key := range keysByID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *Store) resolveVersionMeta(secretID, versionID string) ([]byte, bool) {
	if raw, ok := s.transport.Get(versionMetaKey(secretID, versionID)); ok {
		return raw, true
	}
	return s.transport.Get(legacyVersionMetaKey(secretID, versionID))
}

func (s *Store) resolveChunk(secretID, versionID string, idx int) ([]byte, bool) {
	if raw, ok := s.transport.Get(chunkKey(secretID, versionID, idx)); ok {
		return raw, true
	}
	return s.transport.Get(legacyChunkKey(secretID, versionID, idx))
}

func deviceIDFromPublicKey(pub ed25519.PublicKey) string {
	return "D-" + skykey.FromPublicKey(pub).ShortID()
}

const (
	internalPrefix    = "_sys/secrets/"
	headPrefix        = internalPrefix + "heads/"
	legacyHeadPrefix  = "heads/"
	versionPrefix     = internalPrefix + "versions/"
	legacyVersionPath = "versions/"
)

func headKey(secretID string) string {
	return headPrefix + secretID
}

func legacyHeadKey(secretID string) string {
	return legacyHeadPrefix + secretID
}

func versionMetaKey(secretID, versionID string) string {
	return fmt.Sprintf("%s%s/%s/meta", versionPrefix, secretID, versionID)
}

func legacyVersionMetaKey(secretID, versionID string) string {
	return fmt.Sprintf("%s%s/%s/meta", legacyVersionPath, secretID, versionID)
}

func chunkKey(secretID, versionID string, idx int) string {
	return fmt.Sprintf("%s%s/%s/chunk/%06d", versionPrefix, secretID, versionID, idx)
}

func legacyChunkKey(secretID, versionID string, idx int) string {
	return fmt.Sprintf("%s%s/%s/chunk/%06d", legacyVersionPath, secretID, versionID, idx)
}

func secretIDFromHeadKey(key string) (string, bool) {
	switch {
	case strings.HasPrefix(key, headPrefix):
		return strings.TrimPrefix(key, headPrefix), true
	case strings.HasPrefix(key, legacyHeadPrefix):
		return strings.TrimPrefix(key, legacyHeadPrefix), true
	default:
		return "", false
	}
}
