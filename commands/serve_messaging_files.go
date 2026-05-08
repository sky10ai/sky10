package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	skyconfig "github.com/sky10/sky10/pkg/config"
	"github.com/sky10/sky10/pkg/messaging"
)

const sandboxStateGuestRoot = "/sandbox-state"

type messengerBridgeFiles struct {
	sandboxAgents *sandboxAgentSource
	stateDir      func(context.Context, sandboxAgentTarget) (string, error)
}

func newMessengerBridgeFiles(sandboxAgents *sandboxAgentSource) *messengerBridgeFiles {
	return &messengerBridgeFiles{
		sandboxAgents: sandboxAgents,
		stateDir:      defaultMessengerSandboxStateDir,
	}
}

func (m *messengerBridgeFiles) MaterializeMessages(ctx context.Context, agentID string, messages []messaging.Message) ([]messaging.Message, error) {
	if !messagesNeedBridgeFiles(messages) {
		return messages, nil
	}
	stateDir, err := m.resolveSandboxStateDir(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := append([]messaging.Message(nil), messages...)
	for messageIdx := range out {
		message := out[messageIdx]
		parts := append([]messaging.MessagePart(nil), message.Parts...)
		for partIdx := range parts {
			part := parts[partIdx]
			if !messagePartNeedsMaterialization(part) {
				continue
			}
			ref, err := m.materializeMessagePart(stateDir, message, part, partIdx)
			if err != nil {
				return nil, err
			}
			part.Ref = ref
			if part.Metadata == nil {
				part.Metadata = make(map[string]string)
			} else {
				part.Metadata = cloneMessagingBridgeMap(part.Metadata)
			}
			part.Metadata["sky10_guest_path"] = ref
			parts[partIdx] = part
		}
		message.Parts = parts
		out[messageIdx] = message
	}
	return out, nil
}

func (m *messengerBridgeFiles) HostDraftRefs(ctx context.Context, agentID string, draft messaging.Draft) (messaging.Draft, error) {
	if !draftNeedsHostFileRefs(draft) {
		return draft, nil
	}
	stateDir, err := m.resolveSandboxStateDir(ctx, agentID)
	if err != nil {
		return messaging.Draft{}, err
	}
	parts := append([]messaging.MessagePart(nil), draft.Parts...)
	for idx := range parts {
		part := parts[idx]
		if !messagePartCanCarryFile(part) || strings.TrimSpace(part.Ref) == "" {
			continue
		}
		hostPath, err := hostPathForGuestRef(stateDir, part.Ref)
		if err != nil {
			return messaging.Draft{}, err
		}
		if err := validateBridgeRegularFile(hostPath); err != nil {
			return messaging.Draft{}, err
		}
		part.Ref = hostPath
		parts[idx] = part
	}
	draft.Parts = parts
	return draft, nil
}

func (m *messengerBridgeFiles) resolveSandboxStateDir(ctx context.Context, agentID string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("messenger bridge file mount resolver is unavailable")
	}
	if m.sandboxAgents == nil {
		return defaultMessengerSandboxStateDirForSlug(agentID)
	}
	target, ok := m.sandboxAgents.Resolve(ctx, agentID)
	if !ok {
		return defaultMessengerSandboxStateDirForSlug(agentID)
	}
	if m.stateDir == nil {
		return defaultMessengerSandboxStateDir(ctx, target)
	}
	stateDir, err := m.stateDir(ctx, target)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(stateDir) == "" {
		return "", fmt.Errorf("messenger bridge file mount for agent %s is empty", agentID)
	}
	return filepath.Clean(stateDir), nil
}

func (m *messengerBridgeFiles) materializeMessagePart(stateDir string, message messaging.Message, part messaging.MessagePart, partIdx int) (string, error) {
	source := strings.TrimSpace(part.Ref)
	if !filepath.IsAbs(source) {
		return "", fmt.Errorf("messaging file ref %q is not an absolute host path", source)
	}
	if err := validateBridgeRegularFile(source); err != nil {
		return "", err
	}
	rel := []string{
		"messengers",
		"inbox",
		safeBridgePathSegment(string(message.ConnectionID), "connection"),
		safeBridgePathSegment(string(message.ConversationID), "conversation"),
		safeBridgePathSegment(string(message.ID), "message"),
		bridgePartFileName(part, source, partIdx),
	}
	hostDest := filepath.Join(append([]string{stateDir}, rel...)...)
	if err := copyBridgeFile(source, hostDest); err != nil {
		return "", err
	}
	return path.Join(append([]string{sandboxStateGuestRoot}, rel...)...), nil
}

