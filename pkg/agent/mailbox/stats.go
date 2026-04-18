package mailbox

import "time"

// Stats summarizes durable mailbox state for operator-facing health surfaces.
type Stats struct {
	Queued              int
	Failed              int
	HandedOff           int
	PendingPrivate      int
	PendingSky10Network int
	LastHandoffAt       time.Time
	LastDeliveredAt     time.Time
	LastFailureAt       time.Time
}

// Stats returns a summary of mailbox backlog and recent fallback activity.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.index == nil {
		return Stats{}
	}

	var stats Stats
	for _, state := range s.index.records {
		if state == nil || state.item.ID == "" {
			continue
		}
		record := buildRecord(state)
		if record.State == StateQueued {
			stats.Queued++
		}
		if record.State == StateFailed {
			stats.Failed++
		}
		if statePending(record.State) {
			switch record.Item.Scope() {
			case ScopeSky10Network:
				stats.PendingSky10Network++
			default:
				stats.PendingPrivate++
			}
		}
		if latest, ok := record.LatestEvent(); ok && latest.Type == EventTypeHandedOff {
			stats.HandedOff++
		}
		for _, event := range record.Events {
			switch event.Type {
			case EventTypeHandedOff:
				if event.Timestamp.After(stats.LastHandoffAt) {
					stats.LastHandoffAt = event.Timestamp
				}
			case EventTypeDelivered:
				if event.Timestamp.After(stats.LastDeliveredAt) {
					stats.LastDeliveredAt = event.Timestamp
				}
			case EventTypeDeliveryFailed:
				if event.Timestamp.After(stats.LastFailureAt) {
					stats.LastFailureAt = event.Timestamp
				}
			}
		}
	}
	return stats
}
