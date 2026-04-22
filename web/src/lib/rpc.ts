/** JSON-RPC 2.0 client for the sky10 daemon. */

interface RPCRequest {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
  id: number;
}

interface RPCResponse<T = unknown> {
  jsonrpc: "2.0";
  result?: T;
  error?: { code: number; message: string };
  id: number;
}

let nextID = 1;

const RPC_URL = "/rpc";

export class RPCError extends Error {
  code: number;
  constructor(code: number, message: string) {
    super(message);
    this.name = "RPCError";
    this.code = code;
  }
}

function isUnknownMethodError(error: unknown): error is RPCError {
  return error instanceof RPCError && error.message.startsWith("unknown method:");
}

export async function rpc<T = unknown>(
  method: string,
  params?: unknown
): Promise<T> {
  const id = nextID++;
  const body: RPCRequest = { jsonrpc: "2.0", method, id, params };

  const res = await fetch(RPC_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    throw new RPCError(-1, `HTTP ${res.status}: ${res.statusText}`);
  }

  const data: RPCResponse<T> = await res.json();
  if (data.error) {
    throw new RPCError(data.error.code, data.error.message);
  }
  return data.result as T;
}

// -- skyfs --
export const skyfs = {
  health: () => rpc<HealthResult>("skyfs.health"),
  driveList: () => rpc<DriveListResult>("skyfs.driveList"),
  driveCreate: (p: { name: string; path: string }) =>
    rpc("skyfs.driveCreate", p),
  driveStart: (p: { name: string }) => rpc("skyfs.driveStart", p),
  driveStop: (p: { name: string }) => rpc("skyfs.driveStop", p),
  list: (p?: { prefix?: string }) =>
    rpc<FileListResult>("skyfs.list", p),
  syncStatus: (p: { drive: string }) =>
    rpc<SyncStatus>("skyfs.syncStatus", p),
  remove: (p: { drive: string; path: string }) =>
    rpc<{ status: string }>("skyfs.remove", p),
  mkdir: (p: { drive: string; path: string }) =>
    rpc<{ status: string }>("skyfs.mkdir", p),
  status: () => rpc<StatusResult>("skyfs.status"),
  s3List: (p: { prefix: string }) => rpc<S3ListResult>("skyfs.s3List", p),
  s3Delete: (p: { key: string }) => rpc("skyfs.s3Delete", p),
  syncActivity: () => rpc<SyncActivityResult>("skyfs.syncActivity"),
  driveRemove: (p: { name: string }) => rpc("skyfs.driveRemove", p),
};

// -- skykv --
export const skykv = {
  list: (p?: { prefix?: string; namespace?: string; include_internal?: boolean }) =>
    rpc<KVListResult>("skykv.list", p),
  getAll: (p?: { prefix?: string; namespace?: string; include_internal?: boolean }) =>
    rpc<KVGetAllResult>("skykv.getAll", p),
  get: (p: { key: string; namespace?: string }) =>
    rpc<KVGetResult>("skykv.get", p),
  set: (p: { key: string; value: string; namespace?: string }) =>
    rpc("skykv.set", p),
  delete: (p: { key: string; namespace?: string }) =>
    rpc("skykv.delete", p),
  deleteMatching: (p: { pattern: string; dry_run?: boolean; include_internal?: boolean }) =>
    rpc<KVDeleteMatchingResult>("skykv.deleteMatching", p),
  status: () => rpc<KVStatus>("skykv.status"),
};

// -- skylink --
export const skylink = {
  status: () => rpc<LinkStatus>("skylink.status"),
  peers: () => rpc<PeersResult>("skylink.peers"),
  connect: (p: { address: string }) => rpc("skylink.connect", p),
};

// -- identity --
export const identity = {
  show: () => rpc<IdentityShow>("identity.show"),
  devices: () => rpc<IdentityDevices>("identity.devices"),
  deviceList: () => rpc<DeviceListResult>("identity.deviceList"),
  invite: () => rpc<InviteResult>("identity.invite"),
  join: (p: { code: string }) => rpc<IdentityJoinResult>("identity.join", p),
  approve: () => rpc<{ approved: number }>("identity.approve"),
  deviceRemove: (p: { pubkey: string }) => rpc("identity.deviceRemove", p),
};

