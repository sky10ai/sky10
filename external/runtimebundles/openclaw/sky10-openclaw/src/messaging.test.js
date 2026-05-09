import test from "node:test";
import assert from "node:assert/strict";

import {
  MessagingBridgeError,
  contentFromMessage,
  createMessagingClient,
  deriveMessagingWsUrl,
  messagingPartsFromReplyContent,
} from "./messaging.js";

class FakeWebSocket {
  static instances = [];
  static nextResponse = null;

  constructor(url) {
    this.url = url;
    this.sent = [];
    this.handlers = new Map();
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => this.emit("open", {}));
  }

  addEventListener(name, handler) {
    const handlers = this.handlers.get(name) ?? [];
    handlers.push(handler);
    this.handlers.set(name, handlers);
  }

  emit(name, event) {
    for (const handler of this.handlers.get(name) ?? []) {
      handler(event);
    }
  }

  send(raw) {
    const envelope = JSON.parse(raw);
    this.sent.push(envelope);
    const response = FakeWebSocket.nextResponse
      ? FakeWebSocket.nextResponse(envelope)
      : { type: envelope.type, request_id: envelope.request_id, payload: {} };
    queueMicrotask(() => this.emit("message", { data: JSON.stringify(response) }));
  }

  close() {
    this.closed = true;
  }
}

function resetFakeWebSocket() {
  FakeWebSocket.instances = [];
  FakeWebSocket.nextResponse = null;
}

test("deriveMessagingWsUrl derives bridge endpoint from sky10 RPC URL", () => {
  assert.equal(
    deriveMessagingWsUrl({ rpcUrl: "http://localhost:9101", agentName: "travel-agent" }),
    "ws://localhost:9101/bridge/messengers/ws?agent=travel-agent",
  );
  assert.equal(
    deriveMessagingWsUrl({ rpcUrl: "https://sky10.example/rpc", agentName: "agent/a" }),
    "wss://sky10.example/bridge/messengers/ws?agent=agent%2Fa",
  );
});

test("client lists telegram connections over the messenger bridge websocket", async () => {
  resetFakeWebSocket();
  FakeWebSocket.nextResponse = (envelope) => ({
    type: envelope.type,
    request_id: envelope.request_id,
    payload: {
      connections: [{ id: "telegram/default", adapter_id: "telegram" }],
    },
  });

  const client = createMessagingClient({
    rpcUrl: "http://localhost:9101",
    agentName: "travel-agent",
    WebSocketImpl: FakeWebSocket,
  });
  const result = await client.listConnections({ adapter_id: "telegram" });

  assert.equal(FakeWebSocket.instances[0].url, "ws://localhost:9101/bridge/messengers/ws?agent=travel-agent");
  assert.equal(FakeWebSocket.instances[0].sent[0].type, "messengers.list_connections");
  assert.equal(FakeWebSocket.instances[0].sent[0].payload.adapter_id, "telegram");
  assert.equal(result.connections[0].id, "telegram/default");
});

test("client rejects structured messenger bridge errors", async () => {
  resetFakeWebSocket();
  FakeWebSocket.nextResponse = (envelope) => ({
    type: envelope.type,
    request_id: envelope.request_id,
    error: { code: "handler_error", message: "connection not exposed" },
  });

  const client = createMessagingClient({
    rpcUrl: "http://localhost:9101",
    agentName: "travel-agent",
    WebSocketImpl: FakeWebSocket,
  });

  await assert.rejects(
    () => client.listConnections({ adapter_id: "telegram" }),
    (err) => err instanceof MessagingBridgeError && err.code === "handler_error",
  );
});

test("contentFromMessage maps telegram file refs to OpenClaw file sources", () => {
  const content = contentFromMessage({
    parts: [
      { kind: "text", text: "listen to this" },
      {
        kind: "file",
        file_name: "voice.ogg",
        content_type: "audio/ogg",
        ref: "/sandbox-state/messengers/inbox/telegram/default/voice.ogg",
        metadata: { telegram_media_type: "voice" },
      },
      {
        kind: "image",
        file_name: "photo.jpg",
        content_type: "image/jpeg",
        ref: "/sandbox-state/messengers/inbox/telegram/default/photo.jpg",
      },
    ],
  });

  assert.equal(content.text, "listen to this");
  assert.equal(content.parts.length, 3);
  assert.equal(content.parts[1].type, "audio");
  assert.equal(content.parts[1].source.type, "file");
  assert.equal(content.parts[1].source.path, "/sandbox-state/messengers/inbox/telegram/default/voice.ogg");
  assert.equal(content.parts[2].type, "image");
});

test("messagingPartsFromReplyContent produces text draft parts", () => {
  assert.deepEqual(
    messagingPartsFromReplyContent({
      text: "first",
      parts: [
        { type: "text", text: "first" },
        { type: "image", filename: "ignored.png" },
        { type: "text", text: "second" },
      ],
    }),
    [
      { kind: "text", text: "first" },
      { kind: "text", text: "second" },
    ],
  );
  assert.deepEqual(messagingPartsFromReplyContent({}, "fallback"), [{ kind: "text", text: "fallback" }]);
});
