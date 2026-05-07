import test from "node:test";
import assert from "node:assert/strict";

import {
  X402CommsError,
  createX402Client,
  deriveX402WsUrl,
  formatX402PromptContext,
} from "./x402.js";

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

test("deriveX402WsUrl derives bridge endpoint from sky10 RPC URL", () => {
  assert.equal(
    deriveX402WsUrl({ rpcUrl: "http://localhost:9101", agentName: "travel-agent" }),
    "ws://localhost:9101/bridge/metered-services/ws?agent=travel-agent",
  );
  assert.equal(
    deriveX402WsUrl({ rpcUrl: "https://sky10.example/rpc", agentName: "agent/a" }),
    "wss://sky10.example/bridge/metered-services/ws?agent=agent%2Fa",
  );
});

test("client lists services over the x402 bridge websocket", async () => {
  resetFakeWebSocket();
  FakeWebSocket.nextResponse = (envelope) => ({
    type: envelope.type,
    request_id: envelope.request_id,
    payload: {
      services: [{ id: "structured-travel-api", display_name: "Structured Travel API" }],
    },
  });

  const client = createX402Client({
    rpcUrl: "http://localhost:9101",
    agentName: "travel-agent",
    WebSocketImpl: FakeWebSocket,
  });
  const result = await client.listServices();

  assert.equal(FakeWebSocket.instances[0].url, "ws://localhost:9101/bridge/metered-services/ws?agent=travel-agent");
  assert.equal(FakeWebSocket.instances[0].sent[0].type, "x402.list_services");
  assert.equal(result.services[0].id, "structured-travel-api");
});

test("client adds payment nonce for service calls", async () => {
  resetFakeWebSocket();
  FakeWebSocket.nextResponse = (envelope) => ({
    type: envelope.type,
    request_id: envelope.request_id,
    payload: { status: 200, body: { ok: true } },
  });

  const client = createX402Client({
    rpcUrl: "http://localhost:9101",
    agentName: "travel-agent",
    WebSocketImpl: FakeWebSocket,
  });
  await client.call({
    service_id: "structured-travel-api",
    path: "/api/v1/location/search",
    method: "GET",
    max_price_usdc: "0.01",
  });

  const payload = FakeWebSocket.instances[0].sent[0].payload;
  assert.equal(FakeWebSocket.instances[0].sent[0].type, "x402.service_call");
  assert.equal(payload.service_id, "structured-travel-api");
  assert.ok(payload.payment_nonce);
});

test("client rejects structured comms errors", async () => {
  resetFakeWebSocket();
  FakeWebSocket.nextResponse = (envelope) => ({
    type: envelope.type,
    request_id: envelope.request_id,
    error: { code: "handler_error", message: "service not approved" },
  });

  const client = createX402Client({
    rpcUrl: "http://localhost:9101",
    agentName: "travel-agent",
    WebSocketImpl: FakeWebSocket,
  });

  await assert.rejects(
    () => client.listServices(),
    (err) => err instanceof X402CommsError && err.code === "handler_error",
  );
});

test("formatX402PromptContext tells the agent how to use approved services", () => {
  const prompt = formatX402PromptContext({
    helperPath: "/root/.openclaw/sky10-x402.mjs",
    services: [{
      id: "structured-travel-api",
      display_name: "Structured Travel API",
      price_usdc: "0.01",
      networks: ["base"],
      hint: "Use when the agent needs structured location IDs and review records; prefer browser/search for casual browsing.",
      endpoints: [{
        url: "https://api.example.com/v1/location/search",
        method: "GET",
        description: "Location search",
        price_usdc: "0.01",
      }],
    }],
  });

  assert.match(prompt, /Settings-approved x402 APIs are available/);
  assert.match(prompt, /Use x402 only when an approved service's hint/i);
  assert.match(prompt, /Prefer the listed service's x402 API over browser\/search/i);
  assert.match(prompt, /node \/root\/\.openclaw\/sky10-x402\.mjs call/);
  assert.match(prompt, /GET \/v1\/location\/search price 0\.01 USDC - Location search/);
  assert.match(prompt, /structured-travel-api/);
  assert.match(prompt, /do not manage wallets/);
});
