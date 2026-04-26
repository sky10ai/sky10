package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	skyagent "github.com/sky10/sky10/pkg/agent"
	"github.com/sky10/sky10/pkg/logging"
	skysandbox "github.com/sky10/sky10/pkg/sandbox"
)

const (
	sandboxAgentCacheTTL          = 3 * time.Second
	sandboxAgentListTimeout       = 1500 * time.Millisecond
	sandboxAgentEndpointTimeout   = 1200 * time.Millisecond
	sandboxAgentResolveTimeout    = 1500 * time.Millisecond
	sandboxAgentWebSocketTimeout  = 10 * time.Second
	sandboxAgentWebSocketReadSize = 64 << 20
	sandboxAgentManifestPath      = "agent-manifest.json"
)

type sandboxAgentLister interface {
	List(ctx context.Context) (*skysandbox.ListResult, error)
}

type sandboxAgentSource struct {
	lister          sandboxAgentLister
	client          *http.Client
	logger          *slog.Logger
	cacheTTL        time.Duration
	listTimeout     time.Duration
	endpointTimeout time.Duration

	mu        sync.RWMutex
	cachedAt  time.Time
	agents    []skyagent.AgentInfo
	targetsBy map[string]sandboxAgentTarget
}

type sandboxAgentTarget struct {
	Agent   skyagent.AgentInfo
	Sandbox skysandbox.Record
	BaseURL string
}

type sandboxAgentListRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newSandboxAgentSource(lister sandboxAgentLister, logger *slog.Logger) *sandboxAgentSource {
	return &sandboxAgentSource{
		lister:          lister,
		client:          &http.Client{Timeout: sandboxAgentEndpointTimeout},
		logger:          logging.WithComponent(logger, "sandbox.agent_source"),
		cacheTTL:        sandboxAgentCacheTTL,
		listTimeout:     sandboxAgentListTimeout,
		endpointTimeout: sandboxAgentEndpointTimeout,
		targetsBy:       make(map[string]sandboxAgentTarget),
	}
}

func (s *sandboxAgentSource) ListAgents(ctx context.Context) []skyagent.AgentInfo {
	if s == nil || s.lister == nil {
		return nil
	}
	if agents, ok := s.cachedAgentsIfFresh(); ok {
		return agents
	}

	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.listTimeout
	if timeout <= 0 {
		timeout = sandboxAgentListTimeout
	}
	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	agents, targetsBy, err := s.queryAgents(listCtx)
	if err != nil {
		if s.logger != nil {
			s.logger.Debug("sandbox agent list failed", "error", err)
		}
		return s.cachedAgents()
	}

	s.mu.Lock()
	s.cachedAt = time.Now()
	s.agents = append([]skyagent.AgentInfo(nil), agents...)
	s.targetsBy = targetsBy
	s.mu.Unlock()

	return append([]skyagent.AgentInfo(nil), agents...)
}

func (s *sandboxAgentSource) Resolve(ctx context.Context, nameOrID string) (sandboxAgentTarget, bool) {
	nameOrID = strings.TrimSpace(nameOrID)
	if s == nil || nameOrID == "" {
		return sandboxAgentTarget{}, false
	}
	_ = s.ListAgents(ctx)

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range sandboxAgentLookupKeys(nameOrID) {
		target, ok := s.targetsBy[key]
		if ok {
			return target, true
		}
	}
	return sandboxAgentTarget{}, false
}

func (s *sandboxAgentSource) TryProxyChat(w http.ResponseWriter, r *http.Request) bool {
	agentName := strings.TrimSpace(r.PathValue("agent"))
	if agentName == "" {
		return false
	}

	resolveCtx, cancel := context.WithTimeout(r.Context(), sandboxAgentResolveTimeout)
	target, ok := s.Resolve(resolveCtx, agentName)
	cancel()
	if !ok {
		return false
	}

	if err := s.proxyChat(w, r, target); err != nil && s.logger != nil {
		s.logger.Debug("sandbox agent websocket proxy stopped", "agent", agentName, "sandbox", target.Sandbox.Slug, "error", err)
	}
	return true
}

