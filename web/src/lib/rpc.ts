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
  deviceList: () => rpc<DeviceListResult>("skyfs.deviceList"),
  remove: (p: { drive: string; path: string }) =>
    rpc<{ status: string }>("skyfs.remove", p),
  mkdir: (p: { drive: string; path: string }) =>
    rpc<{ status: string }>("skyfs.mkdir", p),
  invite: () => rpc<InviteResult>("skyfs.invite"),
  approve: (p: { device_id: string }) => rpc("skyfs.approve", p),
  status: () => rpc<StatusResult>("skyfs.status"),
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
  pubkey: string;
  device_pubkey?: string;
  name: string;
  alias?: string;
  joined: string;
  platform: string;
  ip: string;
  location: string;
  version: string;
  last_seen: string;
  multiaddrs?: string[];
}

export interface DeviceListResult {
  devices: Device[];
  this_device: string;
}

export interface InviteResult {
  code: string;
  expires: string;
}

export interface StatusResult {
  syncing: boolean;
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
  device_address: string;
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
