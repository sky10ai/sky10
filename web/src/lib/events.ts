/** SSE client for real-time daemon events. */

export type EventHandler = (event: string, data: unknown) => void;

const SSE_URL = "/rpc/events";

export const STORAGE_EVENT_TYPES = [
  "file.changed",
  "state.changed",
  "sync.active",
  "poll.complete",
  "download.start",
  "download.progress",
  "upload.start",
  "upload.complete",
  "sync.complete",
  "snapshot.uploaded",
] as const;

export const KV_EVENT_TYPES = [
  "kv.poll.complete",
  "kv.snapshot.uploaded",
] as const;

export const LINK_EVENT_TYPES = [
  "device.connected",
  "device.disconnected",
  "link.peer.connected",
  "link.peer.disconnected",
] as const;

export const AGENT_EVENT_TYPES = [
  "agent.connected",
  "agent.disconnected",
  "agent:connected",
  "agent:disconnected",
  "agent.message",
  "agent.mailbox.updated",
  "agent.mailbox.claimed",
  "agent.mailbox.completed",
] as const;

export const WALLET_EVENT_TYPES = [
  "wallet:install:progress",
  "wallet:install:complete",
  "wallet:install:error",
] as const;

export const UPDATE_EVENT_TYPES = [
  "update:available",
  "update:progress",
  "update:complete",
  "update:error",
  "update:download:progress",
  "update:download:complete",
  "update:download:error",
  "update:install:complete",
  "update:install:error",
] as const;

export const SANDBOX_STATE_EVENT_TYPES = [
  "sandbox:state",
] as const;

export const SANDBOX_LOG_EVENT_TYPES = [
  "sandbox:log",
] as const;

export const SANDBOX_EVENT_TYPES = [
  ...SANDBOX_STATE_EVENT_TYPES,
  ...SANDBOX_LOG_EVENT_TYPES,
] as const;

export const LEGACY_EVENT_TYPES = [
  "sync.progress",
  "sync.complete",
  "sync.error",
  "drive.started",
  "drive.stopped",
  "kv.updated",
] as const;

export const KNOWN_EVENT_TYPES = [
  ...new Set([
    ...STORAGE_EVENT_TYPES,
    ...KV_EVENT_TYPES,
    ...LINK_EVENT_TYPES,
    ...AGENT_EVENT_TYPES,
    ...WALLET_EVENT_TYPES,
    ...UPDATE_EVENT_TYPES,
    ...SANDBOX_EVENT_TYPES,
    ...LEGACY_EVENT_TYPES,
  ]),
] as const;

// --- Singleton EventSource ---
// All subscribers share a single SSE connection to avoid exhausting the
// browser's per-origin HTTP/1.1 connection pool (6 connections max).

const handlers = new Set<EventHandler>();
let sharedES: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

function dispatch(event: string, raw: string) {
  let parsed: { event?: string; data?: unknown };
  try {
    parsed = JSON.parse(raw) as { event?: string; data?: unknown };
  } catch {
    return;
  }
  const name = parsed.event ?? event;
  const data = parsed.data ?? parsed;
  for (const h of handlers) {
    h(name, data);
  }
}

function ensureConnection() {
  if (sharedES) return;

  const es = new EventSource(SSE_URL);

  es.onmessage = (msg) => {
    dispatch("message", msg.data);
  };

  for (const type of KNOWN_EVENT_TYPES) {
    es.addEventListener(type, (e) => {
      dispatch(type, (e as MessageEvent).data);
    });
  }

  es.onerror = () => {
    es.close();
    sharedES = null;
    if (handlers.size > 0 && !reconnectTimer) {
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        if (handlers.size > 0) ensureConnection();
      }, 3000);
    }
  };

  sharedES = es;
}

export function subscribe(handler: EventHandler): () => void {
  handlers.add(handler);
  ensureConnection();

  return () => {
    handlers.delete(handler);
    if (handlers.size === 0 && sharedES) {
      sharedES.close();
      sharedES = null;
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
    }
  };
}
