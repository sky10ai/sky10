import fs from "node:fs";
import os from "node:os";
import { randomUUID } from "node:crypto";
import path from "node:path";

import { guessMimeType, sanitizeFilename } from "./media.js";

const MESSENGERS_ENDPOINT_PATH = "/bridge/messengers/ws";
const DEFAULT_RPC_URL = "http://localhost:9101";
const DEFAULT_TIMEOUT_MS = 30_000;
const DEFAULT_HELPER_PATH = path.join(os.homedir(), ".openclaw", "sky10-messaging.mjs");

export class MessagingBridgeError extends Error {
  constructor(code, message) {
    super(message ? `messaging ${code}: ${message}` : `messaging ${code}`);
    this.name = "MessagingBridgeError";
    this.code = code;
  }
}

export function deriveMessagingWsUrl({ wsUrl = "", rpcUrl = DEFAULT_RPC_URL, agentName = "" } = {}) {
  const raw = String(wsUrl || "").trim();
  const base = raw || String(rpcUrl || DEFAULT_RPC_URL).trim() || DEFAULT_RPC_URL;
  const url = new URL(base);
  if (url.protocol === "http:") {
    url.protocol = "ws:";
  } else if (url.protocol === "https:") {
    url.protocol = "wss:";
  }
  if (!raw || !url.pathname || url.pathname === "/" || url.pathname === "/rpc") {
    url.pathname = MESSENGERS_ENDPOINT_PATH;
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

export function createMessagingClient({
  wsUrl = "",
  rpcUrl = DEFAULT_RPC_URL,
  agentName = "",
  timeoutMs = DEFAULT_TIMEOUT_MS,
  WebSocketImpl,
} = {}) {
  const url = deriveMessagingWsUrl({ wsUrl, rpcUrl, agentName });

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
        fail(new Error(`messaging ${type} timed out after ${timeoutMs}ms`));
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
            fail(new MessagingBridgeError(response.error.code || "error", response.error.message || ""));
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
          fail(new Error(`messaging ${type} socket closed before response`));
        }
      });
    });
  };

  return {
    url,
    listConnections: (params = {}) => request("messengers.list_connections", params),
    listConversations: (params = {}) => request("messengers.list_conversations", params),
    listEvents: (params = {}) => request("messengers.list_events", params),
    getMessages: (params = {}) => request("messengers.get_messages", params),
    searchConversations: (params = {}) => request("messengers.search_conversations", params),
    searchMessages: (params = {}) => request("messengers.search_messages", params),
    createDraft: (params = {}) => request("messengers.create_draft", params),
    requestSend: (params = {}) => request("messengers.request_send", params),
  };
}

export function formatMessagingPromptContext(context = {}) {
  const helperPath = context.helperPath || DEFAULT_HELPER_PATH;
  return [
    "Settings-connected messaging accounts are available through sky10.messaging.",
    "Use this helper to list exposed Telegram, Slack, and IMAP/SMTP connections, search conversations/messages, and read message contents.",
    "Message results may include attachment refs mounted as guest-local files; use those paths directly instead of asking for base64.",
    `List connections: node ${helperPath} connections`,
    `Search messages: node ${helperPath} search-messages '{"connection_id":"CONNECTION_ID","query":"SEARCH TERMS","limit":10}'`,
    `Read messages: node ${helperPath} messages '{"connection_id":"CONNECTION_ID","conversation_id":"CONVERSATION_ID","limit":20}'`,
    "Only create drafts or request send when explicitly asked to reply or send a message.",
  ].join("\n");
}

