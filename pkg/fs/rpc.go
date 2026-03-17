package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// RPCServer exposes skyfs operations over a Unix domain socket using JSON-RPC 2.0.
type RPCServer struct {
	store    *Store
	sockPath string
	version  string
	listener net.Listener
	logger   *slog.Logger

	syncMu     sync.Mutex
	syncCancel context.CancelFunc
	syncDir    string
	syncing    bool

	driveManager *DriveManager

	activityMu   sync.Mutex
	lastActivity time.Time

	mu      sync.Mutex
	clients map[net.Conn]bool
	events  chan RPCEvent
}

// RPCEvent is a server-push event sent to all connected clients.
type RPCEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
}

// RPCRequest is a JSON-RPC 2.0 request.
type RPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

// RPCResponse is a JSON-RPC 2.0 response.
type RPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError is a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewRPCServer creates an RPC server for the given store.
func NewRPCServer(store *Store, sockPath string, driveCfgPath string, version string, logger *slog.Logger) *RPCServer {
	if logger == nil {
		logger = slog.Default()
	}
	srv := &RPCServer{
		store:        store,
		sockPath:     sockPath,
		version:      version,
		logger:       logger,
		clients:      make(map[net.Conn]bool),
		events:       make(chan RPCEvent, 100),
		driveManager: NewDriveManager(store, driveCfgPath),
	}
	srv.driveManager.OnActivity = srv.MarkActivity
	return srv
}

// Serve starts listening and blocks until the context is cancelled.
func (s *RPCServer) Serve(ctx context.Context) error {
	os.Remove(s.sockPath) // clean up stale socket

	var err error
	s.listener, err = net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.sockPath, err)
	}
	defer s.listener.Close()
	defer os.Remove(s.sockPath)

	// Set socket permissions
	os.Chmod(s.sockPath, 0600)

	s.logger.Info("RPC server started", "socket", s.sockPath)

	// Broadcast events to clients
	go s.broadcastLoop()

	// Auto-start all enabled drives
	s.driveManager.StartAll(s.logger)

	// Auto-approve pending join requests every 20 seconds
	go s.autoApproveLoop(ctx)

	// Accept connections
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				s.logger.Warn("accept error", "error", err)
				continue
			}
		}

		s.mu.Lock()
		s.clients[conn] = true
		s.mu.Unlock()

		go s.handleConn(ctx, conn)
	}
}

// Emit sends an event to all connected clients.
func (s *RPCServer) Emit(event string, data interface{}) {
	select {
	case s.events <- RPCEvent{Event: event, Data: data}:
	default:
		// Channel full, drop event
	}
}

func (s *RPCServer) broadcastLoop() {
	for range s.events {
		// Events are logged but not pushed to RPC connections.
		// RPC connections use request-response only.
		// A separate event subscription endpoint can be added later
		// (e.g., a second socket or SSE over HTTP).
		s.logger.Debug("event emitted")
	}
}

func (s *RPCServer) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req RPCRequest
		if err := decoder.Decode(&req); err != nil {
			if err != io.EOF {
				s.logger.Debug("decode error", "error", err)
			}
			return
		}

		resp := s.dispatch(ctx, &req)
		if err := encoder.Encode(resp); err != nil {
			s.logger.Debug("encode error", "error", err)
			return
		}
	}
}

