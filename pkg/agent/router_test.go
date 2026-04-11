package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skyid "github.com/sky10/sky10/pkg/id"
	skykey "github.com/sky10/sky10/pkg/key"
	"github.com/sky10/sky10/pkg/link"
)

func makeTestNode(t *testing.T) *link.Node {
	t.Helper()
	k, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	n, err := link.NewFromKey(k, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func makeSharedTestNodes(t *testing.T) (*link.Node, *link.Node) {
	t.Helper()

	identity, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceA, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	deviceB, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	manifest := skyid.NewManifest(identity)
	manifest.AddDevice(deviceA.PublicKey, "node-a")
	manifest.AddDevice(deviceB.PublicKey, "node-b")
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		t.Fatal(err)
	}

	bundleA, err := skyid.New(identity, deviceA, manifest)
	if err != nil {
		t.Fatal(err)
	}
	bundleB, err := skyid.New(identity, deviceB, manifest)
	if err != nil {
		t.Fatal(err)
	}

	nodeA, err := link.New(bundleA, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := link.New(bundleB, link.Config{Mode: link.Private}, nil)
	if err != nil {
		t.Fatal(err)
	}

	return nodeA, nodeB
}

func startNode(t *testing.T, n *link.Node) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- n.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for n.Host() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Host() == nil {
		cancel()
		t.Fatal("node did not start")
	}
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
}

func connectNodes(t *testing.T, a, b *link.Node) {
	t.Helper()
	info := b.Host().Peerstore().PeerInfo(b.PeerID())
	info.Addrs = b.Host().Addrs()
	if err := a.Host().Connect(context.Background(), info); err != nil {
		t.Fatalf("connecting nodes: %v", err)
	}
}

func TestRouterSendLocal(t *testing.T) {
	t.Parallel()

	reg := NewRegistry("D-device01", "host1", nil)
	reg.Register(RegisterParams{Name: "coder"}, "A-agent00100000000")

	var emitted []Message
	emit := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			emitted = append(emitted, msg)
		}
	}

	node := makeTestNode(t)
	router := NewRouter(reg, node, emit, "D-device01", nil)

	msg := Message{
		ID:        "msg-1",
		SessionID: "session-1",
		To:        "coder",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"hello"}`),
	}
	result, err := router.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("local send: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "sent" {
		t.Errorf("status = %s, want sent", m["status"])
	}
	if len(emitted) != 1 {
		t.Fatalf("emitted %d messages, want 1", len(emitted))
	}
	if emitted[0].To != "coder" {
		t.Errorf("emitted to = %s, want coder", emitted[0].To)
	}
}

func TestRouterSendRemote(t *testing.T) {
	t.Parallel()

	// Node A: sender, no local agents.
	nodeA, nodeB := makeSharedTestNodes(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	// Node B: receiver, has agent.
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher"}, "A-remote0100000000")

	var receivedOnB []Message
	emitB := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			receivedOnB = append(receivedOnB, msg)
		}
	}
	RegisterLinkHandlers(nodeB, regB, emitB, nil)

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)
	routerA.cachePeer("D-deviceBB", nodeB.PeerID())

	msg := Message{
		ID:        "msg-remote",
		SessionID: "session-1",
		To:        "researcher",
		DeviceID:  "D-deviceBB",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"search this"}`),
	}
	result, err := routerA.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("remote send: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "sent" {
		t.Errorf("status = %s, want sent", m["status"])
	}
	if len(receivedOnB) != 1 {
		t.Fatalf("node B received %d messages, want 1", len(receivedOnB))
	}
	if receivedOnB[0].To != "researcher" {
		t.Errorf("received to = %s, want researcher", receivedOnB[0].To)
	}
}

func TestRouterListAggregation(t *testing.T) {
	t.Parallel()

	nodeA, nodeB := makeSharedTestNodes(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regA.Register(RegisterParams{Name: "coder", Skills: []string{"code"}}, "A-localA0100000000")

	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher", Skills: []string{"research"}}, "A-remoteB100000000")

	RegisterLinkHandlers(nodeB, regB, nil, nil)

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)

	agents := routerA.List(context.Background())
	if len(agents) != 2 {
		t.Fatalf("List() returned %d agents, want 2", len(agents))
	}

	names := map[string]bool{}
	for _, a := range agents {
		names[a.Name] = true
	}
	if !names["coder"] || !names["researcher"] {
		t.Fatalf("expected coder and researcher, got %v", names)
	}
}

