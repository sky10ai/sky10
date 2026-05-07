import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";

const X402_ENDPOINT_PATH = "/bridge/metered-services/ws";
const DEFAULT_RPC_URL = "http://localhost:9101";
const DEFAULT_TIMEOUT_MS = 30_000;
const DEFAULT_HELPER_PATH = path.join(os.homedir(), ".openclaw", "sky10-x402.mjs");

export class X402CommsError extends Error {
  constructor(code, message) {
    super(message ? `x402 ${code}: ${message}` : `x402 ${code}`);
    this.name = "X402CommsError";
    this.code = code;
  }
}

export function deriveX402WsUrl({ wsUrl = "", rpcUrl = DEFAULT_RPC_URL, agentName = "" } = {}) {
  const raw = String(wsUrl || "").trim();
  const base = raw || String(rpcUrl || DEFAULT_RPC_URL).trim() || DEFAULT_RPC_URL;
  const url = new URL(base);
  if (url.protocol === "http:") {
    url.protocol = "ws:";
  } else if (url.protocol === "https:") {
    url.protocol = "wss:";
  }
  if (!raw || !url.pathname || url.pathname === "/" || url.pathname === "/rpc") {
    url.pathname = X402_ENDPOINT_PATH;
  }
  if (agentName && !url.searchParams.has("agent")) {
    url.searchParams.set("agent", agentName);
  }
  return url.toString();
}

async function resolveWebSocketImpl(explicit) {
  if (explicit) {
    return explicit;
  }
  if (globalThis.WebSocket) {
    return globalThis.WebSocket;
  }
  try {
    const mod = await import("undici");
    if (mod.WebSocket) {
      return mod.WebSocket;
    }
  } catch {
    // Fall through to a useful error.
  }
  throw new Error("WebSocket is not available; install a runtime with global WebSocket or undici");
}

function addEvent(target, name, fn) {
  if (target.addEventListener) {
    target.addEventListener(name, fn);
    return;
  }
  if (target.on) {
    target.on(name, fn);
  }
}

function eventError(event) {
  return event?.error ?? event?.message ?? event;
}

function messageDataToString(data) {
  if (typeof data === "string") {
    return data;
  }
  if (data instanceof ArrayBuffer) {
    return Buffer.from(data).toString("utf8");
  }
  if (ArrayBuffer.isView(data)) {
    return Buffer.from(data.buffer, data.byteOffset, data.byteLength).toString("utf8");
  }
  return String(data ?? "");
}

