package link

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

const (
	nostrSubscriptionConnectTimeout = 10 * time.Second
	nostrSubscriptionPruneAfter     = time.Hour
	nostrSubscriptionMaxSeen        = 4096
)

type nostrEventHandler func(relay string, event *nostr.Event) error

// RunNostrSubscription maintains long-lived subscriptions across the relay set
// and reconnects automatically when individual relays drop.
func RunNostrSubscription(
	ctx context.Context,
	relays []string,
	tracker *NostrRelayTracker,
	logger *slog.Logger,
	label string,
	filters nostr.Filters,
	handler nostrEventHandler,
) error {
	if len(relays) == 0 || handler == nil {
		return nil
	}

	logger = defaultLogger(logger)
	dedupe := newNostrEventDeduper()
	var wg sync.WaitGroup
	for _, relayURL := range relays {
		relayURL := relayURL
		wg.Add(1)
		go func() {
			defer wg.Done()
			runNostrRelaySubscription(ctx, relayURL, tracker, logger, label, filters, dedupe, handler)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

func runNostrRelaySubscription(
	ctx context.Context,
	relayURL string,
	tracker *NostrRelayTracker,
	logger *slog.Logger,
	label string,
	filters nostr.Filters,
	dedupe *nostrEventDeduper,
	handler nostrEventHandler,
) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		relay := nostr.NewRelay(ctx, relayURL)
		started := time.Now()
		connectCtx, cancel := context.WithTimeout(ctx, nostrSubscriptionConnectTimeout)
		err := relay.Connect(connectCtx)
		cancel()
		recordNostrRelay(tracker, relayURL, time.Since(started), err)
		if err != nil {
			logger.Debug("nostr subscription connect failed", "relay", relayURL, "label", label, "error", err)
			if !waitNostrSubscriptionRetry(ctx, attempt) {
				return
			}
			attempt++
			continue
		}

		sub, err := relay.Subscribe(ctx, filters, nostr.WithLabel(label), nostr.WithCheckDuplicate(dedupe.Check))
		if err != nil {
			recordNostrRelay(tracker, relayURL, 0, err)
			logger.Debug("nostr subscription start failed", "relay", relayURL, "label", label, "error", err)
			_ = relay.Close()
			if !waitNostrSubscriptionRetry(ctx, attempt) {
				return
			}
			attempt++
			continue
		}

		attempt = 0
		if !consumeNostrSubscription(ctx, relay, sub, relayURL, tracker, logger, handler) {
			return
		}
	}
}

func consumeNostrSubscription(
	ctx context.Context,
	relay *nostr.Relay,
	sub *nostr.Subscription,
	relayURL string,
	tracker *NostrRelayTracker,
	logger *slog.Logger,
	handler nostrEventHandler,
) bool {
	defer func() {
		sub.Unsub()
		_ = relay.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return false
		case evt, ok := <-sub.Events:
			if !ok {
				if ctx.Err() == nil {
					recordNostrRelay(tracker, relayURL, 0, fmt.Errorf("subscription closed"))
				}
				return ctx.Err() == nil
			}
			if err := handler(relayURL, evt); err != nil {
				logger.Debug("nostr subscription handler failed", "relay", relayURL, "error", err)
			}
		case reason := <-sub.ClosedReason:
			err := fmt.Errorf("subscription closed: %s", reason)
			recordNostrRelay(tracker, relayURL, 0, err)
			logger.Debug("nostr subscription closed", "relay", relayURL, "reason", reason)
			return ctx.Err() == nil
		case <-sub.EndOfStoredEvents:
			recordNostrRelay(tracker, relayURL, 0, nil)
		case <-relay.Context().Done():
			if ctx.Err() == nil {
				recordNostrRelay(tracker, relayURL, 0, context.Cause(relay.Context()))
			}
			return ctx.Err() == nil
		}
	}
}

func waitNostrSubscriptionRetry(ctx context.Context, attempt int) bool {
	backoff := time.Second
	for i := 0; i < attempt && backoff < 15*time.Second; i++ {
		backoff *= 2
	}
	if backoff > 15*time.Second {
		backoff = 15 * time.Second
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func recordNostrRelay(tracker *NostrRelayTracker, relay string, latency time.Duration, err error) {
	if tracker == nil {
		return
	}
	tracker.Record(relay, latency, err)
}

type nostrEventDeduper struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newNostrEventDeduper() *nostrEventDeduper {
	return &nostrEventDeduper{seen: make(map[string]time.Time)}
}

func (d *nostrEventDeduper) Check(id, _ string) bool {
	if d == nil || id == "" {
		return false
	}
	now := time.Now().UTC()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pruneLocked(now)
	if _, ok := d.seen[id]; ok {
		return true
	}
	d.seen[id] = now
	return false
}

func (d *nostrEventDeduper) pruneLocked(now time.Time) {
	if len(d.seen) < nostrSubscriptionMaxSeen {
		return
	}
	cutoff := now.Add(-nostrSubscriptionPruneAfter)
	for id, seenAt := range d.seen {
		if seenAt.Before(cutoff) {
			delete(d.seen, id)
		}
	}
	if len(d.seen) < nostrSubscriptionMaxSeen {
		return
	}
	for id := range d.seen {
		delete(d.seen, id)
		if len(d.seen) <= nostrSubscriptionMaxSeen/2 {
			break
		}
	}
}
