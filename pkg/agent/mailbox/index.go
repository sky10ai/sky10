package mailbox

import (
	"sort"
	"time"
)

type recordState struct {
	item   Item
	events []Event
	claim  *Claim
}

type recordIndex struct {
	records map[string]*recordState
}

func newRecordIndex(snapshot Snapshot) *recordIndex {
	idx := &recordIndex{records: make(map[string]*recordState, len(snapshot.Items))}
	for _, item := range snapshot.Items {
		idx.upsertItem(item)
	}
	for itemID, events := range snapshot.Events {
		sort.Slice(events, func(i, j int) bool {
			if events[i].Timestamp.Equal(events[j].Timestamp) {
				return events[i].EventID < events[j].EventID
			}
			return events[i].Timestamp.Before(events[j].Timestamp)
		})
		for _, event := range events {
			idx.appendEvent(itemID, event)
		}
	}
	for _, claim := range snapshot.Claims {
		idx.setClaim(claim)
	}
	return idx
}

func (idx *recordIndex) upsertItem(item Item) {
	state, ok := idx.records[item.ID]
	if !ok {
		idx.records[item.ID] = &recordState{item: cloneItem(item)}
		return
	}
	state.item = cloneItem(item)
}

func (idx *recordIndex) appendEvent(itemID string, event Event) {
	state, ok := idx.records[itemID]
	if !ok {
		state = &recordState{}
		idx.records[itemID] = state
	}
	state.events = append(state.events, cloneEvent(event))
}

func (idx *recordIndex) setClaim(claim Claim) {
	state, ok := idx.records[claim.ItemID]
	if !ok {
		state = &recordState{}
		idx.records[claim.ItemID] = state
	}
	state.claim = cloneClaim(&claim)
}

func (idx *recordIndex) clearClaim(itemID string) {
	state, ok := idx.records[itemID]
	if !ok {
		return
	}
	state.claim = nil
}

func (idx *recordIndex) get(itemID string) (Record, bool) {
	state, ok := idx.records[itemID]
	if !ok || state == nil || state.item.ID == "" {
		return Record{}, false
	}
	return buildRecord(state), true
}

func (idx *recordIndex) listInbox(principalID string) []Record {
	return idx.list(func(record Record) bool {
		return record.Item.RecipientID() == principalID && !record.Terminal() && !record.Failed()
	})
}

func (idx *recordIndex) listOutbox(principalID string) []Record {
	return idx.list(func(record Record) bool {
		return record.Item.From.ID == principalID && !record.Terminal()
	})
}

func (idx *recordIndex) listQueue(queue string) []Record {
	return idx.list(func(record Record) bool {
		return record.Item.QueueName() == queue && !record.Terminal()
	})
}

func (idx *recordIndex) listSent(principalID string) []Record {
	return idx.list(func(record Record) bool {
		return record.Item.From.ID == principalID && record.Terminal() && !record.Failed()
	})
}

func (idx *recordIndex) listFailed(principalID string) []Record {
	return idx.list(func(record Record) bool {
		if record.Item.From.ID == principalID {
			return record.Failed()
		}
		return record.Item.RecipientID() == principalID && record.Failed()
	})
}

func (idx *recordIndex) list(fn func(Record) bool) []Record {
	out := make([]Record, 0)
	for _, state := range idx.records {
		if state == nil || state.item.ID == "" {
			continue
		}
		record := buildRecord(state)
		if fn(record) {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := recordActivityAt(out[i])
		right := recordActivityAt(out[j])
		if left.Equal(right) {
			return out[i].Item.ID < out[j].Item.ID
		}
		return left.After(right)
	})
	return out
}

func buildRecord(state *recordState) Record {
	record := Record{
		Item:  cloneItem(state.item),
		Claim: cloneClaim(state.claim),
	}
	record.Events = make([]Event, len(state.events))
	for i, event := range state.events {
		record.Events[i] = cloneEvent(event)
	}
	record.State = deriveState(record.Events, record.Claim)
	return record
}

func deriveState(events []Event, claim *Claim) State {
	latest := StateQueued
	if len(events) > 0 {
		latest = stateForEvent(events[len(events)-1].Type)
	}
	if claim != nil && !stateTerminal(latest) && latest != StateFailed {
		return StateClaimed
	}
	if claim == nil && latest == StateClaimed {
		return StateQueued
	}
	return latest
}

func stateForEvent(eventType string) State {
	switch eventType {
	case EventTypeDelivered, EventTypeSeen:
		return StateDelivered
	case EventTypeClaimed:
		return StateClaimed
	case EventTypeApproved:
		return StateApproved
	case EventTypeCompleted:
		return StateCompleted
	case EventTypeRejected:
		return StateRejected
	case EventTypeCancelled:
		return StateCancelled
	case EventTypeExpired, EventTypeLeaseExpired:
		return StateExpired
	case EventTypeDeadLettered:
		return StateDeadLettered
	case EventTypeDeliveryFailed:
		return StateFailed
	default:
		return StateQueued
	}
}

func recordActivityAt(record Record) timeKey {
	if latest, ok := record.LatestEvent(); ok {
		return timeKey{value: latest.Timestamp}
	}
	return timeKey{value: record.Item.CreatedAt}
}

type timeKey struct {
	value time.Time
}

func (k timeKey) Equal(other timeKey) bool {
	return k.value.Equal(other.value)
}

func (k timeKey) After(other timeKey) bool {
	return k.value.After(other.value)
}
