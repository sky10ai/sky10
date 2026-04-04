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
    ...LEGACY_EVENT_TYPES,
  ]),
] as const;

function emitParsed(
  handler: EventHandler,
  raw: string,
  fallbackEvent?: string
) {
  try {
    const parsed = JSON.parse(raw) as {
      event?: string;
      data?: unknown;
    };
    handler(parsed.event ?? fallbackEvent ?? "message", parsed.data ?? parsed);
  } catch {
    if (fallbackEvent) {
      handler(fallbackEvent, raw);
    }
  }
}

export function subscribe(
  handler: EventHandler,
  eventTypes: readonly string[] = KNOWN_EVENT_TYPES
): () => void {
  let es: EventSource | null = null;
  let closed = false;

  function connect() {
    if (closed) return;
    es = new EventSource(SSE_URL);

    es.onmessage = (msg) => {
      emitParsed(handler, msg.data);
    };

    // Named events from the server use the event name as the SSE event type.
    // The server sends: `event: <name>\ndata: ...\n\n`
    // EventSource routes named events to addEventListener, not onmessage.
    // We listen for known event types emitted by the daemon.
    for (const type of [...new Set(eventTypes)]) {
      es.addEventListener(type, (e) => {
        emitParsed(handler, (e as MessageEvent).data, type);
      });
    }

    es.onerror = () => {
      es?.close();
      // Reconnect after a short delay.
      if (!closed) {
        setTimeout(connect, 3000);
      }
    };
  }

  connect();

  return () => {
    closed = true;
    es?.close();
  };
}
