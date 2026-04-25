package host

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/messaging"
	messagingbroker "github.com/sky10/sky10/pkg/messaging/broker"
	"github.com/sky10/sky10/pkg/messaging/protocol"
	messagingshim "github.com/sky10/sky10/pkg/messaging/shim"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func TestServeExposesOnlyShimRPCSurface(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shim host currently uses the repo's Unix-socket RPC server")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socketDir, err := os.MkdirTemp("", "sky10-shim-host-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "shim.sock")
	ready := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- Serve(ctx, Config{
			SocketPath: socketPath,
			Version:    "test",
			Service: &hostTestService{
				connections: []messaging.Connection{{
					ID:        "slack/work",
					AdapterID: "slack",
					Label:     "Work Slack",
				}},
			},
			OnServe: func() { close(ready) },
		})
	}()

	select {
	case <-ready:
	case err := <-errCh:
		t.Fatalf("Serve() returned before ready: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shim host")
	}

	var list struct {
		Connections []messaging.Connection `json:"connections"`
		Count       int                    `json:"count"`
	}
	callShimRPC(t, socketPath, string(messagingshim.MethodListConnections), nil, &list)
	if list.Count != 1 || list.Connections[0].ID != "slack/work" {
		t.Fatalf("list connections = %+v, want slack/work", list)
	}

	draft := messaging.Draft{
		ID:              "draft/work",
		ConnectionID:    "slack/work",
		ConversationID:  "conv/work",
		LocalIdentityID: "identity/work",
		Parts:           []messaging.MessagePart{{Kind: messaging.MessagePartKindText, Text: "Yes."}},
		Status:          messaging.DraftStatusPending,
	}
	var create messagingbroker.DraftMutationResult
	callShimRPC(t, socketPath, string(messagingshim.MethodCreateDraft), map[string]any{"draft": draft}, &create)
	if create.Draft.ID != draft.ID {
		t.Fatalf("create draft = %+v, want %s", create.Draft, draft.ID)
	}

	var send messagingbroker.RequestSendDraftResult
	callShimRPC(t, socketPath, string(messagingshim.MethodRequestSend), map[string]any{"draft_id": draft.ID}, &send)
	if send.Approval == nil || send.Approval.Status != messaging.ApprovalStatusPending {
		t.Fatalf("request send approval = %+v, want pending approval", send.Approval)
	}
	if send.Message != nil {
		t.Fatalf("request send message = %+v, want nil", send.Message)
	}

	err = callShimRPCError(t, socketPath, "messaging.connections", nil)
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("messaging.connections error = %v, want method not found", err)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve() after cancel error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shim host shutdown")
	}
}

func TestServeRequiresSocketAndService(t *testing.T) {
	t.Parallel()

	if err := Serve(context.Background(), Config{Service: &hostTestService{}}); err == nil || !strings.Contains(err.Error(), "socket_path") {
		t.Fatalf("Serve(no socket) = %v, want socket_path error", err)
	}
	if err := Serve(context.Background(), Config{SocketPath: filepath.Join(t.TempDir(), "shim.sock")}); err == nil || !strings.Contains(err.Error(), "service") {
		t.Fatalf("Serve(no service) = %v, want service error", err)
	}
}

func callShimRPC(t *testing.T, socketPath, method string, params any, out any) {
	t.Helper()
	if err := callShimRPCErrorInto(socketPath, method, params, out); err != nil {
		t.Fatalf("rpc %s error = %v", method, err)
	}
}

func callShimRPCError(t *testing.T, socketPath, method string, params any) error {
	t.Helper()
	var ignored json.RawMessage
	return callShimRPCErrorInto(socketPath, method, params, &ignored)
}

func callShimRPCErrorInto(socketPath, method string, params any, out any) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return err
		}
	}
	req := skyrpc.Request{JSONRPC: "2.0", Method: method, Params: rawParams, ID: 1}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return &rpcTestError{message: resp.Error.Message}
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return err
		}
	}
	return nil
}

type rpcTestError struct {
	message string
}

func (e *rpcTestError) Error() string {
	return e.message
}

type hostTestService struct {
	connections []messaging.Connection
}

func (s *hostTestService) ListConnections(context.Context) ([]messaging.Connection, error) {
	return s.connections, nil
}

func (s *hostTestService) ListIdentities(context.Context, messaging.ConnectionID) ([]messaging.Identity, error) {
	return []messaging.Identity{{ID: "identity/work", ConnectionID: "slack/work", Kind: messaging.IdentityKindBot}}, nil
}

func (s *hostTestService) ListConversations(context.Context, messaging.ConnectionID) ([]messaging.Conversation, error) {
	return []messaging.Conversation{{ID: "conv/work", ConnectionID: "slack/work", LocalIdentityID: "identity/work", Kind: messaging.ConversationKindDirect}}, nil
}

func (s *hostTestService) GetConversation(context.Context, messaging.ConnectionID, messaging.ConversationID) (messaging.Conversation, bool, error) {
	return messaging.Conversation{ID: "conv/work", ConnectionID: "slack/work", LocalIdentityID: "identity/work", Kind: messaging.ConversationKindDirect}, true, nil
}

func (s *hostTestService) GetMessages(context.Context, messaging.ConnectionID, messaging.ConversationID) ([]messaging.Message, error) {
	return []messaging.Message{{ID: "msg/work", ConnectionID: "slack/work", ConversationID: "conv/work", LocalIdentityID: "identity/work"}}, nil
}

func (s *hostTestService) ListContainers(context.Context, protocol.ListContainersParams) (protocol.ListContainersResult, error) {
	return protocol.ListContainersResult{Containers: []messaging.Container{{ID: "container/work/inbox", ConnectionID: "slack/work", Kind: messaging.ContainerKindInbox}}}, nil
}

func (s *hostTestService) CreateDraft(_ context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error) {
	return messagingbroker.DraftMutationResult{Draft: draft}, nil
}

func (s *hostTestService) UpdateDraft(_ context.Context, draft messaging.Draft) (messagingbroker.DraftMutationResult, error) {
	return messagingbroker.DraftMutationResult{Draft: draft}, nil
}

func (s *hostTestService) RequestSend(_ context.Context, draftID messaging.DraftID, _ bool) (messagingbroker.RequestSendDraftResult, error) {
	return messagingbroker.RequestSendDraftResult{
		Draft: messaging.Draft{ID: draftID},
		Approval: &messaging.Approval{
			ID:      "approval/work",
			DraftID: draftID,
			Status:  messaging.ApprovalStatusPending,
		},
	}, nil
}

func (s *hostTestService) MoveMessages(context.Context, protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error) {
	return protocol.ManageMessagesResult{}, nil
}

func (s *hostTestService) MoveConversation(context.Context, protocol.MoveConversationParams) (protocol.ManageMessagesResult, error) {
	return protocol.ManageMessagesResult{}, nil
}

func (s *hostTestService) ArchiveMessages(context.Context, protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error) {
	return protocol.ManageMessagesResult{}, nil
}

func (s *hostTestService) ArchiveConversation(context.Context, protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error) {
	return protocol.ManageMessagesResult{}, nil
}

func (s *hostTestService) ApplyLabels(context.Context, protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error) {
	return protocol.ManageMessagesResult{}, nil
}

func (s *hostTestService) MarkRead(context.Context, protocol.MarkReadParams) (protocol.ManageMessagesResult, error) {
	return protocol.ManageMessagesResult{}, nil
}
