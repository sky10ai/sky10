package x402

import (
	"errors"
	"fmt"
	"sort"
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
	mu        sync.RWMutex
	manifests map[string]ServiceManifest
	policy    map[string]PolicyEntry
	approvals map[approvalKey]Approval
	pins      map[approvalKey]Pin
	store     RegistryStore
	clock     func() time.Time
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
		manifests: make(map[string]ServiceManifest),
		policy:    make(map[string]PolicyEntry),
		approvals: make(map[approvalKey]Approval),
		pins:      make(map[approvalKey]Pin),
		store:     store,
		clock:     now,
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
	Category    string
	Tier        Tier
	PriceUSDC   string
	Hint        string
}

// ListApproved returns the services this agent has approved, joined
// with policy overlay (tier, hint) and the manifest's display
// metadata. Sorted by service id for stable iteration.
func (r *Registry) ListApproved(agentID string) []ListApprovedListing {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ListApprovedListing
	for key, ap := range r.approvals {
		if key.agentID != agentID {
			continue
		}
		m, ok := r.manifests[key.serviceID]
		if !ok {
			continue
		}
		entry := ListApprovedListing{
			ID:          m.ID,
			DisplayName: m.DisplayName,
			Category:    m.Category,
			Tier:        ap.Tier,
			PriceUSDC:   m.MaxPriceUSDC,
		}
		if p, ok := r.policy[key.serviceID]; ok {
			entry.Tier = p.Tier
			entry.Hint = p.Hint
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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
	sort.Slice(snap.Manifests, func(i, j int) bool { return snap.Manifests[i].ID < snap.Manifests[j].ID })
	sort.Slice(snap.Policy, func(i, j int) bool { return snap.Policy[i].ServiceID < snap.Policy[j].ServiceID })
	sort.Slice(snap.Approvals, func(i, j int) bool {
		if snap.Approvals[i].AgentID != snap.Approvals[j].AgentID {
			return snap.Approvals[i].AgentID < snap.Approvals[j].AgentID
		}
		return snap.Approvals[i].ServiceID < snap.Approvals[j].ServiceID
	})
	sort.Slice(snap.Pins, func(i, j int) bool { return snap.Pins[i].ServiceID < snap.Pins[j].ServiceID })
	return r.store.Save(snap)
}
