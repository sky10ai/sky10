//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/agent"
	agentmailbox "github.com/sky10/sky10/pkg/agent/mailbox"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

type rpcIdentityInfo struct {
	Address      string `json:"address"`
	DeviceID     string `json:"device_id"`
	DevicePubKey string `json:"device_pubkey"`
	DeviceCount  int    `json:"device_count"`
}

type rpcMailboxRecordResult struct {
	Item     agentmailbox.Record    `json:"item"`
	Found    bool                   `json:"found,omitempty"`
	Delivery agent.DeliveryMetadata `json:"delivery"`
}

type rpcMailboxListResult struct {
	Items []agentmailbox.Record `json:"items"`
	Count int                   `json:"count"`
}

type rpcQueueDiscoverResult struct {
	Offers []agentmailbox.QueueOffer `json:"offers"`
	Count  int                       `json:"count"`
}

type rpcQueueClaimResult struct {
	Claim  agentmailbox.QueueClaim `json:"claim"`
	Status string                  `json:"status"`
}

type rpcLinkStatusResult struct {
	Health struct {
		Nostr struct {
			Subscriptions []struct {
				Label          string `json:"label"`
				ActiveRelays   int    `json:"active_relays"`
				RequiredRelays int    `json:"required_relays"`
				LastError      string `json:"last_error"`
			} `json:"subscriptions"`
		} `json:"nostr"`
	} `json:"health"`
}

type rpcKVGetResult struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Found bool   `json:"found"`
}

type rpcSecretsGetResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Payload string `json:"payload"`
}

type rpcSecretsListResult struct {
	Items []struct {
		ID                 string   `json:"id"`
		Name               string   `json:"name"`
		Scope              string   `json:"scope"`
		RecipientDeviceIDs []string `json:"recipient_device_ids"`
	} `json:"items"`
	Count int `json:"count"`
}

func rpcCall[T any](t *testing.T, home, method string, params any) T {
	t.Helper()

	var out T
	if err := rpcCallInto(home, method, params, &out); err != nil {
		t.Fatalf("rpc %s on %s: %v", method, home, err)
	}
	return out
}

func rpcCallInto(home, method string, params any, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", daemonSocketPath(home))
	if err != nil {
		return fmt.Errorf("dial daemon socket: %w", err)
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params: %w", err)
		}
	}

	req := skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	var resp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("%s", resp.Error.Message)
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		return fmt.Errorf("unmarshal result: %w", err)
	}
	return nil
}

func daemonSocketPath(home string) string {
	runtimeDir := filepath.Join(home, "run")
	path := filepath.Join(runtimeDir, "sky10.sock")
	if runtime.GOOS == "windows" || len(path) < 100 {
		return path
	}
	sum := sha256.Sum256([]byte(runtimeDir))
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = os.TempDir()
	}
	return filepath.Join(base, "sky10-"+hex.EncodeToString(sum[:6])+".sock")
}

func identityInfo(t *testing.T, home string) rpcIdentityInfo {
	t.Helper()
	return rpcCall[rpcIdentityInfo](t, home, "identity.show", nil)
}

func waitForNostrSubscription(t *testing.T, home, label string) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcLinkStatusResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcLinkStatusResult](t, home, "skylink.status", nil)
		for _, sub := range last.Health.Nostr.Subscriptions {
			if sub.Label == label && sub.ActiveRelays > 0 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("subscription %q not active on %s; last status: %+v", label, home, last.Health.Nostr.Subscriptions)
}

func waitForMailboxRecord(t *testing.T, home, principalID, principalKind, itemID string, cond func(rpcMailboxRecordResult) bool) rpcMailboxRecordResult {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcMailboxRecordResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcMailboxRecordResult](t, home, "agent.mailbox.get", map[string]any{
			"item_id":        itemID,
			"principal_id":   principalID,
			"principal_kind": principalKind,
		})
		if cond(last) {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mailbox item %s on %s did not satisfy condition; last=%+v", itemID, home, last)
	return rpcMailboxRecordResult{}
}

