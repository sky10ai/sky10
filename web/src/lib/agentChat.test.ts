import { describe, expect, test } from "bun:test";
import {
  appendChatMessage,
  applyStreamingDelta,
  dedupeChatMessages,
  finalizeStreamingMessage,
  readChatContentText,
  readStreamingEnvelope,
  type ChatMessage,
} from "./agentChat";

function msg(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: overrides.id ?? crypto.randomUUID(),
    from: overrides.from ?? "agent",
    type: overrides.type ?? "text",
    content: overrides.content ?? "hello",
    timestamp: overrides.timestamp ?? new Date("2026-04-14T22:00:00Z"),
    delivered: overrides.delivered,
    delivery: overrides.delivery,
    timing: overrides.timing,
  };
}

describe("dedupeChatMessages", () => {
  test("drops duplicate inbound message ids", () => {
    const first = msg({ id: "m-1", content: "same" });
    const dup = msg({ id: "m-1", content: "same" });
    expect(dedupeChatMessages([first, dup])).toEqual([first]);
  });

  test("collapses adjacent identical agent replies from prior fan-out", () => {
    const first = msg({ id: "m-1", content: "HEARTBEAT_OK" });
    const second = msg({ id: "m-2", content: "HEARTBEAT_OK" });
    expect(dedupeChatMessages([first, second])).toEqual([first]);
  });

  test("keeps identical agent replies when separated by a user turn", () => {
    const first = msg({ id: "m-1", content: "HEARTBEAT_OK" });
    const user = msg({ id: "u-1", from: "user", content: "again" });
    const second = msg({ id: "m-2", content: "HEARTBEAT_OK" });
    expect(dedupeChatMessages([first, user, second])).toEqual([first, user, second]);
  });
});

describe("appendChatMessage", () => {
  test("ignores a replayed inbound message", () => {
    const first = msg({ id: "m-1", content: "reply" });
    expect(appendChatMessage([first], msg({ id: "m-1", content: "reply" }))).toEqual([first]);
  });
});

describe("streaming helpers", () => {
  test("applyStreamingDelta accumulates onto one synthetic message", () => {
    const first = applyStreamingDelta([], "stream-1", "Hel", new Date("2026-04-14T22:00:01Z"), {
      firstTokenMs: 1400,
    });
    const second = applyStreamingDelta(first, "stream-1", "lo");

    expect(second).toHaveLength(1);
    expect(second[0]?.id).toBe("stream:stream-1");
    expect(second[0]?.streaming).toBe(true);
    expect(second[0]?.content).toBe("Hello");
    expect(second[0]?.timing).toEqual({ firstTokenMs: 1400, completeMs: undefined });
  });

  test("finalizeStreamingMessage replaces the draft bubble", () => {
    const draft = applyStreamingDelta([], "stream-2", "Hello", new Date("2026-04-14T22:00:01Z"), {
      firstTokenMs: 1400,
    });
    const finalMessage = msg({
      id: "m-final",
      type: "text",
      content: "Hello world",
      timing: { completeMs: 3500 },
    });

    const finalized = finalizeStreamingMessage(draft, "stream-2", finalMessage);
    expect(finalized).toEqual([{
      ...finalMessage,
      streaming: false,
      timing: { firstTokenMs: 1400, completeMs: 3500 },
    }]);
  });

  test("readStreamingEnvelope returns stream metadata", () => {
    expect(readStreamingEnvelope({
      stream_id: "stream-3",
      text: "Hel",
      client_request_id: "req-3",
    })).toEqual({
      stream_id: "stream-3",
      text: "Hel",
      client_request_id: "req-3",
    });
  });

  test("readChatContentText joins text parts from websocket payloads", () => {
    expect(readChatContentText({
      parts: [
        { type: "text", text: "Hel" },
        { type: "text", text: "lo" },
      ],
    })).toBe("Hello");
  });
});