func (s *sandboxAgentSource) cachedAgentsIfFresh() ([]skyagent.AgentInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.agents) == 0 || s.cachedAt.IsZero() {
		return nil, false
	}
	ttl := s.cacheTTL
	if ttl <= 0 {
		ttl = sandboxAgentCacheTTL
	}
	if time.Since(s.cachedAt) > ttl {
		return nil, false
	}
	return append([]skyagent.AgentInfo(nil), s.agents...), true
}

func (s *sandboxAgentSource) cachedAgents() []skyagent.AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]skyagent.AgentInfo(nil), s.agents...)
}

func (s *sandboxAgentSource) queryAgents(ctx context.Context) ([]skyagent.AgentInfo, map[string]sandboxAgentTarget, error) {
	listed, err := s.lister.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	if listed == nil || len(listed.Sandboxes) == 0 {
		return nil, make(map[string]sandboxAgentTarget), nil
	}

	type queryResult struct {
		targets []sandboxAgentTarget
		err     error
	}

	results := make(chan queryResult, len(listed.Sandboxes))
	agents := make([]skyagent.AgentInfo, 0, len(listed.Sandboxes))
	targetsBy := make(map[string]sandboxAgentTarget)
	var launched int
	for _, rec := range listed.Sandboxes {
		manifestTargets := sandboxManifestAgentTargets(rec, "")
		if !sandboxCanHaveAgents(rec) {
			agents, targetsBy = appendSandboxAgentTargets(agents, targetsBy, manifestTargets)
			continue
		}
		baseURL, ok := sandboxSky10BaseURL(rec)
		if !ok {
			agents, targetsBy = appendSandboxAgentTargets(agents, targetsBy, manifestTargets)
			continue
		}
		manifestTargets = sandboxManifestAgentTargets(rec, baseURL)
		launched++
		go func(rec skysandbox.Record, baseURL string, manifestTargets []sandboxAgentTarget) {
			targets, err := s.querySandboxAgents(ctx, rec, baseURL)
			if err != nil && len(manifestTargets) > 0 {
				targets = manifestTargets
				err = nil
			} else if err == nil && len(manifestTargets) > 0 {
				if len(targets) == 0 {
					targets = manifestTargets
				} else {
					targets = enrichSandboxTargetsWithManifest(targets, manifestTargets[0].Agent)
				}
			}
			results <- queryResult{targets: targets, err: err}
		}(rec, baseURL, manifestTargets)
	}

	var firstErr error
	for i := 0; i < launched; i++ {
		select {
		case <-ctx.Done():
			if firstErr == nil {
				firstErr = ctx.Err()
			}
		case result := <-results:
			if result.err != nil {
				if firstErr == nil {
					firstErr = result.err
				}
				continue
			}
			for _, target := range result.targets {
				agents = append(agents, target.Agent)
				for _, key := range sandboxAgentTargetKeys(target.Agent) {
					targetsBy[key] = target
				}
			}
		}
	}

	if len(agents) == 0 && firstErr != nil {
		return nil, nil, firstErr
	}
	return agents, targetsBy, nil
}

func appendSandboxAgentTargets(agents []skyagent.AgentInfo, targetsBy map[string]sandboxAgentTarget, targets []sandboxAgentTarget) ([]skyagent.AgentInfo, map[string]sandboxAgentTarget) {
	for _, target := range targets {
		agents = append(agents, target.Agent)
		if strings.TrimSpace(target.BaseURL) == "" {
			continue
		}
		for _, key := range sandboxAgentTargetKeys(target.Agent) {
			targetsBy[key] = target
		}
	}
	return agents, targetsBy
}

func enrichSandboxTargetsWithManifest(targets []sandboxAgentTarget, manifest skyagent.AgentInfo) []sandboxAgentTarget {
	if len(manifest.Tools) == 0 && len(manifest.Skills) == 0 {
		return targets
	}
	enriched := make([]sandboxAgentTarget, 0, len(targets))
	for _, target := range targets {
		if len(target.Agent.Tools) == 0 {
			target.Agent.Tools = append([]skyagent.AgentToolSpec(nil), manifest.Tools...)
		}
		if len(target.Agent.Skills) == 0 {
			target.Agent.Skills = append([]string(nil), manifest.Skills...)
		}
		enriched = append(enriched, target)
	}
	return enriched
}

