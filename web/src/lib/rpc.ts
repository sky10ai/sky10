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
};

// -- wallet --
export const wallet = {
  status: () => rpc<WalletStatus>("wallet.status"),
  install: () => rpc<{ status: string }>("wallet.install"),
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
}

export interface LinkStatus {
  peer_id: string;
  address: string;
  mode: string;
  addrs: string[];
  peers: number;
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
}

// -- Wallet types --

export interface WalletStatus {
  installed: boolean;
  wallets: number;
  version?: string;
  bin_path?: string;
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