// -- secrets --
export const secrets = {
  list: () => rpc<SecretsListResult>("secrets.list"),
  devices: () => rpc<SecretsDevicesResult>("secrets.devices"),
  status: () => rpc<SecretsStatus>("secrets.status"),
  sync: () => rpc<{ status: string }>("secrets.sync"),
  delete: (p: { id_or_name: string }) => rpc<{ status: string }>("secrets.delete", p),
  get: (p: { id_or_name: string }) => rpc<SecretRecord>("secrets.get", p),
  put: (p: {
    name: string;
    kind?: string;
    content_type?: string;
    scope?: string;
    payload: string;
    recipient_devices?: string[];
  }) => rpc<SecretSummary>("secrets.put", p),
  rewrap: (p: {
    id_or_name: string;
    scope?: string;
    recipient_devices?: string[];
  }) => rpc<SecretSummary>("secrets.rewrap", p),
};

// -- agent --
export const agent = {
  list: () => rpc<AgentListResult>("agent.list"),
  status: () => rpc<AgentStatus>("agent.status"),
  send: (p: AgentSendParams) => rpc<AgentSendResult>("agent.send", p),
  mailbox: {
    views: () => rpc<MailboxViewListResult>("agent.mailbox.views"),
    send: (p: MailboxSendParams) =>
      rpc<MailboxRecordResult>("agent.mailbox.send", p),
    listInbox: (p?: MailboxListParams) =>
      rpc<MailboxListResult>("agent.mailbox.listInbox", p),
    listOutbox: (p?: MailboxListParams) =>
      rpc<MailboxListResult>("agent.mailbox.listOutbox", p),
    listQueue: (p?: MailboxListParams) =>
      rpc<MailboxListResult>("agent.mailbox.listQueue", p),
    listFailed: (p?: MailboxListParams) =>
      rpc<MailboxListResult>("agent.mailbox.listFailed", p),
    listSent: (p?: MailboxListParams) =>
      rpc<MailboxListResult>("agent.mailbox.listSent", p),
    get: (p: MailboxGetParams) =>
      rpc<MailboxGetResult>("agent.mailbox.get", p),
    claim: (p: MailboxActionParams) =>
      rpc<MailboxActionResult>("agent.mailbox.claim", p),
    release: (p: MailboxActionParams) =>
      rpc<MailboxActionResult>("agent.mailbox.release", p),
    ack: (p: MailboxActionParams) =>
      rpc<MailboxRecordResult>("agent.mailbox.ack", p),
    approve: (p: MailboxActionParams) =>
      rpc<MailboxRecordResult>("agent.mailbox.approve", p),
    reject: (p: MailboxActionParams) =>
      rpc<MailboxRecordResult>("agent.mailbox.reject", p),
    complete: (p: MailboxActionParams) =>
      rpc<MailboxRecordResult>("agent.mailbox.complete", p),
    retry: (p: MailboxRetryParams) =>
      rpc<MailboxRecordResult>("agent.mailbox.retry", p),
  },
};

// -- sandbox --
export const sandbox = {
  list: () => rpc<SandboxListResult>("sandbox.list"),
  get: (p: { name?: string; slug?: string }) => rpc<SandboxRecord>("sandbox.get", p),
  logs: (p: { name?: string; slug?: string; limit?: number }) =>
    rpc<SandboxLogsResult>("sandbox.logs", p),
  create: (p: { name: string; provider: string; template: string }) =>
    rpc<SandboxRecord>("sandbox.create", p),
  start: (p: { name?: string; slug?: string }) => rpc<SandboxRecord>("sandbox.start", p),
  stop: (p: { name?: string; slug?: string }) => rpc<SandboxRecord>("sandbox.stop", p),
  delete: (p: { name?: string; slug?: string }) => rpc<SandboxRecord>("sandbox.delete", p),
};

export function sandboxTerminalURL(slug: string) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/rpc/sandboxes/${encodeURIComponent(slug)}/terminal`;
}

export function agentChatWebSocketURL(agentID: string, sessionID: string) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const agent = encodeURIComponent(agentID);
  const session = encodeURIComponent(sessionID);
  return `${protocol}//${window.location.host}/rpc/agents/${agent}/chat?session_id=${session}`;
}

