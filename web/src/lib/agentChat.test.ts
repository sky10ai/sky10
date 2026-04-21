import { describe, expect, test } from "bun:test";
import {
  appendChatMessage,
  applyStreamingDelta,
  chatContentText,
  dedupeChatMessages,
  finalizeStreamingMessage,
  normalizeChatContent,
  readStreamingEnvelope,
  serializeChatMessages,
  type ChatMessage,
} from "./agentChat";

function msg(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: overrides.id ?? crypto.randomUUID(),
    from: overrides.from ?? "agent",
    type: overrides.type ?? "text",
    content: overrides.content ?? { text: "hello", parts: [{ type: "text", text: "hello" }] },
    timestamp: overrides.timestamp ?? new Date("2026-04-14T22:00:00Z"),
    delivered: overrides.delivered,
    delivery: overrides.delivery,
    timing: overrides.timing,
  };
}

describe("dedupeChatMessages", () => {
  test("drops duplicate inbound message ids", () => {
    const first = msg({ id: "m-1" });
    const dup = msg({ id: "m-1" });
    expect(dedupeChatMessages([first, dup])).toEqual([first]);
  });

  test("collapses adjacent identical agent replies", () => {
    const first = msg({ id: "m-1", content: { text: "HEARTBEAT_OK", parts: [{ type: "text", text: "HEARTBEAT_OK" }] } });
    const second = msg({ id: "m-2", content: { text: "HEARTBEAT_OK", parts: [{ type: "text", text: "HEARTBEAT_OK" }] } });
    expect(dedupeChatMessages([first, second])).toEqual([first]);
  });

  test("keeps identical agent replies when separated by a user turn", () => {
    const first = msg({ id: "m-1", content: { text: "HEARTBEAT_OK", parts: [{ type: "text", text: "HEARTBEAT_OK" }] } });
    const user = msg({ id: "u-1", from: "user", content: { text: "again", parts: [{ type: "text", text: "again" }] } });
    const second = msg({ id: "m-2", content: { text: "HEARTBEAT_OK", parts: [{ type: "text", text: "HEARTBEAT_OK" }] } });
    expect(dedupeChatMessages([first, user, second])).toEqual([first, user, second]);
  });
});

describe("appendChatMessage", () => {
  test("ignores a replayed inbound message", () => {
    const first = msg({ id: "m-1" });
    expect(appendChatMessage([first], msg({ id: "m-1" }))).toEqual([first]);
  });
});

describe("structured content helpers", () => {
  test("normalizeChatContent preserves non-text parts", () => {
    expect(normalizeChatContent({
      text: "see attachment",
      parts: [
        { type: "text", text: "see attachment" },
        {
          type: "image",
          filename: "diagram.png",
          media_type: "image/png",
          source: { type: "base64", data: "abc" },
        },
      ],
    })).toEqual({
      text: "see attachment",
      parts: [
        { type: "text", text: "see attachment", filename: undefined, media_type: undefined, caption: undefined },
        {
          type: "image",
          text: undefined,
          filename: "diagram.png",
          media_type: "image/png",
          caption: undefined,
          source: { type: "base64", data: "abc", url: undefined, filename: undefined, media_type: undefined },
        },
      ],
    });
  });

  test("chatContentText joins text parts", () => {
    expect(chatContentText({
      parts: [
        { type: "text", text: "Hel" },
        { type: "text", text: "lo" },
      ],
    })).toBe("Hel\n\nlo");
  });

  test("serializeChatMessages redacts base64 payloads", () => {
    const serialized = serializeChatMessages([msg({
      id: "m-redact",
      content: {
        text: "artifact ready",
        parts: [
          { type: "text", text: "artifact ready" },
          {
            type: "image",
            filename: "artifact.png",
            media_type: "image/png",
            source: {
              type: "base64",
              data: "iVBORw0KGgo=",
              filename: "artifact.png",
              media_type: "image/png",
            },
          },
        ],
      },
    })]);

    expect(serialized).toContain("artifact.png");
    expect(serialized).not.toContain("iVBORw0KGgo=");
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
    expect(second[0]?.content).toEqual({
      text: "Hello",
      parts: [{ type: "text", text: "Hello" }],
    });
    expect(second[0]?.timing).toEqual({ firstTokenMs: 1400, completeMs: undefined });
  });

  test("finalizeStreamingMessage replaces the draft bubble", () => {
    const draft = applyStreamingDelta([], "stream-2", "Hello", new Date("2026-04-14T22:00:01Z"), {
      firstTokenMs: 1400,
    });
    const finalMessage = msg({
      id: "m-final",
      type: "chat",
      content: {
        text: "Hello world",
        parts: [
          { type: "text", text: "Hello world" },
          {
            type: "file",
            filename: "artifact.txt",
            media_type: "text/plain",
            source: { type: "url", url: "https://example.com/artifact.txt" },
          },
        ],
      },
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
});
