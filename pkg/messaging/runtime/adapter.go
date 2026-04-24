package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/sky10/sky10/pkg/messaging/protocol"
)

// AdapterClient wraps a ProcessHost with typed messaging adapter operations.
type AdapterClient struct {
	host *ProcessHost
}

// StartAdapter launches one adapter child process and returns a typed client.
func StartAdapter(ctx context.Context, spec ProcessSpec, notify NotificationHandler) (*AdapterClient, error) {
	host, err := StartProcess(ctx, spec, notify)
	if err != nil {
		return nil, err
	}
	return NewAdapterClient(host), nil
}

// NewAdapterClient wraps an existing process host with typed adapter calls.
func NewAdapterClient(host *ProcessHost) *AdapterClient {
	return &AdapterClient{host: host}
}

// Host returns the underlying process host.
func (c *AdapterClient) Host() *ProcessHost {
	if c == nil {
		return nil
	}
	return c.host
}

// PID returns the adapter process ID.
func (c *AdapterClient) PID() int {
	if c == nil || c.host == nil {
		return 0
	}
	return c.host.PID()
}

// Stderr returns buffered adapter stderr output.
func (c *AdapterClient) Stderr() string {
	if c == nil || c.host == nil {
		return ""
	}
	return c.host.Stderr()
}

// Wait waits for the adapter process to exit.
func (c *AdapterClient) Wait() error {
	if c == nil || c.host == nil {
		return nil
	}
	return c.host.Wait()
}

// Close requests adapter shutdown and waits for completion.
func (c *AdapterClient) Close() error {
	if c == nil || c.host == nil {
		return nil
	}
	return c.host.Close()
}

// Describe returns the adapter manifest and verifies protocol compatibility.
func (c *AdapterClient) Describe(ctx context.Context) (protocol.DescribeResult, error) {
	var result protocol.DescribeResult
	err := c.call(ctx, protocol.MethodDescribe, protocol.DescribeParams{
		BrokerProtocol: protocol.CurrentProtocol(),
	}, &result)
	if err != nil {
		return protocol.DescribeResult{}, err
	}
	if err := result.Protocol.Validate(); err != nil {
		return protocol.DescribeResult{}, fmt.Errorf("describe protocol: %w", err)
	}
	if err := result.Adapter.Validate(); err != nil {
		return protocol.DescribeResult{}, fmt.Errorf("describe adapter: %w", err)
	}
	if err := ValidateProtocolCompatibility(protocol.CurrentProtocol(), result.Protocol); err != nil {
		return protocol.DescribeResult{}, err
	}
	return result, nil
}

// ValidateConfig asks the adapter to validate one connection.
func (c *AdapterClient) ValidateConfig(ctx context.Context, params protocol.ValidateConfigParams) (protocol.ValidateConfigResult, error) {
	var result protocol.ValidateConfigResult
	if err := c.call(ctx, protocol.MethodValidateConfig, params, &result); err != nil {
		return protocol.ValidateConfigResult{}, err
	}
	return result, nil
}

// Connect starts one connection inside the adapter.
func (c *AdapterClient) Connect(ctx context.Context, params protocol.ConnectParams) (protocol.ConnectResult, error) {
	var result protocol.ConnectResult
	if err := c.call(ctx, protocol.MethodConnect, params, &result); err != nil {
		return protocol.ConnectResult{}, err
	}
	return result, nil
}

// Refresh refreshes one connection inside the adapter.
func (c *AdapterClient) Refresh(ctx context.Context, params protocol.RefreshParams) (protocol.RefreshResult, error) {
	var result protocol.RefreshResult
	if err := c.call(ctx, protocol.MethodRefresh, params, &result); err != nil {
		return protocol.RefreshResult{}, err
	}
	return result, nil
}

// ListIdentities enumerates local identities for one connection.
func (c *AdapterClient) ListIdentities(ctx context.Context, params protocol.ListIdentitiesParams) (protocol.ListIdentitiesResult, error) {
	var result protocol.ListIdentitiesResult
	if err := c.call(ctx, protocol.MethodListIdentities, params, &result); err != nil {
		return protocol.ListIdentitiesResult{}, err
	}
	return result, nil
}