export function guestAgentChatWebSocketURL(address: string, agentID: string, sessionID: string) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const agent = encodeURIComponent(agentID);
  const session = encodeURIComponent(sessionID);
  return `${protocol}//${address}:9101/rpc/agents/${agent}/chat?session_id=${session}`;
}

// -- apps --
export const apps = {
  status: (p: { id: string }) => rpc<AppStatus>("apps.status", p),
  install: (p: { id: string }) => rpc<{ id: string; status: string }>("apps.install", p),
  checkUpdate: (p: { id: string }) => rpc<AppReleaseInfo>("apps.checkUpdate", p),
};

// -- system --
export const system = {
  restart: () => rpc<{ status: string }>("system.restart"),
  update: {
    run: () => rpc<{ status: string }>("system.update"),
    check: async () => {
      try {
        return await rpc<SystemUpdateInfo>("system.update.check");
      } catch (error) {
        if (!isUnknownMethodError(error)) throw error;
        return rpc<SystemUpdateInfo>("system.checkUpdate");
      }
    },
    status: async () => {
      try {
        const status = await rpc<SystemUpdateStatus>("system.update.status");
        return { ...status, mode: "staged" as const };
      } catch (error) {
        if (!isUnknownMethodError(error)) throw error;
        const info = await rpc<SystemUpdateInfo>("system.checkUpdate");
        return {
          current: info.current,
          ready: false,
          latest: info.latest || undefined,
          cli_staged: false,
          menu_staged: false,
          mode: "legacy" as const,
        };
      }
    },
    download: () => rpc<{ status: string }>("system.update.download"),
    install: () => rpc<SystemInstallUpdateResult>("system.update.install"),
  },
};

// -- wallet --
export const wallet = {
  status: () => rpc<WalletStatus>("wallet.status"),
  install: () => rpc<{ status: string }>("wallet.install"),
  uninstall: () => rpc<WalletUninstallResult>("wallet.uninstall"),
  checkUpdate: () => rpc<WalletReleaseInfo>("wallet.checkUpdate"),
  create: (p: { name: string }) => rpc<WalletInfo>("wallet.create", p),
  list: () => rpc<WalletListResult>("wallet.list"),
  address: (p: { wallet: string; chain?: string }) => rpc<WalletAddress>("wallet.address", p),
  balance: (p: { wallet: string; chain?: string }) => rpc<WalletBalance>("wallet.balance", p),
  deposit: (p: { wallet: string; chain?: string }) => rpc<WalletDeposit>("wallet.deposit", p),
  transfer: (p: { wallet: string; chain?: string; to: string; amount: string; token?: string }) =>
    rpc<WalletTransfer>("wallet.transfer", p),
  maxTransfer: (p: { wallet: string; chain?: string }) =>
    rpc<{ max: string; fee: string }>("wallet.maxTransfer", p),
};

// -- codex --
export const codex = {
  status: () => rpc<CodexStatus>("codex.status"),
  loginStart: () => rpc<CodexStatus>("codex.loginStart"),
  loginComplete: (p: { authorization_input: string }) =>
    rpc<CodexStatus>("codex.loginComplete", p),
  loginCancel: () => rpc<CodexStatus>("codex.loginCancel"),
  logout: () => rpc<CodexStatus>("codex.logout"),
  chat: (p: {
    model?: string;
    system_prompt?: string;
    messages: CodexChatMessage[];
  }) => rpc<CodexChatResult>("codex.chat", p),
};

// ---- Types matching actual daemon responses ----

export interface HealthResult {
  status: string;
  version: string;
  uptime: string;
  drives: number;
  drives_running: number;
  outbox_pending: number;
  transfer_pending: number;
  transfer_staged: number;
  read_local_hits: number;
  read_peer_hits: number;
  read_s3_hits: number;
  fs_peer_count: number;
  sync_ready_drives: number;
  sync_waiting_drives: number;
  sync_error_drives: number;
  path_issue_drives: number;
  path_issue_count: number;
  conflict_drives: number;
  conflict_files: number;
  peer_degraded_drives: number;
  s3_degraded_drives: number;
  peer_source_failures: number;
  s3_source_failures: number;
  last_activity_ago: string;
  rpc_clients: number;
  rpc_subscribers: number;
  http_addr?: string;
}

