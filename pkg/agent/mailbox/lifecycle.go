package mailbox

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const defaultLifecycleSweepInterval = time.Minute

const (
	deadLetterReasonRetryBudget = "retry_budget_exhausted"
)

// RetryPolicy defines retry-budget and backoff defaults for one item kind.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Delay returns the recommended delay before the next retry after attempt
// delivery failures.
func (p RetryPolicy) Delay(attempt int) time.Duration {
	if attempt <= 0 || p.InitialBackoff <= 0 {
		return 0
	}
	delay := p.InitialBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
		if p.MaxBackoff > 0 && delay >= p.MaxBackoff {
			return p.MaxBackoff
		}
	}
	if p.MaxBackoff > 0 && delay > p.MaxBackoff {
		return p.MaxBackoff
	}
	return delay
}

// LifecyclePolicy defines creation, retry, and retention defaults for one
// mailbox item kind.
type LifecyclePolicy struct {
	DefaultTTL         time.Duration
	Retry              RetryPolicy
	DeliveredRetention time.Duration
	TerminalRetention  time.Duration
	AckExplicit        bool
}

// LifecycleSweepResult reports the number of transitions performed during one
// lifecycle sweep.
type LifecycleSweepResult struct {
	Expired      int
	LeaseExpired int
	DeadLettered int
	Compacted    int
}

// DefaultLifecycleSweepInterval returns the daemon-side lifecycle maintenance
// cadence.
func DefaultLifecycleSweepInterval() time.Duration {
	return defaultLifecycleSweepInterval
}

// DefaultLifecyclePolicy returns the mailbox lifecycle defaults for an item
// kind. These defaults govern implicit TTL assignment, retry budgeting, and
// retention after terminal resolution.
func DefaultLifecyclePolicy(kind string) LifecyclePolicy {
	switch kind {
	case ItemKindApprovalRequest:
		return LifecyclePolicy{
			DefaultTTL: 72 * time.Hour,
			Retry: RetryPolicy{
				MaxAttempts:    8,
				InitialBackoff: time.Minute,
				MaxBackoff:     time.Hour,
			},
			TerminalRetention: 14 * 24 * time.Hour,
			AckExplicit:       true,
		}
	case ItemKindPaymentRequired, ItemKindPaymentProof:
		return LifecyclePolicy{
			DefaultTTL: 30 * time.Minute,
			Retry: RetryPolicy{
				MaxAttempts:    10,
				InitialBackoff: 15 * time.Second,
				MaxBackoff:     10 * time.Minute,
			},
			TerminalRetention: 30 * 24 * time.Hour,
			AckExplicit:       true,
		}
	case ItemKindResult:
		return LifecyclePolicy{
			DefaultTTL: 24 * time.Hour,
			Retry: RetryPolicy{
				MaxAttempts:    16,
				InitialBackoff: 10 * time.Second,
				MaxBackoff:     30 * time.Minute,
			},
			TerminalRetention: 14 * 24 * time.Hour,
			AckExplicit:       true,
		}
	case ItemKindReceipt:
		return LifecyclePolicy{
			DefaultTTL: 7 * 24 * time.Hour,
			Retry: RetryPolicy{
				MaxAttempts:    16,
				InitialBackoff: 10 * time.Second,
				MaxBackoff:     30 * time.Minute,
			},
			TerminalRetention: 90 * 24 * time.Hour,
			AckExplicit:       true,
		}
	case ItemKindTaskRequest:
		return LifecyclePolicy{
			DefaultTTL: 24 * time.Hour,
			Retry: RetryPolicy{
				MaxAttempts:    12,
				InitialBackoff: 15 * time.Second,
				MaxBackoff:     15 * time.Minute,
			},
			TerminalRetention: 14 * 24 * time.Hour,
			AckExplicit:       true,
		}
	case ItemKindMessage:
		return LifecyclePolicy{
			DefaultTTL: 24 * time.Hour,
			Retry: RetryPolicy{
				MaxAttempts:    8,
				InitialBackoff: 5 * time.Second,
				MaxBackoff:     5 * time.Minute,
			},
			DeliveredRetention: time.Hour,
			TerminalRetention:  7 * 24 * time.Hour,
			AckExplicit:        true,
		}
	default:
		return LifecyclePolicy{
			DefaultTTL: 24 * time.Hour,
			Retry: RetryPolicy{
				MaxAttempts:    8,
				InitialBackoff: 10 * time.Second,
				MaxBackoff:     10 * time.Minute,
			},
			TerminalRetention: 7 * 24 * time.Hour,
			AckExplicit:       true,
		}
	}
}