func TestRouterDiscover(t *testing.T) {
	t.Parallel()

	nodeA, nodeB := makeSharedTestNodes(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regA.Register(RegisterParams{Name: "coder", Skills: []string{"code"}}, "A-localA0100000000")

	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher", Skills: []string{"research"}}, "A-remoteB100000000")

	RegisterLinkHandlers(nodeB, regB, nil, nil)
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)

	found := routerA.Discover(context.Background(), "code")
	if len(found) != 1 || found[0].Name != "coder" {
		t.Fatalf("Discover(code) = %v, want [coder]", found)
	}

	found = routerA.Discover(context.Background(), "research")
	if len(found) != 1 || found[0].Name != "researcher" {
		t.Fatalf("Discover(research) = %v, want [researcher]", found)
	}

	found = routerA.Discover(context.Background(), "missing")
	if len(found) != 0 {
		t.Fatalf("Discover(missing) = %v, want []", found)
	}
}

func TestRouterSendUnknownDevice(t *testing.T) {
	t.Parallel()

	node := makeTestNode(t)
	reg := NewRegistry("D-device01", "host1", nil)
	router := NewRouter(reg, node, nil, "D-device01", nil)

	msg := Message{
		ID:       "msg-1",
		To:       "missing",
		DeviceID: "D-unknown0",
	}
	_, err := router.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestRouterListPopulatesPeerCache(t *testing.T) {
	t.Parallel()

	nodeA, nodeB := makeSharedTestNodes(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)

	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher"}, "A-remoteB100000000")

	RegisterLinkHandlers(nodeB, regB, nil, nil)
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)

	_, ok := routerA.lookupPeer("D-deviceBB")
	if ok {
		t.Fatal("peer cache should be empty before List")
	}

	routerA.List(context.Background())

	pid, ok := routerA.lookupPeer("D-deviceBB")
	if !ok {
		t.Fatal("peer cache not populated after List")
	}
	if pid != nodeB.PeerID() {
		t.Fatalf("cached peer = %s, want %s", pid, nodeB.PeerID())
	}
}

func TestRouterListIgnoresPublicPeers(t *testing.T) {
	t.Parallel()

	nodeA, nodeB := makeSharedTestNodes(t)
	nodePublic := makeTestNode(t)

	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regA.Register(RegisterParams{Name: "coder", Skills: []string{"code"}}, "A-localA0100000000")

	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher", Skills: []string{"research"}}, "A-remoteB100000000")
	RegisterLinkHandlers(nodeB, regB, nil, nil)

	regPublic := NewRegistry("D-deviceCC", "hostC", nil)
	regPublic.Register(RegisterParams{Name: "outsider", Skills: []string{"noise"}}, "A-publicC100000000")
	RegisterLinkHandlers(nodePublic, regPublic, nil, nil)

	startNode(t, nodeA)
	startNode(t, nodeB)
	startNode(t, nodePublic)

	connectNodes(t, nodeA, nodeB)
	connectNodes(t, nodeA, nodePublic)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)
	agents := routerA.List(context.Background())

	names := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		names[agent.Name] = struct{}{}
	}
	if _, ok := names["coder"]; !ok {
		t.Fatalf("expected local agent in list, got %v", names)
	}
	if _, ok := names["researcher"]; !ok {
		t.Fatalf("expected private-network remote agent in list, got %v", names)
	}
	if _, ok := names["outsider"]; ok {
		t.Fatalf("public peer agent leaked into list: %v", names)
	}
}