func waitForMailboxList(t *testing.T, home, method string, params map[string]any, cond func(rpcMailboxListResult) bool) rpcMailboxListResult {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcMailboxListResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcMailboxListResult](t, home, method, params)
		if cond(last) {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("mailbox list %s on %s did not satisfy condition; params=%v last=%+v", method, home, params, last)
	return rpcMailboxListResult{}
}

func waitForKVValueRPC(t *testing.T, home, key, want string) rpcKVGetResult {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcKVGetResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcKVGetResult](t, home, "skykv.get", map[string]any{
			"key": key,
		})
		if last.Found && last.Value == want {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("kv %s on %s = found=%v len=%d, want len=%d", key, home, last.Found, len(last.Value), len(want))
	return rpcKVGetResult{}
}

func waitForSecretValueRPC(t *testing.T, home, idOrName, want, wantScope string) rpcSecretsGetResult {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var (
		last    rpcSecretsGetResult
		lastErr error
	)
	for time.Now().Before(deadline) {
		last = rpcSecretsGetResult{}
		lastErr = rpcCallInto(home, "secrets.get", map[string]any{
			"id_or_name": idOrName,
		}, &last)
		if lastErr == nil {
			payload, err := base64.StdEncoding.DecodeString(last.Payload)
			if err == nil && string(payload) == want && (wantScope == "" || last.Scope == wantScope) {
				return last
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("secret %s on %s did not converge: last_err=%v last_scope=%q", idOrName, home, lastErr, last.Scope)
	return rpcSecretsGetResult{}
}

func waitForSecretErrorRPC(t *testing.T, home, idOrName, wantErr string) {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = rpcCallInto(home, "secrets.get", map[string]any{
			"id_or_name": idOrName,
		}, nil)
		if lastErr != nil && strings.Contains(lastErr.Error(), wantErr) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("secret %s on %s did not fail with %q; last_err=%v", idOrName, home, wantErr, lastErr)
}

func waitForSecretRecipientCountRPC(t *testing.T, home, idOrName string, want int) {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcSecretsListResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcSecretsListResult](t, home, "secrets.list", nil)
		for _, item := range last.Items {
			if item.ID == idOrName || item.Name == idOrName {
				if len(item.RecipientDeviceIDs) == want {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("secret %s on %s did not reach %d recipients; last=%+v", idOrName, home, want, last.Items)
}

func waitForSecretMissingInListRPC(t *testing.T, home, idOrName string) {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	var last rpcSecretsListResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcSecretsListResult](t, home, "secrets.list", nil)
		found := false
		for _, item := range last.Items {
			if item.ID == idOrName || item.Name == idOrName {
				found = true
				break
			}
		}
		if !found {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("secret %s on %s still listed; last=%+v", idOrName, home, last.Items)
}

func waitForQueueOffers(t *testing.T, home, skill, queue string, cond func(rpcQueueDiscoverResult) bool) rpcQueueDiscoverResult {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var last rpcQueueDiscoverResult
	for time.Now().Before(deadline) {
		last = rpcCall[rpcQueueDiscoverResult](t, home, "agent.queue.discover", map[string]any{
			"skill": skill,
			"queue": queue,
		})
		if cond(last) {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("queue discover on %s did not satisfy condition; skill=%q queue=%q last=%+v", home, skill, queue, last)
	return rpcQueueDiscoverResult{}
}

func ensureMailboxRecordMissing(t *testing.T, home, principalID, principalKind, itemID string, dur time.Duration) {
	t.Helper()

	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		record := rpcCall[rpcMailboxRecordResult](t, home, "agent.mailbox.get", map[string]any{
			"item_id":        itemID,
			"principal_id":   principalID,
			"principal_kind": principalKind,
		})
		if record.Found {
			t.Fatalf("mailbox item %s unexpectedly visible on %s: %+v", itemID, home, record)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