// NextRetryAt returns the earliest recommended retry time for a failed record.
// It is advisory: operator-triggered repair actions may bypass it.
func NextRetryAt(record Record) (time.Time, bool) {
	if record.State != StateFailed {
		return time.Time{}, false
	}
	policy := DefaultLifecyclePolicy(record.Item.Kind)
	failures := deliveryFailureCount(record)
	if policy.Retry.MaxAttempts > 0 && failures >= policy.Retry.MaxAttempts {
		return time.Time{}, false
	}
	lastFailure, ok := latestEventOfType(record, EventTypeDeliveryFailed)
	if !ok {
		return time.Time{}, false
	}
	delay := policy.Retry.Delay(failures)
	return lastFailure.Timestamp.Add(delay), true
}

// Sweep applies expiry, dead-letter, and retention policy to all records.
func (s *Store) Sweep(ctx context.Context) (LifecycleSweepResult, error) {
	var result LifecycleSweepResult
	now := s.now().UTC()
	records := s.listMatching(func(record Record) bool { return true })
	for _, record := range records {
		changed, err := s.sweepRecord(ctx, now, record, &result)
		if err != nil {
			return result, err
		}
		if !changed {
			continue
		}
	}
	return result, nil
}

// RunLifecycle periodically applies lifecycle policy until the context is
// cancelled.
func (s *Store) RunLifecycle(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultLifecycleSweepInterval()
	}
	if _, err := s.Sweep(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.Sweep(ctx); err != nil {
				return err
			}
		}
	}
}

func (s *Store) applyCreateDefaults(item Item) Item {
	policy := DefaultLifecyclePolicy(item.Kind)
	if item.ExpiresAt.IsZero() && policy.DefaultTTL > 0 {
		item.ExpiresAt = s.now().UTC().Add(policy.DefaultTTL)
	}
	return item
}