func sandboxManifestAgentTargets(rec skysandbox.Record, baseURL string) []sandboxAgentTarget {
	manifest, ok := sandboxAgentManifest(rec)
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	info := skyagent.AgentInfo{
		ID:          strings.TrimSpace(manifest.ID),
		Name:        strings.TrimSpace(manifest.Name),
		DeviceID:    strings.TrimSpace(rec.GuestDeviceID),
		Tools:       append([]skyagent.AgentToolSpec(nil), manifest.Tools...),
		Skills:      skillsFromManifestTools(manifest.Tools),
		Status:      sandboxAgentStatus(rec),
		ConnectedAt: now,
	}
	if info.Name == "" {
		info.Name = strings.TrimSpace(rec.Name)
	}
	if info.ID == "" {
		info.ID = strings.TrimSpace(rec.Slug)
	}
	info = normalizeSandboxAgentInfo(info, rec, now)
	return []sandboxAgentTarget{{
		Agent:   info,
		Sandbox: rec,
		BaseURL: baseURL,
	}}
}

type sandboxAgentManifestFile struct {
	ID    string                   `json:"id"`
	Name  string                   `json:"name"`
	Tools []skyagent.AgentToolSpec `json:"tools"`
}

func sandboxAgentManifest(rec skysandbox.Record) (sandboxAgentManifestFile, bool) {
	for _, file := range rec.Files {
		if strings.TrimSpace(file.Path) != sandboxAgentManifestPath {
			continue
		}
		var manifest sandboxAgentManifestFile
		if err := json.Unmarshal([]byte(file.Content), &manifest); err != nil {
			return sandboxAgentManifestFile{}, false
		}
		if strings.TrimSpace(manifest.Name) == "" && len(manifest.Tools) == 0 {
			return sandboxAgentManifestFile{}, false
		}
		return manifest, true
	}
	return sandboxAgentManifestFile{}, false
}

func skillsFromManifestTools(tools []skyagent.AgentToolSpec) []string {
	seen := map[string]struct{}{}
	skills := make([]string, 0, len(tools)*2)
	for _, tool := range tools {
		for _, value := range []string{tool.Capability, tool.Name} {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			skills = append(skills, value)
		}
	}
	return skills
}

func sandboxAgentStatus(rec skysandbox.Record) string {
	if status := strings.TrimSpace(rec.Status); status != "" {
		return status
	}
	if vmStatus := strings.TrimSpace(rec.VMStatus); vmStatus != "" {
		return strings.ToLower(vmStatus)
	}
	return "provisioned"
}

