import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter, Route, Routes } from "react-router";
import AgentChat from "./AgentChat";

type FetchCall = {
  method: string;
  params: unknown;
};

const jsdom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "http://localhost/",
});
const { window } = jsdom;
for (const key of Object.getOwnPropertyNames(window)) {
  if (key in globalThis) continue;
  Object.defineProperty(globalThis, key, {
    configurable: true,
    enumerable: true,
    value: (window as unknown as Record<string, unknown>)[key],
    writable: true,
  });
}
globalThis.window = window as unknown as typeof globalThis.window;
globalThis.document = window.document;
globalThis.navigator = window.navigator;
globalThis.localStorage = window.localStorage;
globalThis.Event = window.Event as typeof Event;
globalThis.MessageEvent = window.MessageEvent as typeof MessageEvent;
globalThis.HTMLElement = window.HTMLElement as typeof HTMLElement;
globalThis.File = window.File as typeof File;
globalThis.Blob = window.Blob as typeof Blob;
if (typeof globalThis.requestAnimationFrame !== "function") {
  globalThis.requestAnimationFrame = ((callback: FrameRequestCallback) => {
    return window.setTimeout(() => callback(Date.now()), 0);
  }) as typeof requestAnimationFrame;
}
if (typeof globalThis.cancelAnimationFrame !== "function") {
  globalThis.cancelAnimationFrame = ((id: number) => {
    window.clearTimeout(id);
  }) as typeof cancelAnimationFrame;
}
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
if (typeof globalThis.btoa !== "function") {
  globalThis.btoa = (value: string) => Buffer.from(value, "binary").toString("base64");
}

const originalFetch = globalThis.fetch;
const originalEventSource = globalThis.EventSource;
const originalFileReader = globalThis.FileReader;
const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
const originalAttachEvent = (HTMLElement.prototype as { attachEvent?: ((...args: unknown[]) => void) | undefined }).attachEvent;
const originalDetachEvent = (HTMLElement.prototype as { detachEvent?: ((...args: unknown[]) => void) | undefined }).detachEvent;
const originalWebSocket = globalThis.WebSocket;

let container: HTMLDivElement | null = null;
let root: Root | null = null;
let fetchCalls: FetchCall[] = [];

class FakeEventSource {
  onmessage: ((event: MessageEvent) => void) | null = null;

  constructor(public url: string) {}

  addEventListener(_type: string, _listener: (event: MessageEvent) => void) {}

  removeEventListener(_type: string, _listener: (event: MessageEvent) => void) {}

  close() {}
}

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  sentFrames: unknown[] = [];
  private listeners = new Map<string, Set<(event: Event | MessageEvent | CloseEvent) => void>>();

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
    setTimeout(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.dispatch("open", new Event("open"));
      this.emitFrame({
        type: "event",
        event: "session.ready",
        payload: {
          session_id: new URL(url).searchParams.get("session_id") ?? "session-test",
          agent: { id: "A-agent", name: "agent-1" },
        },
      });
    }, 0);
  }

  addEventListener(type: string, listener: (event: Event | MessageEvent | CloseEvent) => void) {
    const existing = this.listeners.get(type) ?? new Set();
    existing.add(listener);
    this.listeners.set(type, existing);
  }

  removeEventListener(type: string, listener: (event: Event | MessageEvent | CloseEvent) => void) {
    this.listeners.get(type)?.delete(listener);
  }

  send(data: string) {
    const frame = JSON.parse(data) as Record<string, unknown>;
    this.sentFrames.push(frame);
    setTimeout(() => {
      this.emitFrame({
        type: "res",
        id: frame.id,
        result: {
          id: "msg-sent",
          status: "sent",
          delivery: {
            policy: "live_only",
            status: "sent",
            live_transport: "guest_websocket",
            live_attempted: true,
            durable_used: false,
          },
        },
      });
    }, 0);
  }

  close(code = 1000, reason = "") {
    this.readyState = FakeWebSocket.CLOSED;
    this.dispatch("close", { code, reason } as CloseEvent);
  }

  emitFrame(frame: unknown) {
    this.dispatch("message", { data: JSON.stringify(frame) } as MessageEvent);
  }

  private dispatch(type: string, event: Event | MessageEvent | CloseEvent) {
    if (type === "open") {
      this.onopen?.(event as Event);
    } else if (type === "message") {
      this.onmessage?.(event as MessageEvent);
    } else if (type === "error") {
      this.onerror?.(event as Event);
    } else if (type === "close") {
      this.onclose?.(event as CloseEvent);
    }
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event);
    }
  }

  static latest(): FakeWebSocket {
    const latest = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
    if (!latest) {
      throw new Error("expected a fake WebSocket instance");
    }
    return latest;
  }

  static reset() {
    FakeWebSocket.instances = [];
  }
}

