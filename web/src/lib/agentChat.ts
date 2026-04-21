import type { ChatContent, ChatContentPart, DeliveryMetadata } from "./rpc";

export interface ChatMessageTiming {
  firstTokenMs?: number;
  completeMs?: number;
}

export interface ChatMessage {
  id: string;
  from: "user" | "agent";
  type: string;
  content: ChatContent;
  timestamp: Date;
  streaming?: boolean;
  delivered?: boolean;
  delivery?: DeliveryMetadata;
  timing?: ChatMessageTiming;
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

function normalizePart(value: unknown): ChatContentPart | null {
  if (!value || typeof value !== "object") return null;
  const part = value as Partial<ChatContentPart>;
  const type = typeof part.type === "string" && part.type.trim()
    ? part.type.trim()
    : (typeof part.text === "string" ? "text" : "file");
  const normalized: ChatContentPart = {
    type,
    text: typeof part.text === "string" ? part.text : undefined,
    filename: typeof part.filename === "string" ? part.filename : undefined,
    media_type: typeof part.media_type === "string" ? part.media_type : undefined,
    caption: typeof part.caption === "string" ? part.caption : undefined,
  };
  if (part.source && typeof part.source === "object") {
    normalized.source = {
      type: typeof part.source.type === "string" ? part.source.type : "",
      data: typeof part.source.data === "string" ? part.source.data : undefined,
      url: typeof part.source.url === "string" ? part.source.url : undefined,
      filename: typeof part.source.filename === "string" ? part.source.filename : undefined,
      media_type: typeof part.source.media_type === "string" ? part.source.media_type : undefined,
    };
  }
  return normalized;
}

export function normalizeChatContent(value: unknown): ChatContent {
  if (typeof value === "string") {
    return {
      text: value,
      parts: [{ type: "text", text: value }],
    };
  }
  if (!value || typeof value !== "object") {
    if (value == null) {
      return {};
    }
    const serialized = stringifyChatContent(value);
    return {
      text: serialized,
      parts: [{ type: "text", text: serialized }],
    };
  }

  const content = value as Partial<ChatContent>;
  const text = typeof content.text === "string" ? content.text : "";
  const parts = Array.isArray(content.parts)
    ? content.parts.map(normalizePart).filter((part): part is ChatContentPart => part !== null)
    : [];

  if (parts.length === 0 && text) {
    parts.push({ type: "text", text });
  }
  if (parts.length > 0) {
    return {
      text: text || undefined,
      parts,
    };
  }

  const keys = Object.keys(content as object);
  if (keys.length === 0) {
    return {};
  }

  const serialized = stringifyChatContent(value);
  return {
    text: serialized,
    parts: [{ type: "text", text: serialized }],
  };
}

export interface StreamingEnvelope {
  stream_id?: string;
  text?: string;
  client_request_id?: string;
}

export function readStreamingEnvelope(content: unknown): StreamingEnvelope {
  if (!content || typeof content !== "object") {
    return {};
  }
  const value = content as Record<string, unknown>;
  return {
    stream_id: typeof value.stream_id === "string" ? value.stream_id : undefined,
    text: typeof value.text === "string" ? value.text : undefined,
    client_request_id: typeof value.client_request_id === "string" ? value.client_request_id : undefined,
  };
}

export function chatContentText(content: ChatContent): string {
  const parts = Array.isArray(content.parts) ? content.parts : [];
  const texts = parts
    .filter((part) => part.type === "text" && typeof part.text === "string")
    .map((part) => part.text ?? "")
    .filter((text) => text !== "");
  if (texts.length > 0) {
    return texts.join("\n\n");
  }
  return typeof content.text === "string" ? content.text : "";
}

export function readChatContentText(content: unknown): string {
  return chatContentText(normalizeChatContent(content));
}

function compactChatContent(content: ChatContent): ChatContent {
  const normalized = normalizeChatContent(content);
  return {
    text: normalized.text,
    parts: normalized.parts?.map((part) => ({
      ...part,
      source: part.source
        ? {
            ...part.source,
            data: undefined,
          }
        : part.source,
    })),
  };
}

function contentFingerprint(content: ChatContent): string {
  return JSON.stringify(compactChatContent(content));
}

function normalizeTiming(value: unknown): ChatMessageTiming | undefined {
  if (!value || typeof value !== "object") return undefined;
  const timing = value as Record<string, unknown>;
  const firstTokenMs = typeof timing.firstTokenMs === "number" ? timing.firstTokenMs : undefined;
  const completeMs = typeof timing.completeMs === "number" ? timing.completeMs : undefined;
  if (firstTokenMs == null && completeMs == null) return undefined;
  return { firstTokenMs, completeMs };
}

function normalizeMessage(value: unknown): ChatMessage | null {
  if (!value || typeof value !== "object") return null;
  const msg = value as Partial<ChatMessage> & { content?: unknown };
  if (typeof msg.id !== "string" || msg.id === "") return null;
  if (msg.from !== "user" && msg.from !== "agent") return null;
  if (typeof msg.type !== "string") return null;
  return {
    id: msg.id,
    from: msg.from,
    type: msg.type,
    content: normalizeChatContent(msg.content),
    timestamp: parseTimestamp(msg.timestamp),
    streaming: msg.streaming,
    delivered: msg.delivered,
    delivery: msg.delivery,
    timing: normalizeTiming(msg.timing),
  };
}

function mergeTiming(existing?: ChatMessageTiming, next?: ChatMessageTiming): ChatMessageTiming | undefined {
  if (!existing && !next) return undefined;
  return {
    firstTokenMs: next?.firstTokenMs ?? existing?.firstTokenMs,
    completeMs: next?.completeMs ?? existing?.completeMs,
  };
}

function isAdjacentDuplicateAgentReply(prev: ChatMessage | undefined, next: ChatMessage): boolean {
  return !!prev &&
    prev.from === "agent" &&
    next.from === "agent" &&
    prev.type === next.type &&
    contentFingerprint(prev.content) === contentFingerprint(next.content);
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
  return dedupeChatMessagesStable(deduped);
}

function dedupeChatMessagesStable(messages: readonly ChatMessage[]): ChatMessage[] {
  return [...messages];
}

export function appendChatMessage(messages: readonly ChatMessage[], message: ChatMessage): ChatMessage[] {
  return dedupeChatMessages([...messages, message]);
}

export function applyStreamingDelta(
  messages: readonly ChatMessage[],
  streamID: string,
  deltaText: string,
  timestamp = new Date(),
  timing?: ChatMessageTiming,
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
      content: {
        text: deltaText,
        parts: [{ type: "text", text: deltaText }],
      },
      timestamp,
      streaming: true,
      timing,
    });
    return next;
  }

  const existing = next[index]!;
  const contentText = `${chatContentText(existing.content)}${deltaText}`;
  next[index] = {
    ...existing,
    type: "delta",
    content: {
      text: contentText,
      parts: [{ type: "text", text: contentText }],
    },
    timestamp,
    streaming: true,
    timing: mergeTiming(existing.timing, timing),
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
    timing: mergeTiming(next[index]?.timing, finalMessage.timing),
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

export function serializeChatMessages(messages: readonly ChatMessage[]): string {
  return JSON.stringify(messages.map((msg) => ({
    ...msg,
    content: compactChatContent(msg.content),
  })));
}
