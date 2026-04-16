package commands

import (
	"testing"

	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
)

func TestMailboxRetryTrigger(t *testing.T) {
	t.Run("private queued uses device hint", func(t *testing.T) {
		reason, detail, ok := mailboxRetryTrigger("queued", agentmailbox.Record{
			Item: agentmailbox.Item{
				ID:   "private-1",
				From: agentmailbox.Principal{ID: "sender"},
				To: &agentmailbox.Principal{
					ID:         "worker",
					Kind:       agentmailbox.PrincipalKindLocalAgent,
					Scope:      agentmailbox.ScopePrivateNetwork,
					DeviceHint: "D-worker",
				},
			},
		})
		if !ok {
			t.Fatal("expected private queued mailbox item to trigger retry")
		}
		if reason != mailboxRetryReasonPrivate || detail != "D-worker" {
			t.Fatalf("trigger = (%q, %q), want (%q, %q)", reason, detail, mailboxRetryReasonPrivate, "D-worker")
		}
	})

	t.Run("network handed off uses route address", func(t *testing.T) {
		address := "sky10q44gdywv9g54xedu8nt6mz9qph3v2ncxlljc3ns8428x7cyjhtl6qvkj9zf"
		reason, detail, ok := mailboxRetryTrigger("handed_off", agentmailbox.Record{
			Item: agentmailbox.Item{
				ID:   "network-1",
				From: agentmailbox.Principal{ID: "sender"},
				To: &agentmailbox.Principal{
					ID:        "worker",
					Kind:      agentmailbox.PrincipalKindNetworkAgent,
					Scope:     agentmailbox.ScopeSky10Network,
					RouteHint: address,
				},
			},
		})
		if !ok {
			t.Fatal("expected network handed_off mailbox item to trigger retry")
		}
		if reason != mailboxRetryReasonNetwork || detail != address {
			t.Fatalf("trigger = (%q, %q), want (%q, %q)", reason, detail, mailboxRetryReasonNetwork, address)
		}
	})

	t.Run("network queue failure retries specific item", func(t *testing.T) {
		reason, detail, ok := mailboxRetryTrigger("queued", agentmailbox.Record{
			Item: agentmailbox.Item{
				ID:          "queue-1",
				From:        agentmailbox.Principal{ID: "sender"},
				TargetSkill: "research",
				To: &agentmailbox.Principal{
					ID:    "queue",
					Kind:  agentmailbox.PrincipalKindCapabilityQueue,
					Scope: agentmailbox.ScopeSky10Network,
				},
			},
		})
		if !ok {
			t.Fatal("expected failed queue offer to trigger item retry")
		}
		if reason != mailboxRetryReasonNetworkItem || detail != "queue-1" {
			t.Fatalf("trigger = (%q, %q), want (%q, %q)", reason, detail, mailboxRetryReasonNetworkItem, "queue-1")
		}
	})

	t.Run("successful queue handoff does not retry", func(t *testing.T) {
		_, _, ok := mailboxRetryTrigger("handed_off", agentmailbox.Record{
			Item: agentmailbox.Item{
				ID:          "queue-2",
				From:        agentmailbox.Principal{ID: "sender"},
				TargetSkill: "research",
				To: &agentmailbox.Principal{
					ID:    "queue",
					Kind:  agentmailbox.PrincipalKindCapabilityQueue,
					Scope: agentmailbox.ScopeSky10Network,
				},
			},
		})
		if ok {
			t.Fatal("expected successful queue handoff to skip retry trigger")
		}
	})

	t.Run("non queue non delivery update ignored", func(t *testing.T) {
		_, _, ok := mailboxRetryTrigger("delivered", agentmailbox.Record{})
		if ok {
			t.Fatal("expected delivered update to skip retry trigger")
		}
	})
}