func (s *Store) sweepRecord(ctx context.Context, now time.Time, record Record, result *LifecycleSweepResult) (bool, error) {
	changed := false
	if record.Claim != nil && !now.Before(record.Claim.ExpiresAt) {
		if !hasRecordEvent(record, EventTypeLeaseExpired, "lease_id", record.Claim.Token) {
			if _, err := s.AppendEvent(ctx, Event{
				ItemID:  record.Item.ID,
				Type:    EventTypeLeaseExpired,
				Actor:   lifecycleActor(record.Item.Scope()),
				LeaseID: record.Claim.Token,
				Meta: map[string]string{
					"lease_id": record.Claim.Token,
					"queue":    record.Item.QueueName(),
				},
			}); err != nil {
				return changed, err
			}
			result.LeaseExpired++
			changed = true
		}
		if _, released, err := s.Release(ctx, record.Item.ID, record.Claim.Holder, record.Claim.Token); err != nil {
			return changed, err
		} else if released {
			changed = true
		}
		if refreshed, ok := s.Get(record.Item.ID); ok {
			record = refreshed
		}
	}

	policy := DefaultLifecyclePolicy(record.Item.Kind)
	if shouldCompactDeliveredRecord(now, record, policy) {
		if err := s.compactRecord(ctx, record); err != nil {
			return changed, fmt.Errorf("delete compacted mailbox item %s: %w", record.Item.ID, err)
		}
		result.Compacted++
		changed = true
		return changed, nil
	}

	if !record.Terminal() && !record.Item.ExpiresAt.IsZero() && !now.Before(record.Item.ExpiresAt) {
		if !hasRecordEvent(record, EventTypeExpired, "", "") {
			if _, err := s.AppendEvent(ctx, Event{
				ItemID: record.Item.ID,
				Type:   EventTypeExpired,
				Actor:  lifecycleActor(record.Item.Scope()),
			}); err != nil {
				return changed, err
			}
			result.Expired++
			changed = true
		}
		if refreshed, ok := s.Get(record.Item.ID); ok {
			record = refreshed
		}
	}

	if record.State == StateFailed {
		policy := DefaultLifecyclePolicy(record.Item.Kind)
		failures := deliveryFailureCount(record)
		if policy.Retry.MaxAttempts > 0 && failures >= policy.Retry.MaxAttempts {
			if !hasRecordEvent(record, EventTypeDeadLettered, "reason", deadLetterReasonRetryBudget) {
				if _, err := s.AppendEvent(ctx, Event{
					ItemID: record.Item.ID,
					Type:   EventTypeDeadLettered,
					Actor:  lifecycleActor(record.Item.Scope()),
					Error:  latestFailureError(record),
					Meta: map[string]string{
						"reason":   deadLetterReasonRetryBudget,
						"attempts": strconv.Itoa(failures),
					},
				}); err != nil {
					return changed, err
				}
				result.DeadLettered++
				changed = true
			}
			if refreshed, ok := s.Get(record.Item.ID); ok {
				record = refreshed
			}
		}
	}

	if record.Terminal() && policy.TerminalRetention > 0 && !recordActivityTime(record).IsZero() && !now.Before(recordActivityTime(record).Add(policy.TerminalRetention)) {
		if err := s.compactRecord(ctx, record); err != nil {
			return changed, fmt.Errorf("delete compacted mailbox item %s: %w", record.Item.ID, err)
		}
		result.Compacted++
		changed = true
	}
	return changed, nil
}

func lifecycleActor(scope string) Principal {
	return Principal{
		ID:    "system:mailbox",
		Kind:  PrincipalKindLocalAgent,
		Scope: scope,
	}
}

func hasRecordEvent(record Record, eventType, metaKey, metaValue string) bool {
	for _, event := range record.Events {
		if event.Type != eventType {
			continue
		}
		if metaKey == "" {
			return true
		}
		if event.Meta != nil && event.Meta[metaKey] == metaValue {
			return true
		}
	}
	return false
}

func latestEventOfType(record Record, eventType string) (Event, bool) {
	for i := len(record.Events) - 1; i >= 0; i-- {
		if record.Events[i].Type == eventType {
			return cloneEvent(record.Events[i]), true
		}
	}
	return Event{}, false
}

func deliveryFailureCount(record Record) int {
	var count int
	for _, event := range record.Events {
		if event.Type == EventTypeDeliveryFailed {
			count++
		}
	}
	return count
}

func latestFailureError(record Record) string {
	event, ok := latestEventOfType(record, EventTypeDeliveryFailed)
	if !ok {
		return ""
	}
	return event.Error
}

func recordActivityTime(record Record) time.Time {
	if latest, ok := record.LatestEvent(); ok {
		return latest.Timestamp
	}
	return record.Item.CreatedAt
}

func shouldCompactDeliveredRecord(now time.Time, record Record, policy LifecyclePolicy) bool {
	if record.State != StateDelivered {
		return false
	}
	if !record.Item.ExpiresAt.IsZero() && !now.Before(record.Item.ExpiresAt) {
		return true
	}
	if policy.DeliveredRetention <= 0 {
		return false
	}
	activity := recordActivityTime(record)
	if activity.IsZero() {
		return false
	}
	return !now.Before(activity.Add(policy.DeliveredRetention))
}

func (s *Store) compactRecord(ctx context.Context, record Record) error {
	if err := s.backend.DeleteItem(ctx, record.Item.ID); err != nil {
		return err
	}
	s.mu.Lock()
	if s.index != nil {
		s.index.delete(record.Item.ID)
	}
	s.mu.Unlock()
	return nil
}