export interface ChunkSourceHealthSnapshot {
  consecutive_failures?: number;
  degraded?: boolean;
  degraded_until?: number;
  last_success_at?: number;
  last_error_at?: number;
  last_error?: string;
}

export interface Drive {
  id: string;
  name: string;
  local_path: string;
  namespace: string;
  enabled: boolean;
  running: boolean;
  snapshot_files: number;
  outbox_pending: number;
  transfer_pending: number;
  transfer_staged: number;
  read_local_hits: number;
  read_peer_hits: number;
  read_s3_hits: number;
  last_read_source?: string;
  last_read_at?: number;
  peer_source_health?: ChunkSourceHealthSnapshot;
  s3_source_health?: ChunkSourceHealthSnapshot;
  sync_ready?: boolean;
  peer_count?: number;
  sync_state?: string;
  sync_message?: string;
  path_issue_count?: number;
  path_issue_message?: string;
  last_sync_ok?: number;
  last_sync_peer?: string;
  last_sync_error?: string;
  last_sync_error_at?: number;
  conflict_files?: number;
}

export interface DriveListResult {
  drives: Drive[];
}

export interface FileEntry {
  path: string;
  size: number;
  modified: string;
  checksum: string;
  namespace: string;
  chunks: number;
}

export interface DirectoryEntry {
  path: string;
  namespace: string;
}

export interface FileListResult {
  files: FileEntry[];
  dirs: DirectoryEntry[];
}

export interface SyncStatus {
  sync_dir: string;
  syncing: boolean;
}

export interface Device {
  id: string;
  pubkey: string;
  name: string;
  alias?: string;
  joined: string;
  platform: string;
  ip: string;
  location: string;
  version: string;
  last_seen: string;
  multiaddrs?: string[];
  current?: boolean;
}

export interface DeviceListResult {
  identity?: string;
  devices: Device[];
  this_device: string;
}

export interface InviteResult {
  code: string;
  expires: string;
}

export interface IdentityJoinResult {
  status: string;
  identity: string;
  device_id: string;
  device_pubkey: string;
  restarting: boolean;
}

export interface StatusResult {
  syncing: boolean;
}

export interface S3ObjectEntry {
  key: string;
  size: number;
}

export interface S3ListResult {
  files: S3ObjectEntry[];
  dirs: string[];
  prefix: string;
  total: number;
}

export interface KVListResult {
  count: number;
  keys: string[];
}

export interface KVGetAllResult {
  count: number;
  entries: Record<string, string>;
}

export interface KVGetResult {
  key: string;
  value: string;
  found: boolean;
}

export interface KVStatus {
  namespace: string;
  device_id: string;
  keys: number;
  nsid?: string;
  ready: boolean;
  peer_count: number;
  expected_peers: number;
  sync_state: "ok" | "waiting" | "error";
  sync_message?: string;
  last_sync_ok?: string;
  last_sync_peer?: string;
  last_sync_error?: string;
  last_sync_error_at?: string;
}

export interface KVDeleteMatchingResult {
  pattern: string;
  keys: string[];
  count: number;
  dry_run: boolean;
}

export interface LinkStatus {
  peer_id: string;
  address: string;
  mode: string;
  addrs: string[];
  peers: number;
  private_peers: number;
  health: LinkNetworkHealth;
}

export interface LinkHealthEvent {
  type: string;
  status: string;
  detail?: string;
  at: string;
}

export interface LinkNetcheckResult {
  checked_at: string;
  udp: boolean;
  public_addr?: string;
  preferred_server?: string;
  mapping_varies_by_server?: boolean;
  probes: Array<{
    server: string;
    public_addr?: string;
    latency_ms?: number;
    error?: string;
  }>;
}

export interface LinkMailboxHealth {
  queued: number;
  failed: number;
  handed_off: number;
  pending_private: number;
  pending_sky10_network: number;
  last_handoff_at?: string;
  last_delivered_at?: string;
  last_failure_at?: string;
}