func TestRouterDeliverMailboxRecordSky10Network(t *testing.T) {
	t.Parallel()

	nodeA := makeTestNode(t)
	nodeB := makeTestNode(t)
	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "worker"}, "A-worker010000000")

	mailboxA := newRouterMailboxStore(t)
	mailboxB := newRouterMailboxStore(t)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)
	routerA.SetMailbox(mailboxA)
	routerA.cacheAddress(nodeB.Address(), nodeB.PeerID())

	routerB := NewRouter(regB, nodeB, nil, "D-deviceBB", nil)
	routerB.SetMailbox(mailboxB)
	RegisterLinkHandlers(nodeB, regB, nil, routerB)

	record, err := mailboxA.Create(context.Background(), agentmailbox.Item{
		ID:             "network-item-1",
		Kind:           agentmailbox.ItemKindMessage,
		From:           agentmailbox.Principal{ID: nodeA.Address(), Kind: agentmailbox.PrincipalKindHuman, Scope: agentmailbox.ScopeSky10Network, RouteHint: nodeA.Address()},
		To:             &agentmailbox.Principal{ID: "worker", Kind: agentmailbox.PrincipalKindNetworkAgent, Scope: agentmailbox.ScopeSky10Network, RouteHint: nodeB.Address()},
		SessionID:      "session-1",
		RequestID:      "request-1",
		IdempotencyKey: "request-1",
		PayloadInline:  json.RawMessage(`{"text":"hello from sky10"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	delivered, err := routerA.DeliverMailboxRecord(context.Background(), record)
	if err != nil {
		t.Fatalf("deliver mailbox record: %v", err)
	}
	if delivered.State != agentmailbox.StateDelivered {
		t.Fatalf("state = %s, want %s", delivered.State, agentmailbox.StateDelivered)
	}

	got, ok := mailboxB.Get(record.Item.ID)
	if !ok {
		t.Fatal("remote mailbox item not found")
	}
	if got.Item.To == nil || got.Item.To.ID != "worker" {
		t.Fatalf("remote recipient = %+v, want worker", got.Item.To)
	}
	if got.Item.Scope() != agentmailbox.ScopeSky10Network {
		t.Fatalf("remote item scope = %s, want %s", got.Item.Scope(), agentmailbox.ScopeSky10Network)
	}
}

func TestRouterDeliverMailboxRecordSky10NetworkQueuesOnUnresolvedAddress(t *testing.T) {
	t.Parallel()

	node := makeTestNode(t)
	reg := NewRegistry("D-deviceAA", "hostA", nil)
	mailbox := newRouterMailboxStore(t)
	router := NewRouter(reg, node, nil, "D-deviceAA", nil)
	router.SetMailbox(mailbox)

	record, err := mailbox.Create(context.Background(), agentmailbox.Item{
		ID:             "network-item-2",
		Kind:           agentmailbox.ItemKindMessage,
		From:           agentmailbox.Principal{ID: "sky10qsender", Kind: agentmailbox.PrincipalKindHuman, Scope: agentmailbox.ScopeSky10Network, RouteHint: "sky10qsender"},
		To:             &agentmailbox.Principal{ID: "worker", Kind: agentmailbox.PrincipalKindNetworkAgent, Scope: agentmailbox.ScopeSky10Network, RouteHint: "sky10qunreachable"},
		SessionID:      "session-2",
		RequestID:      "request-2",
		IdempotencyKey: "request-2",
		PayloadInline:  json.RawMessage(`{"text":"retry later"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := router.DeliverMailboxRecord(context.Background(), record)
	if err == nil {
		t.Fatal("expected unresolved sky10 address to fail live delivery")
	}
	if !updated.Failed() {
		t.Fatalf("updated state = %s, want failure state", updated.State)
	}
	if _, ok := updated.LatestEvent(); !ok {
		t.Fatal("expected delivery failure event")
	}
}

func TestRouterSky10NetworkRelayStoreAndForward(t *testing.T) {
	t.Parallel()

	senderKey, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	recipientKey, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	relayTransport := newTestRelayTransport()
	senderRelay := agentmailbox.NewRelayDropbox(senderKey, relayTransport, nil)
	recipientRelay := agentmailbox.NewRelayDropbox(recipientKey, relayTransport, nil)

	senderStore, err := agentmailbox.NewStore(context.Background(), agentmailbox.NewScopedKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatal(err)
	}
	recipientStore, err := agentmailbox.NewStore(context.Background(), agentmailbox.NewScopedKVBackend(newMemoryKVStore(), ""))
	if err != nil {
		t.Fatal(err)
	}

	senderRouter := NewRouter(NewRegistry("D-sender", "sender", nil), nil, nil, "D-sender", nil)
	senderRouter.SetMailbox(senderStore)
	senderRouter.SetNetworkRelay(senderRelay)

	recipientRegistry := NewRegistry("D-recipient", "recipient", nil)
	recipientRegistry.Register(RegisterParams{Name: "worker"}, "A-worker010000000")
	recipientRouter := NewRouter(recipientRegistry, nil, nil, "D-recipient", nil)
	recipientRouter.SetMailbox(recipientStore)
	recipientRouter.SetNetworkRelay(recipientRelay)

	record, err := senderStore.Create(context.Background(), agentmailbox.Item{
		ID:             "relay-item-1",
		Kind:           agentmailbox.ItemKindMessage,
		From:           agentmailbox.Principal{ID: senderKey.Address(), Kind: agentmailbox.PrincipalKindHuman, Scope: agentmailbox.ScopeSky10Network, RouteHint: senderKey.Address()},
		To:             &agentmailbox.Principal{ID: "worker", Kind: agentmailbox.PrincipalKindNetworkAgent, Scope: agentmailbox.ScopeSky10Network, RouteHint: recipientKey.Address()},
		SessionID:      "session-1",
		RequestID:      "request-1",
		IdempotencyKey: "request-1",
		PayloadInline:  json.RawMessage(`{"text":"relay this"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	queued, err := senderRouter.DeliverMailboxRecord(context.Background(), record)
	if err != nil {
		t.Fatalf("deliver via relay fallback: %v", err)
	}
	if queued.State != agentmailbox.StateQueued {
		t.Fatalf("sender state = %s, want queued after handoff", queued.State)
	}
	if !hasEventType(queued, agentmailbox.EventTypeHandedOff) {
		t.Fatal("expected handed_off event after relay handoff")
	}

	if err := recipientRouter.PollNetworkRelay(context.Background()); err != nil {
		t.Fatalf("recipient poll: %v", err)
	}
	received, ok := recipientStore.Get(record.Item.ID)
	if !ok {
		t.Fatal("recipient mailbox did not ingest relay item")
	}
	if received.Item.To == nil || received.Item.To.ID != "worker" {
		t.Fatalf("recipient to = %+v, want worker", received.Item.To)
	}

	if err := senderRouter.PollNetworkRelay(context.Background()); err != nil {
		t.Fatalf("sender poll receipts: %v", err)
	}
	delivered, ok := senderStore.Get(record.Item.ID)
	if !ok {
		t.Fatal("sender mailbox record missing after receipt poll")
	}
	if delivered.State != agentmailbox.StateDelivered {
		t.Fatalf("sender state = %s, want delivered", delivered.State)
	}
	if !hasEventType(delivered, agentmailbox.EventTypeDelivered) {
		t.Fatal("expected delivered receipt event on sender")
	}
}

func newRouterMailboxStore(t *testing.T) *agentmailbox.Store {
	t.Helper()
	store, err := agentmailbox.NewStore(context.Background(), agentmailbox.NewScopedKVBackend(newRouterMemoryKVStore(), ""))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type testRelayTransport struct {
	mu     sync.RWMutex
	events map[string]testRelayEvent
}

type testRelayEvent struct {
	recordType string
	recipient  string
	itemID     string
	payload    []byte
	createdAt  time.Time
}

func newTestRelayTransport() *testRelayTransport {
	return &testRelayTransport{events: make(map[string]testRelayEvent)}
}

func (t *testRelayTransport) Publish(_ context.Context, _ *skykey.Key, event agentmailbox.RelayTransportEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events[event.DTag] = testRelayEvent{
		recordType: event.RecordType,
		recipient:  event.Recipient,
		itemID:     event.ItemID,
		payload:    append([]byte(nil), event.Payload...),
		createdAt:  event.CreatedAt,
	}
	return nil
}

func (t *testRelayTransport) Query(_ context.Context, recipient, recordType string, _ int) ([]agentmailbox.RelayTransportEvent, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]agentmailbox.RelayTransportEvent, 0)
	for dtag, event := range t.events {
		if event.recipient != recipient || event.recordType != recordType {
			continue
		}
		out = append(out, agentmailbox.RelayTransportEvent{
			ID:         dtag,
			DTag:       dtag,
			RecordType: event.recordType,
			Recipient:  event.recipient,
			ItemID:     event.itemID,
			CreatedAt:  event.createdAt,
			Payload:    append([]byte(nil), event.payload...),
		})
	}
	return out, nil
}

type routerMemoryKVStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func newRouterMemoryKVStore() *routerMemoryKVStore {
	return &routerMemoryKVStore{data: make(map[string][]byte)}
}

func (s *routerMemoryKVStore) Set(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), value...)
	return nil
}

func (s *routerMemoryKVStore) Get(key string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.data[key]
	return append([]byte(nil), value...), ok
}

func (s *routerMemoryKVStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *routerMemoryKVStore) List(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]string, 0)
	for key := range s.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			out = append(out, key)
		}
	}
	return out
}

func hasEventType(record agentmailbox.Record, eventType string) bool {
	for _, event := range record.Events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func TestRouterSendQueuesWhenRemoteDeviceUnavailable(t *testing.T) {
	t.Parallel()

	node := makeTestNode(t)
	reg := NewRegistry("D-deviceAA", "hostA", nil)
	router := NewRouter(reg, node, nil, "D-deviceAA", nil)
	mailboxStore := newTestMailboxStore(t)
	router.SetMailbox(mailboxStore)

	msg := Message{
		ID:        "msg-queued",
		SessionID: "session-1",
		From:      "D-deviceAA",
		To:        "researcher",
		DeviceID:  "D-deviceBB",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"search this"}`),
		Timestamp: time.Now().UTC(),
	}
	result, err := router.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "queued" {
		t.Fatalf("status = %s, want queued", m["status"])
	}

	outbox := mailboxStore.ListOutbox("D-deviceAA")
	if len(outbox) != 1 {
		t.Fatalf("outbox len = %d, want 1", len(outbox))
	}
	if outbox[0].State != agentmailbox.StateFailed {
		t.Fatalf("state = %s, want %s", outbox[0].State, agentmailbox.StateFailed)
	}
}

func TestRouterDrainOutboxDeliversQueuedRemoteMessage(t *testing.T) {
	t.Parallel()

	nodeA, nodeB := makeSharedTestNodes(t)
	regA := NewRegistry("D-deviceAA", "hostA", nil)
	regB := NewRegistry("D-deviceBB", "hostB", nil)
	regB.Register(RegisterParams{Name: "researcher"}, "A-remote0100000000")

	var receivedOnB []Message
	emitB := func(event string, data interface{}) {
		if msg, ok := data.(Message); ok {
			receivedOnB = append(receivedOnB, msg)
		}
	}
	RegisterLinkHandlers(nodeB, regB, emitB, nil)

	startNode(t, nodeA)
	startNode(t, nodeB)
	connectNodes(t, nodeA, nodeB)

	routerA := NewRouter(regA, nodeA, nil, "D-deviceAA", nil)
	mailboxStore := newTestMailboxStore(t)
	routerA.SetMailbox(mailboxStore)

	msg := Message{
		ID:        "msg-drain",
		SessionID: "session-1",
		From:      "D-deviceAA",
		To:        "researcher",
		DeviceID:  "D-deviceBB",
		Type:      "text",
		Content:   json.RawMessage(`{"text":"search this"}`),
		Timestamp: time.Now().UTC(),
	}
	result, err := routerA.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("initial send: %v", err)
	}
	if result.(map[string]string)["status"] != "queued" {
		t.Fatalf("initial status = %s, want queued", result.(map[string]string)["status"])
	}

	routerA.cachePeer("D-deviceBB", nodeB.PeerID())
	if err := routerA.DrainOutbox(context.Background(), "D-deviceBB"); err != nil {
		t.Fatalf("drain outbox: %v", err)
	}
	if len(receivedOnB) != 1 {
		t.Fatalf("node B received %d messages, want 1", len(receivedOnB))
	}

	outbox := mailboxStore.ListOutbox("D-deviceAA")
	if len(outbox) != 1 {
		t.Fatalf("outbox len = %d, want 1", len(outbox))
	}
	if outbox[0].State != agentmailbox.StateDelivered {
		t.Fatalf("state = %s, want %s", outbox[0].State, agentmailbox.StateDelivered)
	}
}