export function installMessagingHelper({
  helperPath = DEFAULT_HELPER_PATH,
  wsUrl = "",
  rpcUrl = DEFAULT_RPC_URL,
  agentName = "",
  moduleUrl = import.meta.url,
} = {}) {
  const resolvedPath = String(helperPath || DEFAULT_HELPER_PATH).trim() || DEFAULT_HELPER_PATH;
  const resolvedWsUrl = deriveMessagingWsUrl({ wsUrl, rpcUrl, agentName });
  const content = `#!/usr/bin/env node
process.env.SKY10_MESSAGING_WS_URL ||= ${JSON.stringify(resolvedWsUrl)};
process.env.SKY10_RPC_URL ||= ${JSON.stringify(rpcUrl || DEFAULT_RPC_URL)};
process.env.SKY10_AGENT_NAME ||= ${JSON.stringify(agentName || "")};
const { runMessagingCLI } = await import(${JSON.stringify(moduleUrl)});
await runMessagingCLI(process.argv.slice(2), process.env, (value) => console.log(value), (value) => console.error(value));
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

export async function runMessagingCLI(args = process.argv.slice(2), env = process.env, stdout = console.log, stderr = console.error) {
  const [command, raw] = args;
  const client = createMessagingClient({
    wsUrl: env.SKY10_MESSAGING_WS_URL,
    rpcUrl: env.SKY10_RPC_URL || DEFAULT_RPC_URL,
    agentName: env.SKY10_AGENT_NAME || "",
  });

  try {
    let result;
    switch (command) {
    case "connections":
      result = await client.listConnections(parseJSONArg(raw, "connections params"));
      break;
    case "conversations":
      result = await client.listConversations(parseJSONArg(raw, "conversations params"));
      break;
    case "events":
      result = await client.listEvents(parseJSONArg(raw, "events params"));
      break;
    case "messages":
      result = await client.getMessages(parseJSONArg(raw, "messages params"));
      break;
    case "search-conversations":
      result = await client.searchConversations(parseJSONArg(raw, "search-conversations params"));
      break;
    case "search-messages":
      result = await client.searchMessages(parseJSONArg(raw, "search-messages params"));
      break;
    case "draft":
      result = await client.createDraft(parseJSONArg(raw, "draft params"));
      break;
    case "send":
      result = await client.requestSend(parseJSONArg(raw, "send params"));
      break;
    default:
      stderr("usage: sky10-messaging <connections [json] | conversations json | events json | messages json | search-conversations json | search-messages json | draft json | send json>");
      process.exitCode = 2;
      return;
    }
    stdout(JSON.stringify(result, null, 2));
  } catch (err) {
    stderr(err?.message ?? String(err));
    process.exitCode = 1;
  }
}

function stringValue(value) {
  return typeof value === "string" ? value.trim() : "";
}

function partText(part) {
  const kind = stringValue(part?.kind);
  if (!["text", "markdown", "html"].includes(kind)) {
    return "";
  }
  return typeof part.text === "string" ? part.text.trim() : "";
}

export function textFromMessageParts(parts = []) {
  return parts.map(partText).filter(Boolean).join("\n\n");
}

function mediaKindForPart(part) {
  const metadata = part?.metadata && typeof part.metadata === "object" ? part.metadata : {};
  const contentType = stringValue(part?.content_type) || stringValue(metadata.telegram_media_type) || guessMimeType(part?.file_name || part?.ref || "");
  if (part?.kind === "image" || contentType.startsWith("image/")) {
    return "image";
  }
  if (contentType.startsWith("audio/")) {
    return "audio";
  }
  if (contentType.startsWith("video/")) {
    return "video";
  }
  return "file";
}

function mediaLabel(part) {
  const kind = mediaKindForPart(part);
  const filename = stringValue(part?.file_name) || path.basename(stringValue(part?.ref)) || "attachment";
  return `${kind}: ${filename}`;
}

function mediaSummary(parts = []) {
  const media = parts.filter((part) => ["file", "image"].includes(stringValue(part?.kind)));
  if (media.length === 0) {
    return "";
  }
  return `Messaging attachment${media.length === 1 ? "" : "s"}: ${media.map(mediaLabel).join(", ")}`;
}

function openClawMediaPart(part) {
  const ref = stringValue(part?.ref);
  if (!ref) {
    return null;
  }
  const filename = sanitizeFilename(part?.file_name || path.basename(ref), "telegram-attachment");
  const mediaType = stringValue(part?.content_type) || guessMimeType(filename);
  return {
    type: mediaKindForPart(part),
    filename,
    media_type: mediaType,
    source: {
      type: "file",
      path: ref,
      filename,
      media_type: mediaType,
    },
  };
}

export function contentFromMessage(message) {
  const parts = Array.isArray(message?.parts) ? message.parts : [];
  const text = textFromMessageParts(parts);
  const mediaParts = parts.map(openClawMediaPart).filter(Boolean);
  const fallback = text || mediaSummary(parts);
  const contentParts = [];
  if (fallback) {
    contentParts.push({ type: "text", text: fallback });
  }
  contentParts.push(...mediaParts);
  return {
    text: fallback || undefined,
    parts: contentParts,
  };
}

export function messagingPartsFromReplyContent(replyContent, fallbackText = "") {
  const parts = [];
  const source = replyContent && typeof replyContent === "object" ? replyContent : {};
  const contentParts = Array.isArray(source.parts) ? source.parts : [];
  for (const part of contentParts) {
    if (!part || typeof part !== "object") {
      continue;
    }
    if (part.type === "text" && typeof part.text === "string" && part.text.trim()) {
      parts.push({ kind: "text", text: part.text.trim() });
    }
  }
  if (parts.length === 0 && typeof source.text === "string" && source.text.trim()) {
    parts.push({ kind: "text", text: source.text.trim() });
  }
  if (parts.length === 0 && String(fallbackText || "").trim()) {
    parts.push({ kind: "text", text: String(fallbackText).trim() });
  }
  return parts;
}

export function senderLabel(message) {
  const sender = message?.sender && typeof message.sender === "object" ? message.sender : {};
  return stringValue(sender.display_name) || stringValue(sender.address) || stringValue(sender.remote_id) || "Telegram";
}

export function conversationLabel(conversation, message) {
  return stringValue(conversation?.title) || senderLabel(message);
}