func defaultMessengerSandboxStateDir(_ context.Context, target sandboxAgentTarget) (string, error) {
	slug := strings.TrimSpace(target.Sandbox.Slug)
	if slug == "" {
		return "", fmt.Errorf("sandbox slug is required")
	}
	return defaultMessengerSandboxStateDirForSlug(slug)
}

func defaultMessengerSandboxStateDirForSlug(slug string) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" || strings.ContainsAny(slug, `/\`) {
		return "", fmt.Errorf("sandbox slug %q is invalid", slug)
	}
	root, err := skyconfig.RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sandboxes", slug, "state"), nil
}

func messagesNeedBridgeFiles(messages []messaging.Message) bool {
	for _, message := range messages {
		for _, part := range message.Parts {
			if messagePartNeedsMaterialization(part) {
				return true
			}
		}
	}
	return false
}

func messagePartNeedsMaterialization(part messaging.MessagePart) bool {
	ref := strings.TrimSpace(part.Ref)
	return messagePartCanCarryFile(part) && ref != "" && !strings.HasPrefix(path.Clean(filepath.ToSlash(ref)), sandboxStateGuestRoot+"/")
}

func draftNeedsHostFileRefs(draft messaging.Draft) bool {
	for _, part := range draft.Parts {
		if messagePartCanCarryFile(part) && strings.TrimSpace(part.Ref) != "" {
			return true
		}
	}
	return false
}

func messagePartCanCarryFile(part messaging.MessagePart) bool {
	return part.Kind == messaging.MessagePartKindFile || part.Kind == messaging.MessagePartKindImage
}

func bridgePartFileName(part messaging.MessagePart, source string, idx int) string {
	name := strings.TrimSpace(part.FileName)
	if name == "" {
		name = filepath.Base(source)
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = fmt.Sprintf("part-%02d", idx+1)
	}
	return fmt.Sprintf("%02d-%s", idx+1, safeBridgePathSegment(name, fmt.Sprintf("part-%02d", idx+1)))
}

func safeBridgePathSegment(value, fallback string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		keep := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' ||
			r == '-' ||
			r == '_'
		if keep {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" || out == "." || out == ".." {
		return fallback
	}
	return out
}

func hostPathForGuestRef(stateDir, ref string) (string, error) {
	cleanRef := path.Clean(strings.TrimSpace(ref))
	if cleanRef == sandboxStateGuestRoot || !strings.HasPrefix(cleanRef, sandboxStateGuestRoot+"/") {
		return "", fmt.Errorf("messaging file ref %q must be under %s", ref, sandboxStateGuestRoot)
	}
	rel := strings.TrimPrefix(cleanRef, sandboxStateGuestRoot+"/")
	hostPath := filepath.Clean(filepath.Join(stateDir, filepath.FromSlash(rel)))
	cleanState := filepath.Clean(stateDir)
	if hostPath == cleanState || !strings.HasPrefix(hostPath, cleanState+string(filepath.Separator)) {
		return "", fmt.Errorf("messaging file ref %q escapes sandbox state", ref)
	}
	return hostPath, nil
}

func validateBridgeRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("messaging file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("messaging file %s is a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("messaging file %s is not a regular file", path)
	}
	return nil
}

func copyBridgeFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("creating messenger bridge file dir: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening messenger bridge file: %w", err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating messenger bridge temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copying messenger bridge file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing messenger bridge temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod messenger bridge temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(dst)
		if retryErr := os.Rename(tmpPath, dst); retryErr != nil {
			return fmt.Errorf("installing messenger bridge file: %w", retryErr)
		}
	}
	return nil
}
