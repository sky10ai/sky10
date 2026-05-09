import { randomUUID } from "node:crypto";
import path from "node:path";

import { guessMimeType, sanitizeFilename } from "./media.js";

const MESSENGERS_ENDPOINT_PATH = "/bridge/messengers/ws";
const DEFAULT_RPC_URL = "http://localhost:9101";
const DEFAULT_TIMEOUT_MS = 30_000;

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
    createDraft: (params = {}) => request("messengers.create_draft", params),
    requestSend: (params = {}) => request("messengers.request_send", params),
  };
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
  return `Telegram attachment${media.length === 1 ? "" : "s"}: ${media.map(mediaLabel).join(", ")}`;
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