function envelopeID() {
  if (typeof randomUUID === "function") {
    return randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function createX402Client({
  wsUrl = "",
  rpcUrl = DEFAULT_RPC_URL,
  agentName = "",
  timeoutMs = DEFAULT_TIMEOUT_MS,
  WebSocketImpl,
} = {}) {
  const url = deriveX402WsUrl({ wsUrl, rpcUrl, agentName });

  const request = async (type, payload = {}) => {
    const Impl = await resolveWebSocketImpl(WebSocketImpl);
    const requestID = envelopeID();
    const envelope = {
      type,
      request_id: requestID,
      nonce: envelopeID(),
      payload,
    };

    return new Promise((resolve, reject) => {
      let settled = false;
      let opened = false;
      const socket = new Impl(url);
      const timer = setTimeout(() => {
        fail(new Error(`x402 ${type} timed out after ${timeoutMs}ms`));
      }, timeoutMs);

      const finish = (fn, value) => {
        if (settled) {
          return;
        }
        settled = true;
        clearTimeout(timer);
        try {
          socket.close?.();
        } catch {
          // Best effort.
        }
        fn(value);
      };
      const fail = (err) => finish(reject, err instanceof Error ? err : new Error(String(err)));
      const succeed = (value) => finish(resolve, value);

      addEvent(socket, "open", () => {
        opened = true;
        try {
          socket.send(JSON.stringify(envelope));
        } catch (err) {
          fail(err);
        }
      });
      addEvent(socket, "message", (event) => {
        try {
          const data = event?.data !== undefined ? event.data : event;
          const response = JSON.parse(messageDataToString(data));
          if (response.request_id !== requestID) {
            return;
          }
          if (response.error) {
            fail(new X402CommsError(response.error.code || "error", response.error.message || ""));
            return;
          }
          succeed(response.payload ?? null);
        } catch (err) {
          fail(err);
        }
      });
      addEvent(socket, "error", (event) => {
        fail(eventError(event));
      });
      addEvent(socket, "close", () => {
        if (!settled && opened) {
          fail(new Error(`x402 ${type} socket closed before response`));
        }
      });
    });
  };

  return {
    url,
    listServices: (params = {}) => request("x402.list_services", params),
    budgetStatus: () => request("x402.budget_status", {}),
    call: (params = {}) => request("x402.service_call", {
      ...params,
      payment_nonce: params.payment_nonce || envelopeID(),
    }),
  };
}

function compactText(value, limit = 180) {
  const text = String(value || "").replace(/\s+/g, " ").trim();
  if (text.length <= limit) {
    return text;
  }
  return `${text.slice(0, limit - 1)}...`;
}

function serviceLine(service) {
  const parts = [service.id || service.display_name || "x402-service"];
  if (service.display_name && service.display_name !== service.id) {
    parts.push(`(${service.display_name})`);
  }
  if (service.price_usdc) {
    parts.push(`price ${service.price_usdc} USDC`);
  }
  if (Array.isArray(service.networks) && service.networks.length > 0) {
    parts.push(`networks ${service.networks.join(", ")}`);
  }
  const hint = compactText(service.hint || service.description);
  if (hint) {
    parts.push(`- ${hint}`);
  }
  return parts.join(" ");
}

function endpointLine(endpoint) {
  const rawURL = String(endpoint?.url || "").trim();
  let endpointPath = rawURL;
  try {
    const parsed = new URL(rawURL);
    endpointPath = `${parsed.pathname}${parsed.search}`;
  } catch {
    // Keep the raw catalog value when it is not an absolute URL.
  }
  const method = String(endpoint?.method || "GET").trim();
  const description = compactText(endpoint?.description || "", 90);
  const price = endpoint?.price_usdc ? ` price ${endpoint.price_usdc} USDC` : "";
  return `${method} ${endpointPath}${price}${description ? ` - ${description}` : ""}`;
}

function serviceEndpointLines(service, limit = 4) {
  if (!Array.isArray(service.endpoints) || service.endpoints.length === 0) {
    return [];
  }
  const lines = service.endpoints.slice(0, limit).map((endpoint) => `  endpoints: ${endpointLine(endpoint)}`);
  if (service.endpoints.length > limit) {
    lines.push(`  endpoints: ...${service.endpoints.length - limit} more; run the list command for the full catalog.`);
  }
  return lines;
}

export function formatX402PromptContext(context = {}) {
  const services = Array.isArray(context.services) ? context.services : [];
  if (services.length === 0) {
    return "";
  }
  const helperPath = context.helperPath || DEFAULT_HELPER_PATH;
  const shown = services.slice(0, 12);
  const lines = [
    "Settings-approved x402 APIs are available.",
    "Routing rule: use browser or web-search for casual browsing and unstructured reading. Use x402 only when an approved service's hint, description, or endpoint list advertises structured/API-grade data that directly matches the task.",
    "Prefer the listed service's x402 API over browser/search when you need the exact records described below; otherwise use free local tools first.",
    "The sky10 helper handles x402 payment, receipts, and wallet signing; do not manage wallets, payment headers, or x402 challenges yourself.",
    `List services: node ${helperPath} list`,
    `Call a service: node ${helperPath} call '{"service_id":"SERVICE_ID","path":"/PATH","method":"GET","max_price_usdc":"0.01"}'`,
    "For calls, use service_id from the list and pass a relative path plus any query string; do not pass a full URL.",
    "Approved services:",
  ];
  for (const service of shown) {
    lines.push(`- ${serviceLine(service)}`);
    lines.push(...serviceEndpointLines(service));
  }
  if (services.length > shown.length) {
    lines.push(`- ...${services.length - shown.length} more; run the list command for the full catalog.`);
  }
  return lines.join("\n");
}

export function installX402Helper({
  helperPath = DEFAULT_HELPER_PATH,
  wsUrl = "",
  rpcUrl = DEFAULT_RPC_URL,
  agentName = "",
  moduleUrl = import.meta.url,
} = {}) {
  const resolvedPath = String(helperPath || DEFAULT_HELPER_PATH).trim() || DEFAULT_HELPER_PATH;
  const resolvedWsUrl = deriveX402WsUrl({ wsUrl, rpcUrl, agentName });
  const content = `#!/usr/bin/env node
process.env.SKY10_X402_WS_URL ||= ${JSON.stringify(resolvedWsUrl)};
process.env.SKY10_RPC_URL ||= ${JSON.stringify(rpcUrl || DEFAULT_RPC_URL)};
process.env.SKY10_AGENT_NAME ||= ${JSON.stringify(agentName || "")};
const { runX402CLI } = await import(${JSON.stringify(moduleUrl)});
await runX402CLI(process.argv.slice(2), process.env, (value) => console.log(value), (value) => console.error(value));
`;
  fs.mkdirSync(path.dirname(resolvedPath), { recursive: true });
  fs.writeFileSync(resolvedPath, content, { mode: 0o755 });
  fs.chmodSync(resolvedPath, 0o755);
  return resolvedPath;
}

function parseJSONArg(raw, label) {
  if (!raw) {
    return {};
  }
  try {
    return JSON.parse(raw);
  } catch (err) {
    throw new Error(`${label} must be JSON: ${err.message}`);
  }
}

export async function runX402CLI(args = process.argv.slice(2), env = process.env, stdout = console.log, stderr = console.error) {
  const [command, raw] = args;
  const client = createX402Client({
    wsUrl: env.SKY10_X402_WS_URL,
    rpcUrl: env.SKY10_RPC_URL || DEFAULT_RPC_URL,
    agentName: env.SKY10_AGENT_NAME || "",
  });

  try {
    let result;
    switch (command) {
    case "list":
      result = await client.listServices(parseJSONArg(raw, "list params"));
      break;
    case "budget":
      result = await client.budgetStatus();
      break;
    case "call":
      result = await client.call(parseJSONArg(raw, "call params"));
      break;
    default:
      stderr("usage: sky10-x402 <list [json] | budget | call json>");
      process.exitCode = 2;
      return;
    }
    stdout(JSON.stringify(result, null, 2));
  } catch (err) {
    stderr(err?.message ?? String(err));
    process.exitCode = 1;
  }
}
