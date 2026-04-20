import type { DeliveryMetadata } from "./rpc";

export interface ChatMessage {
  id: string;
  from: "user" | "agent";
  type: string;
  content: string;
  timestamp: Date;
  streaming?: boolean;
  delivered?: boolean;
  delivery?: DeliveryMetadata;
}

function parseTimestamp(value: unknown): Date {
  if (value instanceof Date && !Number.isNaN(value.getTime())) {
    return value;
  }
  if (typeof value === "string" || typeof value === "number") {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.getTime())) {
      return parsed;
    }
  }
  return new Date();
}

function stringifyChatContent(content: unknown): string {
  if (content == null) return "";
  try {
    const text = JSON.stringify(content);
    return typeof text === "string" ? text : String(content);
  } catch {
    return String(content);
  }
}

export interface StreamingEnvelope {
  stream_id?: string;
  text?: string;
}

export function readStreamingEnvelope(content: unknown): StreamingEnvelope {
  if (!content || typeof content !== "object") {
    return {};
  }
  const value = content as Record<string, unknown>;
  return {
    stream_id: typeof value.stream_id === "string" ? value.stream_id : undefined,
    text: typeof value.text === "string" ? value.text : undefined,
  };
}

export function readChatContentText(content: unknown): string {
  if (typeof content === "string") return content;
  if (!content || typeof content !== "object") return stringifyChatContent(content);

  const value = content as Record<string, unknown>;
  if (typeof value.text === "string" && value.text !== "") {
    return value.text;
  }

  if (Array.isArray(value.parts)) {
    const parts = value.parts
      .map((part) => {
        if (!part || typeof part !== "object") return "";
        const item = part as Record<string, unknown>;
        const partType = typeof item.type === "string" ? item.type : "text";
        if (partType !== "text") return "";
        return typeof item.text === "string" ? item.text : "";
      })
      .filter((part) => part !== "");
    if (parts.length > 0) {
      return parts.join("");
    }
  }

  return stringifyChatContent(content);
}

function normalizeMessage(value: unknown): ChatMessage | null {
  if (!value || typeof value !== "object") return null;
  const msg = value as Partial<ChatMessage>;
  if (typeof msg.id !== "string" || msg.id === "") return null;
  if (msg.from !== "user" && msg.from !== "agent") return null;
  if (typeof msg.type !== "string") return null;
  if (typeof msg.content !== "string") return null;
  return {
    id: msg.id,
    from: msg.from,
    type: msg.type,
    content: msg.content,
    timestamp: parseTimestamp(msg.timestamp),
    streaming: msg.streaming,
    delivered: msg.delivered,
    delivery: msg.delivery,
  };
}

function isAdjacentDuplicateAgentReply(prev: ChatMessage | undefined, next: ChatMessage): boolean {
  return !!prev &&
    prev.from === "agent" &&
    next.from === "agent" &&
    prev.type === next.type &&
    prev.content === next.content;
}

export function dedupeChatMessages(messages: readonly unknown[]): ChatMessage[] {
  const seenIDs = new Set<string>();
  const deduped: ChatMessage[] = [];
  for (const value of messages) {
    const msg = normalizeMessage(value);
    if (!msg) continue;
    if (seenIDs.has(msg.id)) continue;
    if (isAdjacentDuplicateAgentReply(deduped[deduped.length - 1], msg)) continue;
    seenIDs.add(msg.id);
    deduped.push(msg);
  }
  return deduped;
}

export function appendChatMessage(messages: readonly ChatMessage[], message: ChatMessage): ChatMessage[] {
  return dedupeChatMessages([...messages, message]);
}

export function applyStreamingDelta(
  messages: readonly ChatMessage[],
  streamID: string,
  deltaText: string,
  timestamp = new Date(),
): ChatMessage[] {
  if (!streamID || !deltaText) {
    return [...messages];
  }

  const syntheticID = `stream:${streamID}`;
  const next = [...messages];
  const index = next.findIndex((message) => message.id === syntheticID);
  if (index === -1) {
    next.push({
      id: syntheticID,
      from: "agent",
      type: "delta",
      content: deltaText,
      timestamp,
      streaming: true,
    });
    return next;
  }

  const existing = next[index]!;
  next[index] = {
    ...existing,
    type: "delta",
    content: `${existing.content}${deltaText}`,
    timestamp,
    streaming: true,
  };
  return next;
}

export function finalizeStreamingMessage(
  messages: readonly ChatMessage[],
  streamID: string,
  finalMessage: ChatMessage,
): ChatMessage[] {
  if (!streamID) {
    return appendChatMessage(messages, finalMessage);
  }

  const syntheticID = `stream:${streamID}`;
  const next = [...messages];
  const index = next.findIndex((message) => message.id === syntheticID);
  if (index === -1) {
    return appendChatMessage(next, { ...finalMessage, streaming: false });
  }

  next[index] = {
    ...finalMessage,
    streaming: false,
  };
  return dedupeChatMessages(next);
}

export function loadChatMessages(raw: string | null): ChatMessage[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return dedupeChatMessages(parsed);
  } catch {
    return [];
  }
}