// ListConversations enumerates conversations for one connection.
func (c *AdapterClient) ListConversations(ctx context.Context, params protocol.ListConversationsParams) (protocol.ListConversationsResult, error) {
	var result protocol.ListConversationsResult
	if err := c.call(ctx, protocol.MethodListConversations, params, &result); err != nil {
		return protocol.ListConversationsResult{}, err
	}
	return result, nil
}

// ListMessages enumerates messages for one conversation.
func (c *AdapterClient) ListMessages(ctx context.Context, params protocol.ListMessagesParams) (protocol.ListMessagesResult, error) {
	var result protocol.ListMessagesResult
	if err := c.call(ctx, protocol.MethodListMessages, params, &result); err != nil {
		return protocol.ListMessagesResult{}, err
	}
	return result, nil
}

// GetMessage returns one normalized message.
func (c *AdapterClient) GetMessage(ctx context.Context, params protocol.GetMessageParams) (protocol.GetMessageResult, error) {
	var result protocol.GetMessageResult
	if err := c.call(ctx, protocol.MethodGetMessage, params, &result); err != nil {
		return protocol.GetMessageResult{}, err
	}
	return result, nil
}

// ListContainers enumerates provider-side containers such as mailboxes,
// folders, and labels.
func (c *AdapterClient) ListContainers(ctx context.Context, params protocol.ListContainersParams) (protocol.ListContainersResult, error) {
	var result protocol.ListContainersResult
	if err := c.call(ctx, protocol.MethodListContainers, params, &result); err != nil {
		return protocol.ListContainersResult{}, err
	}
	return result, nil
}

// CreateDraft validates or creates one draft through the adapter.
func (c *AdapterClient) CreateDraft(ctx context.Context, params protocol.CreateDraftParams) (protocol.CreateDraftResult, error) {
	var result protocol.CreateDraftResult
	if err := c.call(ctx, protocol.MethodCreateDraft, params, &result); err != nil {
		return protocol.CreateDraftResult{}, err
	}
	return result, nil
}

// UpdateDraft updates one draft through the adapter.
func (c *AdapterClient) UpdateDraft(ctx context.Context, params protocol.UpdateDraftParams) (protocol.UpdateDraftResult, error) {
	var result protocol.UpdateDraftResult
	if err := c.call(ctx, protocol.MethodUpdateDraft, params, &result); err != nil {
		return protocol.UpdateDraftResult{}, err
	}
	return result, nil
}

// DeleteDraft deletes one draft through the adapter when supported.
func (c *AdapterClient) DeleteDraft(ctx context.Context, params protocol.DeleteDraftParams) (protocol.DeleteDraftResult, error) {
	var result protocol.DeleteDraftResult
	if err := c.call(ctx, protocol.MethodDeleteDraft, params, &result); err != nil {
		return protocol.DeleteDraftResult{}, err
	}
	return result, nil
}

// SendMessage sends one outbound message.
func (c *AdapterClient) SendMessage(ctx context.Context, params protocol.SendMessageParams) (protocol.SendResult, error) {
	var result protocol.SendResult
	if err := c.call(ctx, protocol.MethodSendMessage, params, &result); err != nil {
		return protocol.SendResult{}, err
	}
	return result, nil
}

// ReplyMessage replies within an existing conversation.
func (c *AdapterClient) ReplyMessage(ctx context.Context, params protocol.ReplyMessageParams) (protocol.SendResult, error) {
	var result protocol.SendResult
	if err := c.call(ctx, protocol.MethodReplyMessage, params, &result); err != nil {
		return protocol.SendResult{}, err
	}
	return result, nil
}

// MoveMessages moves messages to another provider-side container.
func (c *AdapterClient) MoveMessages(ctx context.Context, params protocol.MoveMessagesParams) (protocol.ManageMessagesResult, error) {
	var result protocol.ManageMessagesResult
	if err := c.call(ctx, protocol.MethodMoveMessages, params, &result); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return result, nil
}

