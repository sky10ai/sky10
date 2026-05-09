package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/logging"
	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
	messagingstore "github.com/sky10/sky10/pkg/messaging/store"
)

func TestRestoreMessagingConnectionsSkipsDisabled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newServeMessagingMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	connection := messaging.Connection{
		ID:        "imap/disabled",
		AdapterID: "imap-smtp",
		Label:     "Disabled Mail",
		Status:    messaging.ConnectionStatusDisabled,
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}
	b, err := messagingbroker.New(ctx, messagingbroker.Config{
		Store:   store,
		RootDir: filepath.Join(t.TempDir(), "messaging"),
	})
	if err != nil {
		t.Fatalf("broker.New() error = %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	rt, err := logging.New(logging.Config{FilePath: filepath.Join(t.TempDir(), "sky10.log")})
	if err != nil {
		t.Fatalf("logging.New() error = %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resolverCalled := false
	err = restoreMessagingConnections(ctx, b, store, func(adapterID string) (messagingruntime.ProcessSpec, error) {
		resolverCalled = true
		return messagingruntime.ProcessSpec{}, fmt.Errorf("resolver should not be called for %s", adapterID)
	}, rt.Logger)
	if err != nil {
		t.Fatalf("restoreMessagingConnections() error = %v", err)
	}
	if resolverCalled {
		t.Fatal("process resolver was called for disabled connection")
	}
	got, ok := store.GetConnection(connection.ID)
	if !ok || got.Status != messaging.ConnectionStatusDisabled {
		t.Fatalf("GetConnection() = %+v, %v; want disabled connection retained", got, ok)
	}
}

func TestEnsureDefaultMessagingRuntimeAccessBackfillsPolicyAndRuntimeExposures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newServeMessagingMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	connection := messaging.Connection{
		ID:        "telegram/default",
		AdapterID: "telegram",
		Label:     "Telegram",
		Status:    messaging.ConnectionStatusConnected,
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}

	if err := ensureDefaultMessagingRuntimeAccess(ctx, store, nil); err != nil {
		t.Fatalf("ensureDefaultMessagingRuntimeAccess() error = %v", err)
	}

	stored, ok := store.GetConnection(connection.ID)
	if !ok {
		t.Fatalf("GetConnection(%s) = false", connection.ID)
	}
	wantPolicyID := defaultMessagingRuntimePolicyID(connection.ID)
	if stored.DefaultPolicyID != wantPolicyID {
		t.Fatalf("DefaultPolicyID = %q, want %q", stored.DefaultPolicyID, wantPolicyID)
	}
	policy, ok := store.GetPolicy(wantPolicyID)
	if !ok {
		t.Fatalf("GetPolicy(%s) = false", wantPolicyID)
	}
	if !policy.Rules.ReadInbound || !policy.Rules.CreateDrafts || !policy.Rules.SendMessages || !policy.Rules.RequireApproval {
		t.Fatalf("policy rules = %+v, want read/draft/send-with-approval defaults", policy.Rules)
	}
	if !policy.Rules.ReplyOnly || policy.Rules.AllowNewConversations || policy.Rules.AllowAttachments {
		t.Fatalf("policy rules = %+v, want reply-only sends with new conversations/attachments disabled", policy.Rules)
	}
	for _, subjectID := range defaultMessagingRuntimeSubjects {
		exposureID := defaultMessagingRuntimeExposureID(connection.ID, subjectID)
		exposure, ok := store.GetExposure(exposureID)
		if !ok {
			t.Fatalf("GetExposure(%s) = false", exposureID)
		}
		if exposure.ConnectionID != connection.ID || exposure.SubjectID != subjectID || exposure.SubjectKind != messaging.ExposureSubjectKindRuntime || exposure.PolicyID != wantPolicyID || !exposure.Enabled {
			t.Fatalf("exposure %s = %+v, want enabled runtime exposure for %s", exposureID, exposure, subjectID)
		}
	}
}

func TestEnsureDefaultMessagingRuntimeAccessSkipsDisabledConnections(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newServeMessagingMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	connection := messaging.Connection{
		ID:        "telegram/disabled",
		AdapterID: "telegram",
		Label:     "Disabled Telegram",
		Status:    messaging.ConnectionStatusDisabled,
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}

	if err := ensureDefaultMessagingRuntimeAccess(ctx, store, nil); err != nil {
		t.Fatalf("ensureDefaultMessagingRuntimeAccess() error = %v", err)
	}

	stored, ok := store.GetConnection(connection.ID)
	if !ok {
		t.Fatalf("GetConnection(%s) = false", connection.ID)
	}
	if stored.DefaultPolicyID != "" {
		t.Fatalf("DefaultPolicyID = %q, want empty", stored.DefaultPolicyID)
	}
	for _, subjectID := range defaultMessagingRuntimeSubjects {
		exposureID := defaultMessagingRuntimeExposureID(connection.ID, subjectID)
		if exposure, ok := store.GetExposure(exposureID); ok {
			t.Fatalf("GetExposure(%s) = %+v, want none", exposureID, exposure)
		}
	}
}

func TestEnsureDefaultMessagingRuntimeAccessRefreshesManagedPolicyDefaults(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newServeMessagingMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	connection := messaging.Connection{
		ID:              "telegram/default",
		AdapterID:       "telegram",
		Label:           "Telegram",
		Status:          messaging.ConnectionStatusConnected,
		DefaultPolicyID: defaultMessagingRuntimePolicyID("telegram/default"),
	}
	if err := store.PutConnection(ctx, connection); err != nil {
		t.Fatalf("PutConnection() error = %v", err)
	}
	if err := store.PutPolicy(ctx, messaging.Policy{
		ID:   connection.DefaultPolicyID,
		Name: "Default runtime messaging access",
		Rules: messaging.PolicyRules{
			ReadInbound:     true,
			CreateDrafts:    true,
			SendMessages:    false,
			RequireApproval: true,
			ReplyOnly:       true,
		},
		Metadata: map[string]string{
			"source": defaultMessagingPolicySource,
		},
	}); err != nil {
		t.Fatalf("PutPolicy() error = %v", err)
	}

	if err := ensureDefaultMessagingRuntimeAccess(ctx, store, nil); err != nil {
		t.Fatalf("ensureDefaultMessagingRuntimeAccess() error = %v", err)
	}

	policy, ok := store.GetPolicy(connection.DefaultPolicyID)
	if !ok {
		t.Fatalf("GetPolicy(%s) = false", connection.DefaultPolicyID)
	}
	if !policy.Rules.SendMessages || !policy.Rules.RequireApproval || !policy.Rules.ReplyOnly {
		t.Fatalf("policy rules = %+v, want refreshed approval-gated reply sends", policy.Rules)
	}
}

func TestInstallMessagingEventFanoutEmitsDurableEvents(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := messagingstore.NewStore(ctx, messagingstore.NewKVBackend(newServeMessagingMemoryKVStore(), ""))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	var gotName string
	var gotData interface{}
	installMessagingEventFanout(store, func(name string, data interface{}) {
		gotName = name
		gotData = data
	})

	event := messaging.Event{
		ID:           "evt/work",
		Type:         messaging.EventTypeMessageReceived,
		ConnectionID: "slack/work",
		MessageID:    "msg/work",
		Timestamp:    testingNow(),
	}
	if err := store.AppendEvent(ctx, event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	if gotName != messaging.FanoutEventName {
		t.Fatalf("event name = %q, want %q", gotName, messaging.FanoutEventName)
	}
	gotEvent, ok := gotData.(messaging.Event)
	if !ok || gotEvent.ID != event.ID {
		t.Fatalf("event data = %+v (%T), want messaging event %s", gotData, gotData, event.ID)
	}
}

func testingNow() time.Time {
	return time.Date(2026, 4, 25, 9, 30, 0, 0, time.UTC)
}

type serveMessagingMemoryKVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newServeMessagingMemoryKVStore() *serveMessagingMemoryKVStore {
	return &serveMessagingMemoryKVStore{data: make(map[string][]byte)}
}

func (s *serveMessagingMemoryKVStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), value...)
	return nil
}

func (s *serveMessagingMemoryKVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return append([]byte(nil), value...), ok
}

func (s *serveMessagingMemoryKVStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *serveMessagingMemoryKVStore) List(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0)
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys
}
