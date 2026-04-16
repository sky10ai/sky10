package commands

import (
	"context"
	"errors"
	"strings"

	skyagent "github.com/sky10/sky10/pkg/agent"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	"github.com/sky10/sky10/pkg/link"
)

const (
	mailboxRetryReasonPrivate     = "mailbox_private"
	mailboxRetryReasonNetwork     = "mailbox_network"
	mailboxRetryReasonNetworkItem = "mailbox_network_item"
)

func mailboxRetryTrigger(action string, record agentmailbox.Record) (string, string, bool) {
	action = strings.TrimSpace(action)
	if action != "queued" && action != "handed_off" {
		return "", "", false
	}

	switch record.Item.Scope() {
	case agentmailbox.ScopeSky10Network:
		if record.Item.QueueName() != "" {
			if action != "queued" || strings.TrimSpace(record.Item.ID) == "" {
				return "", "", false
			}
			return mailboxRetryReasonNetworkItem, record.Item.ID, true
		}
		if record.Item.To == nil {
			return "", "", false
		}
		if address := strings.TrimSpace(record.Item.To.RouteAddress()); address != "" {
			return mailboxRetryReasonNetwork, address, true
		}
		return "", "", false
	default:
		if record.Item.To == nil {
			return "", "", false
		}
		if deviceID := strings.TrimSpace(record.Item.To.DeviceHint); deviceID != "" {
			return mailboxRetryReasonPrivate, deviceID, true
		}
		return "", "", false
	}
}

func runMailboxRetryBatch(ctx context.Context, router *skyagent.Router, store *agentmailbox.Store, batch link.ConvergenceBatch) error {
	if router == nil {
		return nil
	}

	var errs []error
	for _, trigger := range batch.Reasons {
		switch trigger.Reason {
		case mailboxRetryReasonPrivate:
			if deviceID := strings.TrimSpace(trigger.Detail); deviceID != "" {
				errs = append(errs, router.DrainOutbox(ctx, deviceID))
			}
		case mailboxRetryReasonNetwork:
			if address := strings.TrimSpace(trigger.Detail); address != "" {
				errs = append(errs, router.DrainNetworkOutbox(ctx, address))
			}
		case mailboxRetryReasonNetworkItem:
			if store == nil {
				continue
			}
			itemID := strings.TrimSpace(trigger.Detail)
			if itemID == "" {
				continue
			}
			record, ok := store.Get(itemID)
			if !ok {
				continue
			}
			if record.State != agentmailbox.StateQueued && record.State != agentmailbox.StateFailed {
				continue
			}
			_, err := router.DeliverMailboxRecord(ctx, record)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
