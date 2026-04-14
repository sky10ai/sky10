import type { DeliveryMetadata } from "./rpc";

export interface ChatMessage {
  id: string;
  from: "user" | "agent";
  type: string;
  content: string;
  timestamp: Date;
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