function rpcResult(id: number, result: unknown) {
  return new Response(
    JSON.stringify({ jsonrpc: "2.0", id, result }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

function setupFetch() {
  fetchCalls = [];
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse(String(init?.body ?? "{}")) as {
      id: number;
      method: string;
      params?: unknown;
    };
    fetchCalls.push({ method: body.method, params: body.params });

    switch (body.method) {
      case "agent.list":
        return rpcResult(body.id, {
          agents: [{
            id: "A-agent",
            name: "agent-1",
            device_id: "D-guest",
            device_name: "Guest VM",
            skills: ["code"],
            status: "connected",
            connected_at: "2026-04-18T12:00:00Z",
          }],
          count: 1,
        });
      case "sandbox.list":
        return rpcResult(body.id, {
          sandboxes: [{
            name: "hermes-dev",
            slug: "hermes-dev",
            provider: "lima",
            template: "hermes",
            status: "running",
            ip_address: "10.0.0.2",
            guest_device_id: "D-guest",
            created_at: "2026-04-18T12:00:00Z",
            updated_at: "2026-04-18T12:00:00Z",
          }],
        });
      case "agent.send":
        return rpcResult(body.id, {
          id: "msg-sent",
          status: "sent",
          delivery: {
            policy: "live_only",
            status: "sent",
            live_transport: "local_registry",
            live_attempted: true,
            durable_used: false,
          },
        });
      default:
        throw new Error(`unexpected RPC method ${body.method}`);
    }
  }) as typeof fetch;
}

async function renderAgentChatPage() {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root!.render(
      <MemoryRouter initialEntries={["/agents/agent-1"]}>
        <Routes>
          <Route path="/agents/:agentId" element={<AgentChat />} />
        </Routes>
      </MemoryRouter>,
    );
  });
  await waitFor(() => container?.textContent?.includes("agent-1") === true, "agent page to load");
  return container!;
}

