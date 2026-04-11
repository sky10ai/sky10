package collections

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Lease is a time-bounded claim record stored at a single KV key.
type Lease struct {
	store   KVStore
	prefix  string
	now     func() time.Time
	newUUID func() string
}

// LeaseRecord is the durable representation of a claim.
type LeaseRecord struct {
	Name       string    `json:"name"`
	Holder     string    `json:"holder"`
	Token      string    `json:"token"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// NewLease creates a lease primitive rooted at prefix.
func NewLease(store KVStore, prefix string) *Lease {
	return &Lease{
		store:   store,
		prefix:  normalizeCollectionPrefix(prefix),
		now:     func() time.Time { return time.Now().UTC() },
		newUUID: func() string { return uuid.NewString() },
	}
}

// Claim acquires a lease if it is missing or expired.
func (l *Lease) Claim(ctx context.Context, name, holder string, ttl time.Duration) (LeaseRecord, bool, error) {
	if err := validateLeaseInput(name, holder, ttl); err != nil {
		return LeaseRecord{}, false, err
	}

	current, ok, err := l.Get(name)
	if err != nil {
		return LeaseRecord{}, false, err
	}
	now := l.now().UTC()
	if ok && !current.Expired(now) {
		return current, false, nil
	}

	record := LeaseRecord{
		Name:       name,
		Holder:     holder,
		Token:      l.newUUID(),
		AcquiredAt: now,
		ExpiresAt:  now.Add(ttl),
	}
	if err := l.put(ctx, record); err != nil {
		return LeaseRecord{}, false, err
	}

	stored, ok, err := l.Get(name)
	if err != nil {
		return LeaseRecord{}, false, err
	}
	if !ok || stored.Token != record.Token {
		return stored, false, nil
	}
	return stored, true, nil
}

// Renew extends an existing lease if the holder and token still match.
func (l *Lease) Renew(ctx context.Context, name, holder, token string, ttl time.Duration) (LeaseRecord, bool, error) {
	if err := validateLeaseInput(name, holder, ttl); err != nil {
		return LeaseRecord{}, false, err
	}
	if token == "" {
		return LeaseRecord{}, false, fmt.Errorf("lease token is required")
	}

	current, ok, err := l.Get(name)
	if err != nil {
		return LeaseRecord{}, false, err
	}
	now := l.now().UTC()
	if !ok || current.Expired(now) {
		return LeaseRecord{}, false, nil
	}
	if current.Holder != holder || current.Token != token {
		return current, false, nil
	}

	current.ExpiresAt = now.Add(ttl)
	if err := l.put(ctx, current); err != nil {
		return LeaseRecord{}, false, err
	}

	stored, ok, err := l.Get(name)
	if err != nil {
		return LeaseRecord{}, false, err
	}
	if !ok || stored.Token != token {
		return stored, false, nil
	}
	return stored, true, nil
}

// Release removes a lease if the holder and token still match.
func (l *Lease) Release(ctx context.Context, name, holder, token string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, fmt.Errorf("lease name is required")
	}
	if strings.TrimSpace(holder) == "" {
		return false, fmt.Errorf("lease holder is required")
	}
	if token == "" {
		return false, fmt.Errorf("lease token is required")
	}

	current, ok, err := l.getRaw(name)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if current.Holder != holder || current.Token != token {
		return false, nil
	}
	if err := l.store.Delete(ctx, l.key(name)); err != nil {
		return false, err
	}
	return true, nil
}

// Get returns the current non-expired lease for name.
func (l *Lease) Get(name string) (LeaseRecord, bool, error) {
	if l == nil || l.store == nil {
		return LeaseRecord{}, false, fmt.Errorf("lease store is required")
	}
	if strings.TrimSpace(name) == "" {
		return LeaseRecord{}, false, fmt.Errorf("lease name is required")
	}

	raw, ok := l.store.Get(l.key(name))
	if !ok {
		return LeaseRecord{}, false, nil
	}
	var record LeaseRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return LeaseRecord{}, false, fmt.Errorf("parsing lease %q: %w", name, err)
	}
	if record.Expired(l.now().UTC()) {
		return LeaseRecord{}, false, nil
	}
	return record, true, nil
}

// Expired reports whether a lease record is no longer valid at now.
func (r LeaseRecord) Expired(now time.Time) bool {
	return !now.Before(r.ExpiresAt)
}

func (l *Lease) put(ctx context.Context, record LeaseRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return l.store.Set(ctx, l.key(record.Name), data)
}

func (l *Lease) getRaw(name string) (LeaseRecord, bool, error) {
	if l == nil || l.store == nil {
		return LeaseRecord{}, false, fmt.Errorf("lease store is required")
	}
	if strings.TrimSpace(name) == "" {
		return LeaseRecord{}, false, fmt.Errorf("lease name is required")
	}
	raw, ok := l.store.Get(l.key(name))
	if !ok {
		return LeaseRecord{}, false, nil
	}
	var record LeaseRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return LeaseRecord{}, false, fmt.Errorf("parsing lease %q: %w", name, err)
	}
	return record, true, nil
}

func (l *Lease) key(name string) string {
	return l.prefix + "/" + name
}

func validateLeaseInput(name, holder string, ttl time.Duration) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("lease name is required")
	}
	if strings.TrimSpace(holder) == "" {
		return fmt.Errorf("lease holder is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("lease ttl must be positive")
	}
	return nil
}