func (s *RPCServer) dispatch(ctx context.Context, req *RPCRequest) *RPCResponse {
	resp := &RPCResponse{JSONRPC: "2.0", ID: req.ID}

	var result interface{}
	var err error

	switch req.Method {
	case "skyfs.ping":
		return &RPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]string{"status": "ok"}}
	case "skyfs.list":
		result, err = s.rpcList(ctx, req.Params)
	case "skyfs.info":
		result, err = s.rpcInfo(ctx)
	case "skyfs.put":
		result, err = s.rpcPut(ctx, req.Params)
	case "skyfs.get":
		result, err = s.rpcGet(ctx, req.Params)
	case "skyfs.remove":
		result, err = s.rpcRemove(ctx, req.Params)
	case "skyfs.status":
		result, err = s.rpcStatus(ctx)
	case "skyfs.versions":
		result, err = s.rpcVersions(ctx, req.Params)
	case "skyfs.compact":
		result, err = s.rpcCompact(ctx, req.Params)
	case "skyfs.gc":
		result, err = s.rpcGC(ctx, req.Params)
	case "skyfs.syncStart":
		result, err = s.rpcSyncStart(ctx, req.Params)
	case "skyfs.syncStop":
		result, err = s.rpcSyncStop(ctx)
	case "skyfs.syncStatus":
		result, err = s.rpcSyncStatus(ctx)
	case "skyfs.driveCreate":
		result, err = s.rpcDriveCreate(ctx, req.Params)
	case "skyfs.driveRemove":
		result, err = s.rpcDriveRemove(ctx, req.Params)
	case "skyfs.driveList":
		result, err = s.rpcDriveList(ctx)
	case "skyfs.driveStart":
		result, err = s.rpcDriveStart(ctx, req.Params)
	case "skyfs.driveStop":
		result, err = s.rpcDriveStop(ctx, req.Params)
	case "skyfs.deviceList":
		result, err = s.rpcDeviceList(ctx)
	case "skyfs.deviceRemove":
		result, err = s.rpcDeviceRemove(ctx, req.Params)
	case "skyfs.invite":
		result, err = s.rpcInvite(ctx)
	case "skyfs.join":
		result, err = s.rpcJoin(ctx, req.Params)
	case "skyfs.approve":
		result, err = s.rpcApprove(ctx)
	default:
		resp.Error = &RPCError{Code: -32601, Message: "method not found: " + req.Method}
		return resp
	}

	if err != nil {
		resp.Error = &RPCError{Code: -32000, Message: err.Error()}
	} else {
		resp.Result = result
	}
	return resp
}

// RPC method implementations

type listParams struct {
	Prefix string `json:"prefix"`
}

type listResult struct {
	Files []fileInfo `json:"files"`
}

type fileInfo struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
	Checksum  string `json:"checksum"`
	Namespace string `json:"namespace"`
	Chunks    int    `json:"chunks"`
}

func (s *RPCServer) rpcList(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p listParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	entries, err := s.store.List(ctx, p.Prefix)
	if err != nil {
		return nil, err
	}

	files := make([]fileInfo, len(entries))
	for i, e := range entries {
		files[i] = fileInfo{
			Path:      e.Path,
			Size:      e.Size,
			Modified:  e.Modified.Format("2006-01-02T15:04:05Z"),
			Checksum:  e.Checksum,
			Namespace: e.Namespace,
			Chunks:    len(e.Chunks),
		}
	}

	return listResult{Files: files}, nil
}

func (s *RPCServer) rpcInfo(ctx context.Context) (interface{}, error) {
	return s.store.Info(ctx)
}

type putParams struct {
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
}

type putResult struct {
	Size   int64 `json:"size"`
	Chunks int   `json:"chunks"`
}

func (s *RPCServer) rpcPut(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p putParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	f, err := os.Open(p.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", p.LocalPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", p.LocalPath, err)
	}

	if err := s.store.Put(ctx, p.Path, f); err != nil {
		return nil, err
	}

	s.Emit("file.changed", map[string]string{"path": p.Path, "type": "put"})
	return putResult{Size: info.Size()}, nil
}

type getParams struct {
	Path    string `json:"path"`
	OutPath string `json:"out_path"`
}

type getResult struct {
	Size int64 `json:"size"`
}

func (s *RPCServer) rpcGet(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p getParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	f, err := os.Create(p.OutPath)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", p.OutPath, err)
	}

	if err := s.store.Get(ctx, p.Path, f); err != nil {
		f.Close()
		os.Remove(p.OutPath)
		return nil, err
	}

	stat, _ := f.Stat()
	f.Close()

	return getResult{Size: stat.Size()}, nil
}

type removeParams struct {
	Path string `json:"path"`
}

func (s *RPCServer) rpcRemove(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p removeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if err := s.store.Remove(ctx, p.Path); err != nil {
		return nil, err
	}

	s.Emit("file.changed", map[string]string{"path": p.Path, "type": "delete"})
	return map[string]string{"status": "ok"}, nil
}