async function waitFor(predicate: () => boolean, label: string) {
  const deadline = Date.now() + 2000;
  while (Date.now() < deadline) {
    await act(async () => {
      await Promise.resolve();
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    if (predicate()) {
      return;
    }
  }
  throw new Error(`timed out waiting for ${label}`);
}

describe("AgentChat page", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    localStorage.clear();
    FakeWebSocket.reset();
    setupFetch();
    globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    globalThis.FileReader = window.FileReader as unknown as typeof FileReader;
    HTMLElement.prototype.scrollIntoView = () => {};
    (HTMLElement.prototype as { attachEvent?: (...args: unknown[]) => void }).attachEvent = () => {};
    (HTMLElement.prototype as { detachEvent?: (...args: unknown[]) => void }).detachEvent = () => {};
  });

  afterEach(async () => {
    if (root) {
      await act(async () => {
        root?.unmount();
      });
    }
    root = null;
    container?.remove();
    container = null;
    fetchCalls = [];
    localStorage.clear();
    document.body.innerHTML = "";
    globalThis.fetch = originalFetch;
    globalThis.EventSource = originalEventSource;
    globalThis.WebSocket = originalWebSocket;
    globalThis.FileReader = originalFileReader;
    HTMLElement.prototype.scrollIntoView = originalScrollIntoView;
    (HTMLElement.prototype as { attachEvent?: ((...args: unknown[]) => void) | undefined }).attachEvent = originalAttachEvent;
    (HTMLElement.prototype as { detachEvent?: ((...args: unknown[]) => void) | undefined }).detachEvent = originalDetachEvent;
    FakeWebSocket.reset();
  });

  test("sends text plus image and file attachments over the guest websocket", async () => {
    localStorage.setItem("sky10:session:agent-1", "session-files");
    const page = await renderAgentChatPage();
    await waitFor(() => FakeWebSocket.instances.length > 0, "guest websocket");

    const attachButton = page.querySelector('button[aria-label="Attach photo or file"]') as HTMLButtonElement | null;
    if (!attachButton) {
      throw new Error("expected visible attach button");
    }
    expect(attachButton.textContent).toContain("Attach");

    const fileInput = page.querySelector('input[type="file"]') as HTMLInputElement | null;
    if (!fileInput) {
      throw new Error("expected hidden file input");
    }

    const imageFile = new File(
      [Uint8Array.from([0x89, 0x50, 0x4e, 0x47])],
      "diagram.png",
      { type: "image/png" },
    );
    const textFile = new File(["hello from attachment"], "notes.txt", { type: "text/plain" });

    Object.defineProperty(fileInput, "files", {
      configurable: true,
      value: [imageFile, textFile],
    });

    await act(async () => {
      fileInput.dispatchEvent(new Event("change", { bubbles: true }));
    });

    await waitFor(
      () => page.textContent?.includes("diagram.png") === true && page.textContent?.includes("notes.txt") === true,
      "attachment chips",
    );

    const sendButton = page.querySelector("button.bg-primary") as HTMLButtonElement | null;
    if (!sendButton) {
      throw new Error("expected send button");
    }
    await waitFor(() => sendButton.disabled === false, "send button enabled");

    await act(async () => {
      sendButton.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });

    await waitFor(
      () => FakeWebSocket.instances.some((socket) => socket.sentFrames.length > 0),
      "websocket request",
    );

    const sentSocket = FakeWebSocket.instances.find((socket) => socket.sentFrames.length > 0);
    if (!sentSocket) {
      throw new Error("expected websocket request frame");
    }
    const frame = sentSocket.sentFrames[0] as {
      method: string;
      params: {
        message_type: string;
        content: {
          text?: string;
          parts?: Array<{ type: string; filename?: string; source?: { type: string; data?: string } }>;
        };
      };
    };

    expect(frame.method).toBe("message.send");
    expect(frame.params.message_type).toBe("chat");
    expect(frame.params.content.parts?.map((part) => part.type)).toEqual(["image", "file"]);
    expect(frame.params.content.parts?.[0]?.filename).toBe("diagram.png");
    expect(frame.params.content.parts?.[0]?.source?.type).toBe("base64");
    expect(frame.params.content.parts?.[0]?.source?.data).toBeTruthy();
    expect(frame.params.content.parts?.[1]?.filename).toBe("notes.txt");
    expect(frame.params.content.parts?.[1]?.source?.type).toBe("base64");
    expect(frame.params.content.parts?.[1]?.source?.data).toBeTruthy();
    expect(fetchCalls.some((call) => call.method === "agent.send")).toBe(false);

    await waitFor(() => page.textContent?.includes("Delivered") === true, "delivered state");
  });

  test("accepts dropped image attachments", async () => {
    localStorage.setItem("sky10:session:agent-1", "session-drop");
    const page = await renderAgentChatPage();

    const imageFile = new File(
      [Uint8Array.from([0x89, 0x50, 0x4e, 0x47])],
      "drop-image.png",
      { type: "image/png" },
    );

    const textarea = page.querySelector("textarea");
    const dropZone = textarea?.closest("div")?.parentElement ?? null;
    if (!dropZone) {
      throw new Error("expected attachment drop zone");
    }

    const dragOverEvent = new Event("dragover", { bubbles: true, cancelable: true }) as Event & {
      dataTransfer?: { files: File[] };
    };
    Object.defineProperty(dragOverEvent, "dataTransfer", {
      configurable: true,
      value: { files: [imageFile] },
    });
    const dropEvent = new Event("drop", { bubbles: true, cancelable: true }) as Event & {
      dataTransfer?: { files: File[] };
    };
    Object.defineProperty(dropEvent, "dataTransfer", {
      configurable: true,
      value: { files: [imageFile] },
    });

    await act(async () => {
      dropZone.dispatchEvent(dragOverEvent);
      dropZone.dispatchEvent(dropEvent);
    });

    await waitFor(() => page.textContent?.includes("drop-image.png") === true, "dropped image chip");
  });

  test("accepts pasted image attachments", async () => {
    localStorage.setItem("sky10:session:agent-1", "session-paste");
    const page = await renderAgentChatPage();

    const imageFile = new File(
      [Uint8Array.from([0x89, 0x50, 0x4e, 0x47])],
      "paste-image.png",
      { type: "image/png" },
    );

    const textarea = page.querySelector("textarea");
    if (!textarea) {
      throw new Error("expected message textarea");
    }

    const pasteEvent = new Event("paste", { bubbles: true, cancelable: true }) as Event & {
      clipboardData?: { items: Array<{ kind: string; getAsFile: () => File | null }> };
    };
    Object.defineProperty(pasteEvent, "clipboardData", {
      configurable: true,
      value: {
        items: [{
          kind: "file",
          getAsFile: () => imageFile,
        }],
      },
    });

    await act(async () => {
      textarea.dispatchEvent(pasteEvent);
    });

    await waitFor(() => page.textContent?.includes("paste-image.png") === true, "pasted image chip");
  });

  test("renders returned image artifacts from websocket messages", async () => {
    localStorage.setItem("sky10:session:agent-1", "session-artifact-image");
    const page = await renderAgentChatPage();
    const ws = FakeWebSocket.latest();

    await act(async () => {
      ws.emitFrame({
        type: "event",
        event: "message",
        payload: {
          id: "reply-artifact",
          session_id: "session-artifact-image",
          message_type: "chat",
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
                  data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO6pZ9sAAAAASUVORK5CYII=",
                  filename: "artifact.png",
                  media_type: "image/png",
                },
              },
            ],
          },
          timestamp: "2026-04-20T12:00:00Z",
        },
      });
    });

    await waitFor(() => page.querySelector('img[alt="artifact.png"]') !== null, "artifact image");
    const image = page.querySelector('img[alt="artifact.png"]') as HTMLImageElement | null;
    expect(image).toBeTruthy();
    expect(image?.src.startsWith("data:image/png;base64,")).toBe(true);

    await waitFor(
      () => (localStorage.getItem("sky10:chat:agent-1") ?? "").includes("artifact.png"),
      "transcript persistence",
    );
    expect(localStorage.getItem("sky10:chat:agent-1") ?? "").not.toContain("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO6pZ9sAAAAASUVORK5CYII=");
  });

  test("renders returned file artifacts from websocket messages", async () => {
    localStorage.setItem("sky10:session:agent-1", "session-artifact-file");
    const page = await renderAgentChatPage();
    const ws = FakeWebSocket.latest();

    await act(async () => {
      ws.emitFrame({
        type: "event",
        event: "message",
        payload: {
          id: "reply-file",
          session_id: "session-artifact-file",
          message_type: "chat",
          content: {
            text: "download ready",
            parts: [
              { type: "text", text: "download ready" },
              {
                type: "file",
                filename: "artifact.txt",
                media_type: "text/plain",
                source: {
                  type: "url",
                  url: "https://example.com/artifact.txt",
                  filename: "artifact.txt",
                  media_type: "text/plain",
                },
              },
            ],
          },
          timestamp: "2026-04-20T12:00:00Z",
        },
      });
    });

    await waitFor(() => page.textContent?.includes("artifact.txt") === true, "artifact file card");
    const link = page.querySelector('a[download="artifact.txt"]') as HTMLAnchorElement | null;
    expect(link).toBeTruthy();
    expect(link?.href).toBe("https://example.com/artifact.txt");
  });
});