func (s *sandboxAgentSource) querySandboxAgents(ctx context.Context, rec skysandbox.Record, baseURL string) ([]sandboxAgentTarget, error) {
	timeout := s.endpointTimeout
	if timeout <= 0 {
		timeout = sandboxAgentEndpointTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"agent.list","params":{}}`)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/rpc", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := s.client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s agent.list: %w", rec.Slug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("%s agent.list: HTTP %d", rec.Slug, resp.StatusCode)
	}

	var rpcResp sandboxAgentListRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("%s agent.list decode: %w", rec.Slug, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("%s agent.list: %s", rec.Slug, rpcResp.Error.Message)
	}

	var listed struct {
		Agents []skyagent.AgentInfo `json:"agents"`
	}
	if len(rpcResp.Result) > 0 {
		if err := json.Unmarshal(rpcResp.Result, &listed); err != nil {
			return nil, fmt.Errorf("%s agent.list result decode: %w", rec.Slug, err)
		}
	}

	targets := make([]sandboxAgentTarget, 0, len(listed.Agents))
	now := time.Now().UTC()
	for _, info := range listed.Agents {
		if !sandboxAgentBelongsToRecord(info, rec) {
			continue
		}
		info = normalizeSandboxAgentInfo(info, rec, now)
		if strings.TrimSpace(info.ID) == "" && strings.TrimSpace(info.Name) == "" {
			continue
		}
		targets = append(targets, sandboxAgentTarget{
			Agent:   info,
			Sandbox: rec,
			BaseURL: baseURL,
		})
	}
	return targets, nil
}

func sandboxAgentBelongsToRecord(info skyagent.AgentInfo, rec skysandbox.Record) bool {
	recordDeviceID := strings.TrimSpace(rec.GuestDeviceID)
	reportedDeviceID := strings.TrimSpace(info.DeviceID)
	return recordDeviceID == "" || reportedDeviceID == "" || reportedDeviceID == recordDeviceID
}

func (s *sandboxAgentSource) proxyChat(w http.ResponseWriter, r *http.Request, target sandboxAgentTarget) error {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return nil
	}

	agentPath := strings.TrimSpace(target.Agent.ID)
	if agentPath == "" {
		agentPath = strings.TrimSpace(target.Agent.Name)
	}
	wsURL, err := skyagent.ChatSmokeWebSocketURL(target.BaseURL, agentPath, sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return err
	}

	dialCtx, cancel := context.WithTimeout(r.Context(), sandboxAgentWebSocketTimeout)
	guestConn, resp, err := websocket.Dial(dialCtx, wsURL, nil)
	cancel()
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("sandbox agent websocket unavailable: %v", err), http.StatusBadGateway)
		return err
	}
	defer guestConn.Close(websocket.StatusNormalClosure, "")
	guestConn.SetReadLimit(sandboxAgentWebSocketReadSize)

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*", "[::1]:*"},
	})
	if err != nil {
		return err
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")
	clientConn.SetReadLimit(sandboxAgentWebSocketReadSize)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		errCh <- copySandboxAgentWebSocket(ctx, clientConn, guestConn)
	}()
	go func() {
		errCh <- copySandboxAgentWebSocket(ctx, guestConn, clientConn)
	}()

	err = <-errCh
	cancel()
	if closeStatus := websocket.CloseStatus(err); closeStatus == websocket.StatusNormalClosure || closeStatus == websocket.StatusGoingAway {
		return nil
	}
	return err
}

func copySandboxAgentWebSocket(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		messageType, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, messageType, data); err != nil {
			return err
		}
	}
}

func sandboxCanHaveAgents(rec skysandbox.Record) bool {
	status := strings.TrimSpace(rec.Status)
	vmStatus := strings.TrimSpace(rec.VMStatus)
	return status == "ready" || status == "starting" || vmStatus == "Running"
}

func sandboxSky10BaseURL(rec skysandbox.Record) (string, bool) {
	for _, endpoint := range rec.ForwardedEndpoints {
		if endpoint.Name != skysandbox.ForwardedEndpointSky10 || endpoint.HostPort <= 0 {
			continue
		}
		host := strings.TrimSpace(endpoint.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(endpoint.HostPort)), true
	}
	if rec.ForwardedPort > 0 {
		host := strings.TrimSpace(rec.ForwardedHost)
		if host == "" {
			host = "127.0.0.1"
		}
		return "http://" + net.JoinHostPort(host, strconv.Itoa(rec.ForwardedPort)), true
	}
	if ip := strings.TrimSpace(rec.IPAddress); ip != "" {
		return "http://" + net.JoinHostPort(ip, "9101"), true
	}
	return "", false
}

func normalizeSandboxAgentInfo(info skyagent.AgentInfo, rec skysandbox.Record, now time.Time) skyagent.AgentInfo {
	if deviceID := strings.TrimSpace(rec.GuestDeviceID); deviceID != "" {
		info.DeviceID = deviceID
	}
	if strings.TrimSpace(info.DeviceName) == "" {
		if slug := strings.TrimSpace(rec.Slug); slug != "" {
			info.DeviceName = "lima-" + slug
		} else {
			info.DeviceName = strings.TrimSpace(rec.Name)
		}
	}
	if strings.TrimSpace(info.Status) == "" {
		info.Status = "connected"
	}
	if info.ConnectedAt.IsZero() {
		info.ConnectedAt = now
	}
	return info
}

func sandboxAgentLookupKeys(nameOrID string) []string {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return nil
	}
	return []string{
		"id:" + nameOrID,
		"name:" + nameOrID,
		"name-lower:" + strings.ToLower(nameOrID),
	}
}

func sandboxAgentTargetKeys(info skyagent.AgentInfo) []string {
	keys := make([]string, 0, 3)
	if id := strings.TrimSpace(info.ID); id != "" {
		keys = append(keys, "id:"+id)
	}
	if name := strings.TrimSpace(info.Name); name != "" {
		keys = append(keys, "name:"+name, "name-lower:"+strings.ToLower(name))
	}
	return keys
}