export interface LinkLiveRelayHealth {
  configured_peers: number;
  cached_peers: number;
  active_peers: number;
  current_peer_id?: string;
  preferred_peer_id?: string;
  active_peer_ids?: string[];
  active_addrs?: string[];
  preferred_at?: string;
  last_switch_at?: string;
  last_bootstrap_at?: string;
}

export interface LinkRelayHealth {
  url: string;
  successes: number;
  failures: number;
  last_success_at?: string;
  last_failure_at?: string;
  last_error?: string;
  last_latency_ms?: number;
  average_latency_ms?: number;
  active_subscriptions?: number;
  last_subscription_at?: string;
  last_subscription_error_at?: string;
  last_subscription_error?: string;
}

export interface LinkNostrPublishOutcome {
  operation?: string;
  attempts: number;
  successes: number;
  quorum: number;
  degraded?: boolean;
  at?: string;
}

export interface LinkNostrCoordinationHealth {
  configured_relays: number;
  last_publish: LinkNostrPublishOutcome;
  subscriptions?: LinkNostrSubscriptionHealth[];
}

export interface LinkNostrSubscriptionHealth {
  label: string;
  active_relays: number;
  required_relays: number;
  last_connect_at?: string;
  last_event_at?: string;
  last_disconnect_at?: string;
  last_error?: string;
}

export interface LinkNetworkHealth {
  preferred_transport: string;
  transport_degraded_reason?: string;
  delivery_degraded_reason?: string;
  coordination_degraded_reason?: string;
  reachability?: string;
  public_addr?: string;
  mapping_varies_by_server?: boolean;
  connected_private_peers: number;
  last_published_at?: string;
  last_address_change_at?: string;
  netcheck: LinkNetcheckResult;
  live_relay: LinkLiveRelayHealth;
  mailbox: LinkMailboxHealth;
  nostr: LinkNostrCoordinationHealth;
  relays?: LinkRelayHealth[];
  events?: LinkHealthEvent[];
}

export interface Peer {
  peer_id: string;
  address: string;
}

export interface PeersResult {
  peers: Peer[];
  count: number;
}

export interface IdentityShow {
  address: string;
  device_id: string;
  device_pubkey: string;
  device_count: number;
}

export interface IdentityDevice {
  public_key: string;
  name: string;
  added_at: string;
  current: boolean;
}

export interface IdentityDevices {
  identity: string;
  devices: IdentityDevice[];
}

export interface SecretSummary {
  id: string;
  name: string;
  kind: string;
  content_type: string;
  scope: "current" | "trusted" | "explicit";
  size: number;
  sha256: string;
  created_at: string;
  updated_at: string;
  recipient_device_ids: string[];
}

export interface SecretRecord extends SecretSummary {
  version_id: string;
  payload: string;
}

export interface SecretDevice {
  id: string;
  name: string;
  role: "trusted" | "sandbox";
  current: boolean;
}

export interface SecretsListResult {
  items: SecretSummary[];
  count: number;
}

export interface SecretsDevicesResult {
  devices: SecretDevice[];
  count: number;
}

export interface SecretsStatus {
  namespace: string;
  device_id: string;
  count: number;
}

export interface SyncActivityEntry {
  direction: string;
  op: string;
  phase?: string;
  path: string;
  drive_id: string;
  drive_name: string;
  bytes_done?: number;
  bytes_total?: number;
  active_source?: string;
  ts: number;
}

export interface SyncReadSourceEntry {
  drive_id: string;
  drive_name: string;
  read_local_hits: number;
  read_peer_hits: number;
  read_s3_hits: number;
  last_read_source?: string;
  last_read_at?: number;
  peer_source_health?: ChunkSourceHealthSnapshot;
  s3_source_health?: ChunkSourceHealthSnapshot;
}

export interface SyncConflictEntry {
  drive_id: string;
  drive_name: string;
  path: string;
  ts?: number;
}

export interface SyncPathIssueEntry {
  drive_id: string;
  drive_name: string;
  kind: string;
  paths: string[];
  reason: string;
}

