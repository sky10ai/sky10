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

// Typed wrappers for each RPC namespace.

// -- skyfs --
export const skyfs = {
  health: () => rpc<HealthResult>("skyfs.health"),
  driveList: () => rpc<Drive[]>("skyfs.driveList"),
  driveCreate: (p: { name: string; path: string }) =>
    rpc("skyfs.driveCreate", p),
  driveStart: (p: { name: string }) => rpc("skyfs.driveStart", p),
  driveStop: (p: { name: string }) => rpc("skyfs.driveStop", p),
  list: (p: { drive: string; path?: string }) =>
    rpc<FileEntry[]>("skyfs.list", p),
  syncStatus: (p: { drive: string }) =>
    rpc<SyncStatus>("skyfs.syncStatus", p),
  deviceList: () => rpc<Device[]>("skyfs.deviceList"),
  invite: () => rpc<InviteResult>("skyfs.invite"),
  approve: (p: { device_id: string }) => rpc("skyfs.approve", p),
  status: () => rpc<StatusResult>("skyfs.status"),
};

// -- skykv --
export const skykv = {
  list: (p?: { prefix?: string; namespace?: string }) =>
    rpc<KVEntry[]>("skykv.list", p),
  get: (p: { key: string; namespace?: string }) =>
    rpc<KVEntry>("skykv.get", p),
  set: (p: { key: string; value: string; namespace?: string }) =>
    rpc("skykv.set", p),
  delete: (p: { key: string; namespace?: string }) =>
    rpc("skykv.delete", p),
  status: () => rpc<KVStatus>("skykv.status"),
};

// -- skylink --
export const skylink = {
  status: () => rpc<LinkStatus>("skylink.status"),
  peers: () => rpc<Peer[]>("skylink.peers"),
  connect: (p: { address: string }) => rpc("skylink.connect", p),
};

// Types

export interface HealthResult {
  status: string;
  version: string;
  uptime: string;
  drives: number;
  drives_running: number;
  http_addr: string;
}

export interface Drive {
  name: string;
  path: string;
  running: boolean;
  file_count?: number;
  total_size?: number;
  last_activity?: string;
}

export interface FileEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  modified: string;
  synced: boolean;
  device?: string;
}

export interface SyncStatus {
  state: string;
  pending_up: number;
  pending_down: number;
  progress?: number;
  last_sync?: string;
}

export interface Device {
  device_id: string;
  hostname: string;
  platform: string;
  address: string;
  last_seen: string;
  version: string;
  location?: string;
  is_self?: boolean;
}

export interface InviteResult {
  code: string;
  expires: string;
}

export interface StatusResult {
  daemon_running: boolean;
  drives: Drive[];
  device_count: number;
  connected_peers: number;
}

export interface KVEntry {
  key: string;
  value: string;
  namespace: string;
  modified?: string;
  device?: string;
  size?: number;
}

export interface KVStatus {
  namespaces: string[];
  total_keys: number;
  synced: boolean;
}

export interface LinkStatus {
  peer_id: string;
  mode: string;
  listen_addrs: string[];
  connected_peers: number;
  uptime: string;
}

export interface Peer {
  peer_id: string;
  device_name: string;
  address: string;
  connection_type: string;
  latency_ms: number;
  connected_since: string;
}