// MoveConversation moves a conversation to another provider-side container.
func (c *AdapterClient) MoveConversation(ctx context.Context, params protocol.MoveConversationParams) (protocol.ManageMessagesResult, error) {
	var result protocol.ManageMessagesResult
	if err := c.call(ctx, protocol.MethodMoveConversation, params, &result); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return result, nil
}

// ArchiveMessages archives messages using the adapter's platform semantics.
func (c *AdapterClient) ArchiveMessages(ctx context.Context, params protocol.ArchiveMessagesParams) (protocol.ManageMessagesResult, error) {
	var result protocol.ManageMessagesResult
	if err := c.call(ctx, protocol.MethodArchiveMessages, params, &result); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return result, nil
}

// ArchiveConversation archives a conversation using the adapter's platform
// semantics.
func (c *AdapterClient) ArchiveConversation(ctx context.Context, params protocol.ArchiveConversationParams) (protocol.ManageMessagesResult, error) {
	var result protocol.ManageMessagesResult
	if err := c.call(ctx, protocol.MethodArchiveConversation, params, &result); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return result, nil
}

// ApplyLabels mutates label-like container membership for messages or a
// conversation.
func (c *AdapterClient) ApplyLabels(ctx context.Context, params protocol.ApplyLabelsParams) (protocol.ManageMessagesResult, error) {
	var result protocol.ManageMessagesResult
	if err := c.call(ctx, protocol.MethodApplyLabels, params, &result); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return result, nil
}

// MarkRead changes read state for messages or a conversation.
func (c *AdapterClient) MarkRead(ctx context.Context, params protocol.MarkReadParams) (protocol.ManageMessagesResult, error) {
	var result protocol.ManageMessagesResult
	if err := c.call(ctx, protocol.MethodMarkRead, params, &result); err != nil {
		return protocol.ManageMessagesResult{}, err
	}
	return result, nil
}

// HandleWebhook converts one broker-owned webhook hit into normalized events.
func (c *AdapterClient) HandleWebhook(ctx context.Context, params protocol.HandleWebhookParams) (protocol.HandleWebhookResult, error) {
	var result protocol.HandleWebhookResult
	if err := c.call(ctx, protocol.MethodHandleWebhook, params, &result); err != nil {
		return protocol.HandleWebhookResult{}, err
	}
	return result, nil
}

// Poll asks the adapter for newly observed events since the last checkpoint.
func (c *AdapterClient) Poll(ctx context.Context, params protocol.PollParams) (protocol.PollResult, error) {
	var result protocol.PollResult
	if err := c.call(ctx, protocol.MethodPoll, params, &result); err != nil {
		return protocol.PollResult{}, err
	}
	return result, nil
}

// Health returns the adapter's reported health.
func (c *AdapterClient) Health(ctx context.Context, params protocol.HealthParams) (protocol.HealthResult, error) {
	var result protocol.HealthResult
	if err := c.call(ctx, protocol.MethodHealth, params, &result); err != nil {
		return protocol.HealthResult{}, err
	}
	return result, nil
}

// ValidateProtocolCompatibility checks whether the broker and adapter protocol
// descriptors can speak to each other.
func ValidateProtocolCompatibility(local, remote protocol.ProtocolInfo) error {
	if err := local.Validate(); err != nil {
		return fmt.Errorf("local protocol: %w", err)
	}
	if err := remote.Validate(); err != nil {
		return fmt.Errorf("remote protocol: %w", err)
	}
	if strings.TrimSpace(local.Name) != strings.TrimSpace(remote.Name) {
		return fmt.Errorf("protocol name mismatch: local=%q remote=%q", local.Name, remote.Name)
	}
	if local.Version == remote.Version {
		return nil
	}
	for _, version := range remote.CompatibleVersions {
		if version == local.Version {
			return nil
		}
	}
	for _, version := range local.CompatibleVersions {
		if version == remote.Version {
			return nil
		}
	}
	return fmt.Errorf("protocol version mismatch: local=%q remote=%q", local.Version, remote.Version)
}

func (c *AdapterClient) call(ctx context.Context, method protocol.Method, params any, out any) error {
	if c == nil || c.host == nil {
		return fmt.Errorf("adapter client is not running")
	}
	return c.host.Call(ctx, string(method), params, out)
}