export interface SyncActivityResult {
  pending: SyncActivityEntry[];
  reads: SyncReadSourceEntry[];
  conflicts: SyncConflictEntry[];
  path_issues: SyncPathIssueEntry[];
}

export interface AgentInfo {
  id: string;
  name: string;
  device_id: string;
  device_name: string;
  skills: string[];
  status: string;
  connected_at: string;
}

export interface AgentListResult {
  agents: AgentInfo[];
  count: number;
}

export interface AgentStatus {
  agents: number;
  skills: string[];
  delivery_policies: Record<string, DeliveryPolicyDescription>;
}

export interface ChatContentSource {
  type: string;
  data?: string;
  url?: string;
  filename?: string;
  media_type?: string;
}

export interface ChatContentPart {
  type: string;
  text?: string;
  source?: ChatContentSource | null;
  filename?: string;
  media_type?: string;
  caption?: string;
}

export interface ChatContent {
  text?: string;
  parts?: ChatContentPart[];
}

export interface AgentSendParams {
  to: string;
  device_id?: string;
  session_id: string;
  type: string;
  content: unknown;
}

export interface AgentSendResult {
  id: string;
  status: string;
  mailbox_item_id?: string;
  delivery: DeliveryMetadata;
}

export interface DeliveryMetadata {
  policy: string;
  scope?: string;
  status: string;
  live_transport?: string;
  durable_transport?: string;
  last_transport?: string;
  mailbox_item_id?: string;
  mailbox_state?: string;
  last_event?: string;
  last_error?: string;
  live_attempted: boolean;
  durable_used: boolean;
}

export interface DeliveryPolicyDescription {
  policy: string;
  scope?: string;
  live_transport?: string;
  durable_transport?: string;
  description: string;
}

export interface MailboxPayloadRef {
  kind: string;
  key: string;
  size: number;
  digest?: string;
}

export interface MailboxPrincipal {
  id: string;
  kind: string;
  scope: string;
  device_hint?: string;
  route_hint?: string;
}

export interface MailboxItem {
  id: string;
  kind: string;
  from: MailboxPrincipal;
  to?: MailboxPrincipal;
  target_skill?: string;
  session_id?: string;
  request_id?: string;
  reply_to?: string;
  idempotency_key?: string;
  payload_ref?: MailboxPayloadRef;
  payload_inline?: unknown;
  priority?: string;
  expires_at?: string;
  created_at: string;
}

export interface MailboxEvent {
  item_id: string;
  event_id?: string;
  type: string;
  actor: MailboxPrincipal;
  lease_id?: string;
  error?: string;
  timestamp?: string;
  meta?: Record<string, string>;
}

export interface MailboxClaim {
  queue: string;
  item_id: string;
  holder: string;
  token: string;
  acquired_at: string;
  expires_at: string;
}

export interface MailboxRecord {
  item: MailboxItem;
  events: MailboxEvent[];
  claim?: MailboxClaim;
  state: string;
}

export interface MailboxView {
  view_id: string;
  label: string;
  role: string;
  principal: MailboxPrincipal;
  skills?: string[];
}

export interface MailboxViewListResult {
  views: MailboxView[];
  count: number;
  default_view_id: string;
}

export interface MailboxListResult {
  items: MailboxRecord[];
  count: number;
}

export interface MailboxGetResult {
  item: MailboxRecord;
  found: boolean;
  delivery: DeliveryMetadata;
}

export interface MailboxRecordResult {
  item: MailboxRecord;
  delivery: DeliveryMetadata;
}

export interface MailboxActionResult {
  item: MailboxRecord;
  delivery?: DeliveryMetadata;
  claimed?: boolean;
  released?: boolean;
}

export interface MailboxPrincipalParams {
  id: string;
  kind?: string;
  scope?: string;
  device_hint?: string;
  route_hint?: string;
}

export interface MailboxListParams {
  principal_id?: string;
  principal_kind?: string;
  queue?: string;
  request_id?: string;
  reply_to?: string;
}

export interface MailboxGetParams {
  item_id: string;
  principal_id?: string;
  principal_kind?: string;
}