type statusResult struct {
	Syncing bool   `json:"syncing"`
	SyncDir string `json:"sync_dir,omitempty"`
}

func (s *RPCServer) rpcStatus(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return statusResult{Syncing: s.syncing, SyncDir: s.syncDir}, nil
}

type versionsParams struct {
	Path string `json:"path"`
}

func (s *RPCServer) rpcVersions(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p versionsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	versions, err := ListVersions(ctx, s.store, p.Path)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{"versions": versions}, nil
}

type compactParams struct {
	Keep int `json:"keep"`
}

func (s *RPCServer) rpcCompact(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p compactParams
	p.Keep = 3
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	result, err := Compact(ctx, s.store.backend, s.store.identity, p.Keep)
	if err != nil {
		return nil, err
	}
	return result, nil
}

type gcParams struct {
	DryRun bool `json:"dry_run"`
}

func (s *RPCServer) rpcGC(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p gcParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	result, err := GC(ctx, s.store.backend, s.store.identity, p.DryRun)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// --- Sync control ---

type syncStartParams struct {
	Dir         string `json:"dir"`
	PollSeconds int    `json:"poll_seconds"`
}

func (s *RPCServer) rpcSyncStart(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p syncStartParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Dir == "" {
		return nil, fmt.Errorf("dir is required")
	}
	if p.PollSeconds <= 0 {
		p.PollSeconds = 30
	}

	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	// Stop existing sync if running
	if s.syncCancel != nil {
		s.syncCancel()
	}

	syncCtx, cancel := context.WithCancel(context.Background())
	s.syncCancel = cancel
	s.syncDir = p.Dir
	s.syncing = true

	ignoreMatcher := NewIgnoreMatcher(p.Dir)
	cfg := SyncConfig{
		LocalRoot:  p.Dir,
		IgnoreFunc: ignoreMatcher.IgnoreFunc(),
	}
	daemonCfg := DaemonConfig{
		SyncConfig:  cfg,
		PollSeconds: p.PollSeconds,
	}

	daemon, err := NewDaemon(s.store, nil, daemonCfg, s.logger)
	if err != nil {
		s.syncing = false
		s.syncCancel = nil
		return nil, fmt.Errorf("creating daemon: %w", err)
	}

	go func() {
		daemon.Run(syncCtx)
		s.syncMu.Lock()
		s.syncing = false
		s.syncDir = ""
		s.syncCancel = nil
		s.syncMu.Unlock()
		s.logger.Info("sync stopped", "dir", p.Dir)
	}()

	s.logger.Info("sync started", "dir", p.Dir, "poll", p.PollSeconds)
	return map[string]string{"status": "started", "dir": p.Dir}, nil
}

func (s *RPCServer) rpcSyncStop(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	if s.syncCancel == nil {
		return map[string]string{"status": "not syncing"}, nil
	}

	s.syncCancel()
	s.syncCancel = nil
	return map[string]string{"status": "stopping"}, nil
}

func (s *RPCServer) rpcSyncStatus(_ context.Context) (interface{}, error) {
	s.syncMu.Lock()
	syncing := s.syncing
	syncDir := s.syncDir
	s.syncMu.Unlock()

	s.activityMu.Lock()
	active := time.Since(s.lastActivity) < 15*time.Second
	s.activityMu.Unlock()

	return map[string]interface{}{
		"syncing":  syncing || active,
		"sync_dir": syncDir,
	}, nil
}

// MarkActivity records that sync I/O is happening right now.
func (s *RPCServer) MarkActivity() {
	s.activityMu.Lock()
	s.lastActivity = time.Now()
	s.activityMu.Unlock()
}

// --- Drive management ---

type driveCreateParams struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Namespace string `json:"namespace"`
}

type driveInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	LocalPath string `json:"local_path"`
	Namespace string `json:"namespace"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
}

func (s *RPCServer) rpcDriveCreate(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Name == "" || p.Path == "" {
		return nil, fmt.Errorf("name and path are required")
	}
	if p.Namespace == "" {
		p.Namespace = p.Name
	}

	drive, err := s.driveManager.CreateDrive(p.Name, p.Path, p.Namespace)
	if err != nil {
		return nil, err
	}

	// Auto-start
	s.driveManager.StartDrive(drive.ID, s.logger)

	return driveInfo{
		ID: drive.ID, Name: drive.Name, LocalPath: drive.LocalPath,
		Namespace: drive.Namespace, Enabled: drive.Enabled, Running: true,
	}, nil
}

type driveIDParams struct {
	ID string `json:"id"`
}

func (s *RPCServer) rpcDriveRemove(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return map[string]string{"status": "ok"}, s.driveManager.RemoveDrive(p.ID)
}

func (s *RPCServer) rpcDriveList(_ context.Context) (interface{}, error) {
	drives := s.driveManager.ListDrives()
	result := make([]driveInfo, len(drives))
	for i, d := range drives {
		result[i] = driveInfo{
			ID: d.ID, Name: d.Name, LocalPath: d.LocalPath,
			Namespace: d.Namespace, Enabled: d.Enabled,
			Running: s.driveManager.IsRunning(d.ID),
		}
	}
	return map[string]interface{}{"drives": result}, nil
}

func (s *RPCServer) rpcDriveStart(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	return map[string]string{"status": "started"}, s.driveManager.StartDrive(p.ID, s.logger)
}

func (s *RPCServer) rpcDriveStop(_ context.Context, params json.RawMessage) (interface{}, error) {
	var p driveIDParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	s.driveManager.StopDrive(p.ID)
	return map[string]string{"status": "stopped"}, nil
}

// --- Device registry ---

func (s *RPCServer) rpcDeviceList(ctx context.Context) (interface{}, error) {
	devices, err := ListDevices(ctx, s.store.backend)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"devices":     devices,
		"this_device": s.store.identity.Address(),
	}, nil
}

func (s *RPCServer) rpcDeviceRemove(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		Pubkey string `json:"pubkey"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.Pubkey == s.store.identity.Address() {
		return nil, fmt.Errorf("cannot remove this device")
	}
	id := shortPubkeyID(p.Pubkey)
	key := "devices/" + id + ".json"
	if err := s.store.backend.Delete(ctx, key); err != nil {
		return nil, err
	}
	return map[string]string{"status": "ok"}, nil
}

