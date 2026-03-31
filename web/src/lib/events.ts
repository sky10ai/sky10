/** SSE client for real-time daemon events. */

export type EventHandler = (event: string, data: unknown) => void;

const SSE_URL = "/rpc/events";

export function subscribe(handler: EventHandler): () => void {
  let es: EventSource | null = null;
  let closed = false;

  function connect() {
    if (closed) return;
    es = new EventSource(SSE_URL);

    es.onmessage = (msg) => {
      try {
        const parsed = JSON.parse(msg.data);
        handler(parsed.event, parsed.data);
      } catch {
        // ignore malformed events
      }
    };

    // Named events from the server use the event name as the SSE event type.
    // The server sends: `event: <name>\ndata: ...\n\n`
    // EventSource routes named events to addEventListener, not onmessage.
    // We listen for common event types.
    const eventTypes = [
      "sync.progress",
      "sync.complete",
      "sync.error",
      "drive.started",
      "drive.stopped",
      "device.connected",
      "device.disconnected",
      "kv.updated",
      "link.peer.connected",
      "link.peer.disconnected",
    ];

    for (const type of eventTypes) {
      es.addEventListener(type, (e) => {
        try {
          const parsed = JSON.parse((e as MessageEvent).data);
          handler(parsed.event ?? type, parsed.data ?? parsed);
        } catch {
          // ignore
        }
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
