package x402

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrServiceUnknown indicates a Call referenced a service the registry
// has never seen. Different from ErrServiceNotApproved (which is the
// "agent has not opted in" failure mode).
var ErrServiceUnknown = errors.New("x402: service unknown")

// ErrServiceNotApproved indicates a Call referenced a service the
// agent has not approved. Failure mode for callers; not an internal
// error.
var ErrServiceNotApproved = errors.New("x402: service not approved for this agent")

// Registry is the in-memory service catalog plus per-agent approval
// state and pins. It is the source of truth the Backend consults on
// every Call. Read-mostly; updates serialize through a single mutex
// because the catalog is small (low hundreds) and updates are rare
// (catalog refresh, user approval).
//
// Persistence is delegated to a RegistryStore. NewRegistry accepts a
// nil store for tests; production wires a JSON-backed store under
// os.UserConfigDir().
type Registry struct {
	mu          sync.RWMutex
	manifests   map[string]ServiceManifest
	policy      map[string]PolicyEntry
	approvals   map[approvalKey]Approval
	pins        map[approvalKey]Pin
	userEnabled map[string]UserEnableRecord
	store       RegistryStore
	clock       func() time.Time
}

// UserEnableRecord captures the user-level "this service is enabled
// for any of my agents" decision made from settings UI/CLI. Distinct
// from per-(agent, service) Approval, which is the fine-grained
// override path. When Backend.Call lacks a per-agent approval it
// falls back to this user-level record.
type UserEnableRecord struct {
	ServiceID    string    `json:"service_id"`
	EnabledAt    time.Time `json:"enabled_at"`
	MaxPriceUSDC string    `json:"max_price_usdc"`
	Pin          Pin       `json:"pin"`
}

type approvalKey struct {
	agentID   string
	serviceID string
}

// NewRegistry constructs a Registry. If store is non-nil, the
// registry loads its initial state from store and persists changes.
// `now` may be nil to use time.Now.
func NewRegistry(store RegistryStore, now func() time.Time) (*Registry, error) {
	if now == nil {
		now = time.Now
	}
	r := &Registry{
		manifests:   make(map[string]ServiceManifest),
		policy:      make(map[string]PolicyEntry),
		approvals:   make(map[approvalKey]Approval),
		pins:        make(map[approvalKey]Pin),
		userEnabled: make(map[string]UserEnableRecord),
		store:       store,
		clock:       now,
	}
	if store != nil {
		snapshot, err := store.Load()
		if err != nil {
			return nil, fmt.Errorf("load registry: %w", err)
		}
		for _, m := range snapshot.Manifests {
			r.manifests[m.ID] = m
		}
		for _, p := range snapshot.Policy {
			r.policy[p.ServiceID] = p
		}
		for _, a := range snapshot.Approvals {
			r.approvals[approvalKey{a.AgentID, a.ServiceID}] = a
		}
		for _, p := range snapshot.Pins {
			r.pins[approvalKey{agentApprovalAgentForPin(p, snapshot), p.ServiceID}] = p
		}
		for _, u := range snapshot.UserEnabled {
			r.userEnabled[u.ServiceID] = u
		}
	}
	return r, nil
}

// agentApprovalAgentForPin recovers the agent_id for a pin from the
// snapshot's approvals. Pins are scoped per agent in this design.
func agentApprovalAgentForPin(p Pin, snap RegistrySnapshot) string {
	for _, a := range snap.Approvals {
		if a.ServiceID == p.ServiceID {
			return a.AgentID
		}
	}
	return ""
}