func (s *RPCServer) rpcInvite(ctx context.Context) (interface{}, error) {
	accessKey := os.Getenv("S3_ACCESS_KEY_ID")
	secretKey := os.Getenv("S3_SECRET_ACCESS_KEY")

	// Read endpoint/bucket from config file
	home, _ := os.UserHomeDir()
	cfgData, err := os.ReadFile(home + "/.sky10/fs/config.json")
	var endpoint, bucket, region string
	var pathStyle bool
	if err == nil {
		var cfg struct {
			Endpoint       string `json:"endpoint"`
			Bucket         string `json:"bucket"`
			Region         string `json:"region"`
			ForcePathStyle bool   `json:"force_path_style"`
		}
		json.Unmarshal(cfgData, &cfg)
		endpoint = cfg.Endpoint
		bucket = cfg.Bucket
		region = cfg.Region
		pathStyle = cfg.ForcePathStyle
	}

	code, err := CreateInvite(ctx, s.store.backend, InviteConfig{
		Endpoint:       endpoint,
		Bucket:         bucket,
		Region:         region,
		AccessKey:      accessKey,
		SecretKey:      secretKey,
		ForcePathStyle: pathStyle,
		DevicePubKey:   s.store.identity.Address(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"code": code}, nil
}

func (s *RPCServer) rpcJoin(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var p struct {
		InviteID string `json:"invite_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if p.InviteID == "" {
		return nil, fmt.Errorf("invite_id required")
	}

	// Submit this device's pubkey to the invite mailbox
	if err := SubmitJoin(ctx, s.store.backend, p.InviteID, s.store.identity.Address()); err != nil {
		return nil, fmt.Errorf("submitting join: %w", err)
	}

	// Poll for approval (up to 60 seconds)
	for i := 0; i < 12; i++ {
		granted, err := IsGranted(ctx, s.store.backend, p.InviteID)
		if err != nil {
			return nil, err
		}
		if granted {
			// Register this device
			RegisterDevice(ctx, s.store.backend, s.store.identity.Address(), GetDeviceName(), s.version)
			return map[string]string{"status": "approved"}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	return map[string]string{"status": "pending"}, nil
}

func (s *RPCServer) rpcApprove(ctx context.Context) (interface{}, error) {
	// Find pending invites
	inviteKeys, err := s.store.backend.List(ctx, "invites/")
	if err != nil {
		return nil, err
	}

	inviteIDs := make(map[string]bool)
	for _, k := range inviteKeys {
		if id := splitInvitePath2(k); id != "" {
			inviteIDs[id] = true
		}
	}

	approved := 0
	for inviteID := range inviteIDs {
		joinerAddr, err := CheckJoinRequest(ctx, s.store.backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := IsGranted(ctx, s.store.backend, inviteID)
		if granted {
			if s.joinerHasAllKeys(ctx, joinerAddr) {
				continue
			}
		}
		if err := ApproveJoin(ctx, s.store.backend, s.store.identity, joinerAddr, inviteID); err != nil {
			continue
		}
		// Register the joiner as a device
		approved++
		// Don't cleanup — joiner needs to poll and see the granted marker
	}

	return map[string]int{"approved": approved}, nil
}

// autoApproveLoop polls for pending join requests and approves them automatically.
// The invite code itself is the authorization — no manual step needed.
func (s *RPCServer) autoApproveLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	// Run once immediately on startup
	s.tryAutoApprove(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tryAutoApprove(ctx)
		}
	}
}

func (s *RPCServer) tryAutoApprove(ctx context.Context) {
	inviteKeys, err := s.store.backend.List(ctx, "invites/")
	if err != nil {
		return
	}

	inviteIDs := make(map[string]bool)
	for _, k := range inviteKeys {
		if id := splitInvitePath2(k); id != "" {
			inviteIDs[id] = true
		}
	}

	for inviteID := range inviteIDs {
		joinerAddr, err := CheckJoinRequest(ctx, s.store.backend, inviteID)
		if err != nil || joinerAddr == "" {
			continue
		}
		granted, _ := IsGranted(ctx, s.store.backend, inviteID)
		if granted {
			// Check if ALL namespace keys were wrapped for the joiner.
			// A partial approve (crash, or new namespace created after join)
			// needs to be retried.
			if s.joinerHasAllKeys(ctx, joinerAddr) {
				continue
			}
		}
		if err := ApproveJoin(ctx, s.store.backend, s.store.identity, joinerAddr, inviteID); err != nil {
			s.logger.Warn("auto-approve failed", "invite", inviteID, "error", err)
			continue
		}
		s.logger.Info("auto-approved device", "address", joinerAddr)
	}
}

// joinerHasAllKeys checks if the joiner has a wrapped key for every
// namespace that this device (the approver) has access to.
func (s *RPCServer) joinerHasAllKeys(ctx context.Context, joinerAddr string) bool {
	joinerID := shortPubkeyID(joinerAddr)
	myID := shortPubkeyID(s.store.identity.Address())

	allKeys, err := s.store.backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return false
	}

	// Find namespaces we have access to (our device-specific key or the base key)
	myNamespaces := make(map[string]bool)
	for _, k := range allKeys {
		ns := extractNamespaceName(k)
		// Check if we can unwrap this key (it's ours)
		if strings.Contains(k, "."+myID+".") || strings.HasSuffix(k, ns+".ns.enc") {
			myNamespaces[ns] = true
		}
	}

	// Check joiner has a key for each namespace
	for ns := range myNamespaces {
		joinerKeyPath := "keys/namespaces/" + ns + "." + joinerID + ".ns.enc"
		if _, err := s.store.backend.Head(ctx, joinerKeyPath); err != nil {
			return false
		}
	}

	return true
}

func splitInvitePath2(key string) string {
	if len(key) < 9 || key[:8] != "invites/" {
		return ""
	}
	rest := key[8:]
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return ""
}
