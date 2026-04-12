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
  list: (p?: { prefix?: string; namespace?: string }) =>
    rpc<KVListResult>("skykv.list", p),
  getAll: (p?: { prefix?: string; namespace?: string }) =>
    rpc<KVGetAllResult>("skykv.getAll", p),
  get: (p: { key: string; namespace?: string }) =>
    rpc<KVGetResult>("skykv.get", p),
  set: (p: { key: string; value: string; namespace?: string }) =>
    rpc("skykv.set", p),
  delete: (p: { key: string; namespace?: string }) =>
    rpc("skykv.delete", p),
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
  address: (p: { wallet: string }) => rpc<WalletAddress>("wallet.address", p),
  balance: (p: { wallet: string }) => rpc<WalletBalance>("wallet.balance", p),
  deposit: (p: { wallet: string }) => rpc<WalletDeposit>("wallet.deposit", p),
  transfer: (p: { wallet: string; to: string; amount: string; token?: string }) =>
    rpc<WalletTransfer>("wallet.transfer", p),
  maxTransfer: (p: { wallet: string }) =>
    rpc<{ max: string; fee: string }>("wallet.maxTransfer", p),
};

// ---- Types matching actual daemon responses ----

export interface HealthResult {
  status: string;
  version: string;
  uptime: string;
  drives: number;
  drives_running: number;
  outbox_pending: number;
  last_activity_ago: string;
  rpc_clients: number;
  rpc_subscribers: number;
  http_addr?: string;
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
  active_peer_ids?: string[];
  active_addrs?: string[];
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

export interface SyncActivityEntry {
  direction: string;
  op: string;
  path: string;
  drive_id: string;
  drive_name: string;
  ts: number;
}

export interface SyncActivityResult {
  pending: SyncActivityEntry[];
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