// AddManifest adds or updates a service manifest in the catalog.
// Existing approvals/pins are not invalidated; the diff classifier
// (in discovery, future milestone) decides whether the change is
// safe (auto-applied) or risky (queued for re-approval).
func (r *Registry) AddManifest(m ServiceManifest) error {
	if m.ID == "" {
		return errors.New("manifest missing id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifests[m.ID] = m
	return r.persistLocked()
}

// SetPolicy replaces the overlay policy entry for a service. Used by
// the catalog overlay loader at startup.
func (r *Registry) SetPolicy(p PolicyEntry) error {
	if p.ServiceID == "" {
		return errors.New("policy entry missing service_id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy[p.ServiceID] = p
	return r.persistLocked()
}

// Policy returns the overlay policy entry for a service, if one
// exists. The bool is false when the registry has no opinion on
// this service.
func (r *Registry) Policy(serviceID string) (PolicyEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.policy[serviceID]
	return p, ok
}

// Manifest returns a copy of the manifest for a service.
// ErrServiceUnknown if not in the catalog.
func (r *Registry) Manifest(serviceID string) (ServiceManifest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.manifests[serviceID]
	if !ok {
		return ServiceManifest{}, ErrServiceUnknown
	}
	return m, nil
}

// AllManifests returns a snapshot of every manifest in the catalog,
// sorted by service id. Used by the discovery package to diff a
// fresh observation against the registry's current view.
func (r *Registry) AllManifests() []ServiceManifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ServiceManifest, 0, len(r.manifests))
	for _, m := range r.manifests {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Approve creates an approval and matching Pin from the current
// manifest. ErrServiceUnknown if the service is not in the catalog.
func (r *Registry) Approve(agentID, serviceID, maxPriceUSDC string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.manifests[serviceID]
	if !ok {
		return ErrServiceUnknown
	}
	pin, err := PinFromManifest(m)
	if err != nil {
		return err
	}
	tier := TierConvenience
	if p, ok := r.policy[serviceID]; ok {
		tier = p.Tier
	}
	key := approvalKey{agentID, serviceID}
	r.approvals[key] = Approval{
		AgentID:      agentID,
		ServiceID:    serviceID,
		ApprovedAt:   r.clock().UTC(),
		MaxPriceUSDC: maxPriceUSDC,
		Tier:         tier,
	}
	r.pins[key] = pin
	return r.persistLocked()
}

// Revoke removes an approval and its pin.
func (r *Registry) Revoke(agentID, serviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := approvalKey{agentID, serviceID}
	delete(r.approvals, key)
	delete(r.pins, key)
	return r.persistLocked()
}

// SetUserEnabled marks a service enabled for the user. Pins the
// current manifest so subsequent calls fail closed if the upstream
// changes risky fields. ErrServiceUnknown if the manifest is not in
// the catalog.
func (r *Registry) SetUserEnabled(serviceID, maxPriceUSDC string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.manifests[serviceID]
	if !ok {
		return ErrServiceUnknown
	}
	pin, err := PinFromManifest(m)
	if err != nil {
		return err
	}
	if maxPriceUSDC == "" {
		maxPriceUSDC = m.MaxPriceUSDC
	}
	r.userEnabled[serviceID] = UserEnableRecord{
		ServiceID:    serviceID,
		EnabledAt:    r.clock().UTC(),
		MaxPriceUSDC: maxPriceUSDC,
		Pin:          pin,
	}
	return r.persistLocked()
}

// SetUserDisabled removes the user-level enable record for a
// service. Per-agent approvals (if any) are not affected; revoking
// those is the agent-scoped operation.
func (r *Registry) SetUserDisabled(serviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.userEnabled, serviceID)
	return r.persistLocked()
}

// UserEnabled returns the user-level enable record for a service.
// The bool is false when no user-level enable exists; callers
// usually only check the bool and ignore the record on miss.
func (r *Registry) UserEnabled(serviceID string) (UserEnableRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.userEnabled[serviceID]
	return rec, ok
}

// Approval returns the per-(agent, service) approval record.
// ErrServiceNotApproved when none exists.
func (r *Registry) Approval(agentID, serviceID string) (Approval, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.approvals[approvalKey{agentID, serviceID}]
	if !ok {
		return Approval{}, ErrServiceNotApproved
	}
	return a, nil
}

// Pin returns the pin for a (agent, service). ErrServiceNotApproved
// when none exists; pins are only created on Approve.
func (r *Registry) Pin(agentID, serviceID string) (Pin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pins[approvalKey{agentID, serviceID}]
	if !ok {
		return Pin{}, ErrServiceNotApproved
	}
	return p, nil
}

// ListApprovedListing is the per-call shape returned to the agent
// (via the Backend) when it asks for its approved services. Mirrors
// pkg/sandbox/comms/x402.ServiceListing without importing the comms
// package, so the dependency direction stays one-way.
type ListApprovedListing struct {
	ID          string
	DisplayName string
	Description string
	Category    string
	Endpoint    string
	ServiceURL  string
	Endpoints   []ServiceEndpoint
	Networks    []Network
	Tier        Tier
	PriceUSDC   string
	Hint        string
}

// ListApproved returns the services this agent can call: those with
// an explicit per-(agent, service) approval AND those the user has
// enabled at the user level. Joined with policy overlay (tier, hint)
// and the manifest's display metadata. Sorted by service id for
// stable iteration.
func (r *Registry) ListApproved(agentID string) []ListApprovedListing {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{})
	var out []ListApprovedListing
	for key, ap := range r.approvals {
		if key.agentID != agentID {
			continue
		}
		m, ok := r.manifests[key.serviceID]
		if !ok {
			continue
		}
		seen[key.serviceID] = struct{}{}
		out = append(out, r.listingLocked(m, ap.Tier))
	}
	for serviceID := range r.userEnabled {
		if _, dupe := seen[serviceID]; dupe {
			continue
		}
		m, ok := r.manifests[serviceID]
		if !ok {
			continue
		}
		out = append(out, r.listingLocked(m, TierConvenience))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// listingLocked is a helper that builds a ListApprovedListing from a
// manifest, applying policy overlay metadata. Callers must hold r.mu
// (read or write).
func (r *Registry) listingLocked(m ServiceManifest, defaultTier Tier) ListApprovedListing {
	entry := ListApprovedListing{
		ID:          m.ID,
		DisplayName: m.DisplayName,
		Description: m.Description,
		Category:    m.Category,
		Endpoint:    m.Endpoint,
		ServiceURL:  m.ServiceURL,
		Endpoints:   listingEndpoints(m),
		Networks:    append([]Network(nil), m.Networks...),
		Tier:        defaultTier,
		PriceUSDC:   m.MaxPriceUSDC,
	}
	if p, ok := r.policy[m.ID]; ok {
		entry.Tier = p.Tier
		entry.Hint = p.Hint
	}
	return entry
}

func listingEndpoints(m ServiceManifest) []ServiceEndpoint {
	if len(m.Endpoints) > 0 {
		return append([]ServiceEndpoint(nil), m.Endpoints...)
	}
	if strings.TrimSpace(m.Endpoint) == "" {
		return nil
	}
	fallback := ServiceEndpoint{
		URL:       m.Endpoint,
		PriceUSDC: m.MaxPriceUSDC,
	}
	if len(m.Networks) > 0 {
		fallback.Network = m.Networks[0]
	}
	return []ServiceEndpoint{fallback}
}

func (r *Registry) persistLocked() error {
	if r.store == nil {
		return nil
	}
	snap := RegistrySnapshot{}
	for _, m := range r.manifests {
		snap.Manifests = append(snap.Manifests, m)
	}
	for _, p := range r.policy {
		snap.Policy = append(snap.Policy, p)
	}
	for _, a := range r.approvals {
		snap.Approvals = append(snap.Approvals, a)
	}
	for _, p := range r.pins {
		snap.Pins = append(snap.Pins, p)
	}
	for _, u := range r.userEnabled {
		snap.UserEnabled = append(snap.UserEnabled, u)
	}
	sort.Slice(snap.Manifests, func(i, j int) bool { return snap.Manifests[i].ID < snap.Manifests[j].ID })
	sort.Slice(snap.Policy, func(i, j int) bool { return snap.Policy[i].ServiceID < snap.Policy[j].ServiceID })
	sort.Slice(snap.Approvals, func(i, j int) bool {
		if snap.Approvals[i].AgentID != snap.Approvals[j].AgentID {
			return snap.Approvals[i].AgentID < snap.Approvals[j].AgentID
		}
		return snap.Approvals[i].ServiceID < snap.Approvals[j].ServiceID
	})
	sort.Slice(snap.Pins, func(i, j int) bool { return snap.Pins[i].ServiceID < snap.Pins[j].ServiceID })
	sort.Slice(snap.UserEnabled, func(i, j int) bool { return snap.UserEnabled[i].ServiceID < snap.UserEnabled[j].ServiceID })
	return r.store.Save(snap)
}