export interface MailboxSendParams {
  kind: string;
  from?: MailboxPrincipalParams;
  to?: MailboxPrincipalParams;
  target_skill?: string;
  session_id?: string;
  request_id?: string;
  reply_to?: string;
  idempotency_key?: string;
  priority?: string;
  expires_at?: string;
  payload?: unknown;
}

export interface MailboxActionParams {
  item_id: string;
  actor_id?: string;
  actor_kind?: string;
  token?: string;
  decision_id?: string;
  ttl_seconds?: number;
}

export interface MailboxRetryParams {
  item_id: string;
  actor_id?: string;
  actor_kind?: string;
}

export interface SandboxRecord {
  name: string;
  slug: string;
  provider: string;
  template: string;
  status: string;
  vm_status?: string;
  shared_dir?: string;
  ip_address?: string;
  shell?: string;
  last_error?: string;
  progress?: SandboxProgress;
  guest_device_id?: string;
  guest_device_pubkey?: string;
  created_at: string;
  updated_at: string;
  last_log_at?: string;
}

export interface SandboxProgress {
  step_id?: string;
  summary?: string;
  percent: number;
}

export interface SandboxListResult {
  sandboxes: SandboxRecord[];
}

export interface SandboxLogEntry {
  time: string;
  stream: string;
  line: string;
}

export interface SandboxLogsResult {
  name: string;
  slug: string;
  entries: SandboxLogEntry[];
}

// -- System update types --

export interface SystemUpdateInfo {
  current: string;
  latest: string;
  available: boolean;
  cli_available: boolean;
  menu_available: boolean;
  asset_url?: string;
  menu_asset_url?: string;
  menu_checksums_url?: string;
}

export interface SystemUpdateStatus {
  current: string;
  ready: boolean;
  latest?: string;
  cli_staged: boolean;
  menu_staged: boolean;
  mode?: "staged" | "legacy";
}

export interface SystemInstallUpdateResult {
  status: "updated" | "restarting" | "restart_required";
  current: string;
  latest: string;
  cli_staged: boolean;
  menu_staged: boolean;
  restarting: boolean;
}

// -- Managed app types --

export interface AppStatus {
  id: string;
  name: string;
  installed: boolean;
  managed: boolean;
  managed_path?: string;
  active_path?: string;
  version?: string;
}

export interface AppReleaseInfo {
  id: string;
  installed: boolean;
  current?: string;
  latest?: string;
  available: boolean;
  asset_url?: string;
  extra_asset_urls?: string[];
}

// -- Wallet types --

export interface WalletStatus {
  installed: boolean;
  managed: boolean;
  managed_path?: string;
  wallets: number;
  version?: string;
  bin_path?: string;
}

export interface WalletUninstallResult {
  path: string;
  removed: boolean;
}

export interface WalletReleaseInfo {
  installed: boolean;
  current?: string;
  latest?: string;
  available: boolean;
  asset_url?: string;
}

export interface WalletInfo {
  id: string;
  name: string;
}

export interface WalletListResult {
  wallets: WalletInfo[];
  count: number;
}

export interface WalletAddress {
  wallet: string;
  chain: string;
  address: string;
}

export interface TokenBalance {
  symbol: string;
  balance: string;
  mint?: string;
}

export interface WalletBalance {
  address: string;
  chain: string;
  tokens: TokenBalance[];
}

export interface WalletDeposit {
  address: string;
  chain: string;
  url?: string;
  status: string;
}

export interface WalletTransfer {
  transaction_hash?: string;
  status: string;
  amount?: string;
}

export interface CodexPendingLogin {
  id: string;
  mode?: string;
  verification_url: string;
  redirect_uri?: string;
  callback_listening?: boolean;
  user_code?: string;
  started_at: string;
  expires_at: string;
}

export interface CodexStatus {
  installed: boolean;
  linked: boolean;
  auth_mode?: string;
  auth_label?: string;
  auth_source?: string;
  email?: string;
  account_id?: string;
  pending_login?: CodexPendingLogin;
  last_error?: string;
}

export interface CodexChatMessage {
  role: "assistant" | "user";
  content: string;
}

export interface CodexChatUsage {
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
}

export interface CodexChatResult {
  model: string;
  response_id?: string;
  text: string;
  usage?: CodexChatUsage;
}
