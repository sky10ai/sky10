import { useEffect, useEffectEvent, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES, SANDBOX_EVENT_TYPES, subscribe } from "../lib/events";
import {
  appendChatMessage,
  applyStreamingDelta,
  finalizeStreamingMessage,
  loadChatMessages,
  readChatContentText,
  readStreamingEnvelope,
  type ChatMessage,
} from "../lib/agentChat";
import {
  agent,
  agentChatWebSocketURL,
  guestAgentChatWebSocketURL,
  sandbox,
  type AgentInfo,
  type AgentSendResult,
  type DeliveryMetadata,
  type SandboxRecord,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

interface ChatWireMessage {
  id?: string;
  session_id?: string;
  from?: string;
  to?: string;
  device_id?: string;
  message_type?: string;
  content?: unknown;
  timestamp?: string;
}

interface ChatWSFrame {
  type?: string;
  id?: string | number;
  event?: string;
  payload?: unknown;
  result?: AgentSendResult;
  error?: {
    code?: string;
    message?: string;
  };
}

interface PendingWSRequest {
  resolve: (result: AgentSendResult) => void;
  reject: (error: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
}

type ChatTransport = "connecting" | "websocket" | "fallback" | "failed";

class ChatWebSocketUnavailableError extends Error {
  constructor(message = "Chat websocket is not connected") {
    super(message);
    this.name = "ChatWebSocketUnavailableError";
  }
}

// uuid() is only available in secure contexts (HTTPS or
// localhost). Fall back to getRandomValues for plain HTTP hosts like
// http://linuxvm:9101.
function uuid(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6]! = (b[6]! & 0x0f) | 0x40;
  b[8]! = (b[8]! & 0x3f) | 0x80;
  const h = [...b].map((x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

function parseChatTimestamp(value: unknown): Date {
  if (typeof value === "string" || typeof value === "number" || value instanceof Date) {
    const parsed = new Date(value);
    if (!Number.isNaN(parsed.getTime())) {
      return parsed;
    }
  }
  return new Date();
}

function deliveryLabel(delivery?: DeliveryMetadata): string | null {
  if (!delivery) return null;
  if (delivery.status === "queued") {
    return delivery.durable_transport
      ? `Queued via ${delivery.durable_transport}`
      : "Queued";
  }
  if (delivery.status === "handed_off") {
    return delivery.durable_transport
      ? `Handed off via ${delivery.durable_transport}`
      : "Handed off";
  }
  if (delivery.status === "sent" || delivery.status === "delivered") {
    return delivery.live_transport
      ? `Sent via ${delivery.live_transport}`
      : "Sent";
  }
  return delivery.status.replaceAll("_", " ");
}

export default function AgentChat() {
  const { agentId } = useParams<{ agentId: string }>();
  const navigate = useNavigate();
  const storageKey = `sky10:chat:${agentId}`;
  const sessionKey = `sky10:session:${agentId}`;

  const [messages, setMessages] = useState<ChatMessage[]>(() => {
    return loadChatMessages(localStorage.getItem(storageKey));
  });
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [slowWaiting, setSlowWaiting] = useState(false);
  const [transport, setTransport] = useState<ChatTransport>("connecting");
  const [sessionId] = useState(() => {
    const existing = localStorage.getItem(sessionKey);
    if (existing) return existing;
    const id = uuid();
    localStorage.setItem(sessionKey, id);
    return id;
  });
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const slowWaitingTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const websocketRef = useRef<WebSocket | null>(null);
  const websocketRetryTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const nextWSRequestIDRef = useRef(1);
  const pendingWSRequestsRef = useRef(new Map<string, PendingWSRequest>());
  const [websocketRetryToken, setWebsocketRetryToken] = useState(0);

  // Fetch agent info.
  const { data, loading } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const { data: sandboxData, loading: sandboxLoading } = useRPC(() => sandbox.list(), [], {
    live: SANDBOX_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  const agentInfo: AgentInfo | undefined = data?.agents?.find(
    (a) => a.id === agentId || a.name === agentId
  );
  const agentName = agentInfo?.name;
  const agentInfoID = agentInfo?.id;
  const sandboxGuest: SandboxRecord | undefined = agentInfo
    ? sandboxData?.sandboxes?.find((record) => record.guest_device_id === agentInfo.device_id)
    : undefined;
  const sandboxLookupPending = Boolean(agentInfo && sandboxData === null && sandboxLoading);
  const requiresGuestWebSocket = Boolean(sandboxGuest);
  const guestChatReady = Boolean(sandboxGuest?.ip_address);
  const chatWebSocketURL = agentInfoID
    ? requiresGuestWebSocket
      ? (guestChatReady
          ? guestAgentChatWebSocketURL(sandboxGuest!.ip_address!, agentInfoID, sessionId)
          : undefined)
      : agentChatWebSocketURL(agentInfoID, sessionId)
    : undefined;
  const showGlobalWaiting = waiting && !messages.some((message) => message.streaming);

  const clearWaitingState = useEffectEvent(() => {
    clearTimeout(slowWaitingTimerRef.current);
    setWaiting(false);
    setSlowWaiting(false);
  });

  const startWaitingState = useEffectEvent(() => {
    setWaiting(true);
    setSlowWaiting(false);
    clearTimeout(slowWaitingTimerRef.current);
    slowWaitingTimerRef.current = setTimeout(() => setSlowWaiting(true), 30_000);
  });

  const rejectPendingWebSocketRequests = useEffectEvent((reason: string) => {
    for (const [id, pending] of pendingWSRequestsRef.current) {
      clearTimeout(pending.timeout);
      pending.reject(new Error(reason));
      pendingWSRequestsRef.current.delete(id);
    }
  });

  const applySendResult = useEffectEvent((userMessageID: string, result: AgentSendResult) => {
    setMessages((prev) =>
      prev.map((message) => (
        message.id === userMessageID
          ? {
              ...message,
              delivered: requiresGuestWebSocket
                ? result.status === "sent"
                : result.status === "sent" && result.delivery.status === "sent",
              delivery: requiresGuestWebSocket ? undefined : result.delivery,
            }
          : message
      ))
    );
    if (requiresGuestWebSocket ? result.status === "sent" : result.status === "sent" && result.delivery.status === "sent") {
      startWaitingState();
      return;
    }
    clearWaitingState();
  });

  const handleIncomingChatMessage = useEffectEvent((msg: ChatWireMessage) => {
    if (msg.session_id && msg.session_id !== sessionId) return;

    const msgType = typeof msg.message_type === "string" && msg.message_type !== ""
      ? msg.message_type
      : "message";
    const envelope = readStreamingEnvelope(msg.content);
    const timestamp = parseChatTimestamp(msg.timestamp);

    if (msgType === "done") {
      clearWaitingState();
      return;
    }
    if (msgType === "delta" && envelope.stream_id && envelope.text) {
      setMessages((prev) => applyStreamingDelta(prev, envelope.stream_id!, envelope.text!, timestamp));
      return;
    }

    clearWaitingState();

    const nextMessage: ChatMessage = {
      id: msg.id || uuid(),
      from: "agent",
      type: msgType,
      content: readChatContentText(msg.content),
      timestamp,
    };

    if (envelope.stream_id && (msgType === "text" || msgType === "error" || msgType === "message")) {
      setMessages((prev) => finalizeStreamingMessage(prev, envelope.stream_id!, nextMessage));
      return;
    }

    setMessages((prev) => appendChatMessage(prev, nextMessage));
  });

  const handleWebSocketFrame = useEffectEvent((frame: ChatWSFrame) => {
    if (frame.type === "event") {
      if (frame.event === "session.ready") {
        setTransport("websocket");
        return;
      }
      if (frame.event !== "delta" && frame.event !== "message" && frame.event !== "done" && frame.event !== "error") {
        return;
      }
      if (!frame.payload || typeof frame.payload !== "object") {
        return;
      }
      handleIncomingChatMessage({
        ...(frame.payload as ChatWireMessage),
        message_type: frame.event,
      });
      return;
    }

    if (frame.type !== "res" || frame.id == null) {
      return;
    }
    const requestID = String(frame.id);
    const pending = pendingWSRequestsRef.current.get(requestID);
    if (!pending) {
      return;
    }
    clearTimeout(pending.timeout);
    pendingWSRequestsRef.current.delete(requestID);

    if (frame.error) {
      pending.reject(new Error(frame.error.message || "Chat websocket request failed"));
      return;
    }
    if (!frame.result) {
      pending.reject(new Error("Chat websocket returned an empty response"));
      return;
    }
    pending.resolve(frame.result);
  });

  useEffect(() => {
    if (!agentInfoID) return;
    if (sandboxLookupPending) {
      setTransport("connecting");
      return;
    }
    if (requiresGuestWebSocket && !chatWebSocketURL) {
      setTransport("failed");
      return;
    }
    if (!chatWebSocketURL) return;

    setTransport("connecting");
    const socket = new WebSocket(chatWebSocketURL);
    websocketRef.current = socket;
    let disposed = false;

    socket.onopen = () => {
      if (disposed || websocketRef.current !== socket) return;
      clearTimeout(websocketRetryTimerRef.current);
      setTransport("websocket");
    };

    socket.onmessage = (event) => {
      if (typeof event.data !== "string") return;
      let frame: ChatWSFrame;
      try {
        frame = JSON.parse(event.data) as ChatWSFrame;
      } catch {
        return;
      }
      handleWebSocketFrame(frame);
    };

    socket.onerror = () => {
      if (disposed || websocketRef.current !== socket) return;
      setTransport(requiresGuestWebSocket ? "failed" : "fallback");
    };

    socket.onclose = (event) => {
      if (websocketRef.current === socket) {
        websocketRef.current = null;
      }
      rejectPendingWebSocketRequests(event.reason || "Chat websocket closed");
      if (disposed || event.code === 1000) return;
      setTransport(requiresGuestWebSocket ? "failed" : "fallback");
      clearTimeout(websocketRetryTimerRef.current);
      websocketRetryTimerRef.current = setTimeout(() => {
        setWebsocketRetryToken((value) => value + 1);
      }, 3_000);
    };

    return () => {
      disposed = true;
      clearTimeout(websocketRetryTimerRef.current);
      if (websocketRef.current === socket) {
        websocketRef.current = null;
      }
      socket.close(1000, "chat closed");
    };
  }, [agentInfoID, chatWebSocketURL, requiresGuestWebSocket, sandboxLookupPending, websocketRetryToken]);

  // Fall back to the shared SSE connection only for non-sandbox agents.
  useEffect(() => {
    if (requiresGuestWebSocket) return;
    return subscribe((event, data) => {
      if (transport === "websocket") return;
      if (event !== "agent.message") return;
      const msg = data as Record<string, unknown> | null;
      if (!msg || !msg.to) return;
      if (msg.session_id !== sessionId) return;
      if (msg.to === agentId) return;
      if (agentInfoID && (msg.to === agentInfoID || msg.to === agentName)) return;

      handleIncomingChatMessage({
        id: typeof msg.id === "string" ? msg.id : undefined,
        session_id: typeof msg.session_id === "string" ? msg.session_id : undefined,
        from: typeof msg.from === "string" ? msg.from : undefined,
        to: typeof msg.to === "string" ? msg.to : undefined,
        device_id: typeof msg.device_id === "string" ? msg.device_id : undefined,
        message_type: typeof msg.type === "string" ? msg.type : undefined,
        content: msg.content,
        timestamp: typeof msg.timestamp === "string" ? msg.timestamp : undefined,
      });
    });
  }, [agentId, agentInfoID, agentName, requiresGuestWebSocket, sessionId, transport]);

  // Auto-scroll to bottom + persist.
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    try {
      localStorage.setItem(storageKey, JSON.stringify(messages));
    } catch {
      // storage full or unavailable
    }
  }, [messages, storageKey]);

  // Focus input on mount, clean up timer on unmount.
  useEffect(() => {
    inputRef.current?.focus();
    return () => {
      clearTimeout(slowWaitingTimerRef.current);
      clearTimeout(websocketRetryTimerRef.current);
      for (const [, pending] of pendingWSRequestsRef.current) {
        clearTimeout(pending.timeout);
        pending.reject(new Error("Chat page closed"));
      }
      pendingWSRequestsRef.current.clear();
    };
  }, []);

  const sendWebSocketMessage = async (text: string): Promise<AgentSendResult> => {
    let socket = websocketRef.current;
    if (socket?.readyState === WebSocket.CONNECTING) {
      socket = await new Promise<WebSocket>((resolve, reject) => {
        const current = socket;
        if (!current) {
          reject(new ChatWebSocketUnavailableError(
            requiresGuestWebSocket
              ? "Guest chat websocket is unavailable"
              : "Chat websocket is not connected",
          ));
          return;
        }
        const timeout = window.setTimeout(() => {
          cleanup();
          reject(new ChatWebSocketUnavailableError(
            requiresGuestWebSocket
              ? "Guest chat websocket is still connecting"
              : "Chat websocket is still connecting",
          ));
        }, 5_000);
        const cleanup = () => {
          window.clearTimeout(timeout);
          current.removeEventListener("open", handleOpen);
          current.removeEventListener("error", handleFailure);
          current.removeEventListener("close", handleFailure);
        };
        const handleOpen = () => {
          cleanup();
          resolve(current);
        };
        const handleFailure = () => {
          cleanup();
          reject(new ChatWebSocketUnavailableError(
            requiresGuestWebSocket
              ? "Guest chat websocket is unavailable"
              : "Chat websocket is unavailable",
          ));
        };
        current.addEventListener("open", handleOpen);
        current.addEventListener("error", handleFailure);
        current.addEventListener("close", handleFailure);
      });
    }
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      throw new ChatWebSocketUnavailableError(
        requiresGuestWebSocket
          ? "Guest chat websocket is unavailable"
          : "Chat websocket is not connected",
      );
    }

    const requestID = `req-${nextWSRequestIDRef.current++}`;
    return await new Promise<AgentSendResult>((resolve, reject) => {
      const timeout = setTimeout(() => {
        pendingWSRequestsRef.current.delete(requestID);
        reject(new Error("Chat websocket request timed out"));
      }, 15_000);

      pendingWSRequestsRef.current.set(requestID, { resolve, reject, timeout });

      try {
        socket.send(JSON.stringify({
          type: "req",
          id: requestID,
          method: "message.send",
          params: {
            message_type: "chat",
            content: { text },
          },
        }));
      } catch (error) {
        clearTimeout(timeout);
        pendingWSRequestsRef.current.delete(requestID);
        reject(error instanceof Error ? error : new Error("Failed to send over chat websocket"));
      }
    });
  };

  const sendMessage = async () => {
    const text = input.trim();
    if (!text || !agentInfo) return;
    if (sandboxLookupPending) return;

    const userMsg: ChatMessage = {
      id: uuid(),
      from: "user",
      type: "text",
      content: text,
      timestamp: new Date(),
    };
    setMessages((prev) => [...prev, userMsg]);
    setInput("");
    setSending(true);

    try {
      let result: AgentSendResult;
      try {
        result = await sendWebSocketMessage(text);
      } catch (error) {
        if (!(error instanceof ChatWebSocketUnavailableError) || requiresGuestWebSocket) {
          throw error;
        }
        result = await agent.send({
          to: agentInfo.id,
          device_id: agentInfo.device_id,
          session_id: sessionId,
          type: "text",
          content: { text },
        });
      }
      applySendResult(userMsg.id, result);
    } catch (e) {
      clearWaitingState();
      setMessages((prev) => [
        ...prev,
        {
          id: uuid(),
          from: "agent",
          type: "error",
          content: e instanceof Error ? e.message : "Failed to send",
          timestamp: new Date(),
        },
      ]);
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  };

  if (!agentInfo) {
    if (loading) {
      return (
        <div className="flex-1 flex items-center justify-center p-8">
          <div className="text-center space-y-4">
            <Icon name="smart_toy" className="text-5xl text-secondary animate-pulse" />
            <h1 className="text-xl font-bold text-on-surface">
              Connecting...
            </h1>
          </div>
        </div>
      );
    }
    return (
      <div className="flex-1 flex items-center justify-center p-8">
        <div className="text-center space-y-4">
          <Icon name="smart_toy" className="text-5xl text-secondary" />
          <h1 className="text-xl font-bold text-on-surface">
            Agent not found
          </h1>
          <button
            onClick={() => navigate("/agents")}
            className="text-primary text-sm font-medium hover:underline"
          >
            Back to Agents
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-1 min-h-0 flex-col bg-surface">
      {/* Header */}
      <div className="flex items-center gap-4 px-8 py-4 border-b border-outline-variant/10">
        <button
          onClick={() => navigate("/agents")}
          className="text-secondary hover:text-on-surface transition-colors"
        >
          <Icon name="arrow_back" />
        </button>
        <div className="w-10 h-10 rounded-xl flex items-center justify-center bg-tertiary-fixed/30 text-tertiary">
          <Icon name="smart_toy" className="text-xl" />
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-lg font-bold text-on-surface truncate">
            {agentInfo.name}
          </h2>
          <p className="text-xs text-secondary">
            {agentInfo.device_name}{" "}
            <span className="text-outline">({agentInfo.device_id})</span>
          </p>
        </div>
        <StatusBadge pulse tone="live">
          Connected
        </StatusBadge>
      </div>

      {/* Messages */}
      <div className="flex-1 space-y-4 overflow-y-auto bg-surface-container-low/35 px-8 py-6">
        {messages.length === 0 && (
          <div className="flex-1 flex items-center justify-center text-secondary text-sm h-full">
            <div className="text-center space-y-2">
              <Icon name="chat" className="text-4xl opacity-30" />
              <p>Start a conversation with {agentInfo.name}</p>
            </div>
          </div>
        )}
        {messages.map((msg) => (
          <div key={msg.id}>
            <div
              className={`flex ${msg.from === "user" ? "justify-end" : "justify-start"}`}
            >
              <div
                className={`max-w-[70%] rounded-2xl px-4 py-3 text-sm leading-relaxed shadow-sm ${
                  msg.from === "user"
                    ? "bg-primary text-on-primary rounded-br-md whitespace-pre-wrap"
                    : msg.type === "error"
                      ? "bg-error-container/20 text-error rounded-bl-md whitespace-pre-wrap"
                      : "rounded-bl-md border border-outline-variant/20 bg-surface-container-high text-on-surface shadow-[0_10px_30px_-24px_rgba(0,0,0,0.7)]"
                }`}
              >
                {msg.from === "agent" && msg.type !== "error" ? (
                  <Markdown
                    remarkPlugins={[remarkGfm]}
                    components={{
                      p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
                      ul: ({ children }) => <ul className="list-disc ml-4 mb-2">{children}</ul>,
                      ol: ({ children }) => <ol className="list-decimal ml-4 mb-2">{children}</ol>,
                      li: ({ children }) => <li className="mb-0.5">{children}</li>,
                      code: ({ children, className }) =>
                        className ? (
                          <pre className="my-2 overflow-x-auto rounded-lg border border-outline-variant/20 bg-surface-container-low px-3 py-2 text-xs text-on-surface">
                            <code>{children}</code>
                          </pre>
                        ) : (
                          <code className="rounded bg-surface-container-low px-1.5 py-0.5 text-xs text-on-surface">{children}</code>
                        ),
                      h1: ({ children }) => <h1 className="text-base font-bold mb-2">{children}</h1>,
                      h2: ({ children }) => <h2 className="text-sm font-bold mb-1.5">{children}</h2>,
                      h3: ({ children }) => <h3 className="text-sm font-semibold mb-1">{children}</h3>,
                      strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
                      a: ({ href, children }) => (
                        <a
                          href={href}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-primary underline decoration-primary/40 underline-offset-2"
                        >
                          {children}
                        </a>
                      ),
                    }}
                  >
                    {msg.content}
                  </Markdown>
                ) : (
                  msg.content
                )}
                {msg.from === "agent" && msg.streaming && (
                  <div className="mt-3 flex items-center gap-2 text-[11px] text-secondary">
                    <div className="flex items-center gap-1">
                      <span className="w-1.5 h-1.5 rounded-full bg-secondary/60 animate-bounce" style={{ animationDelay: "0ms" }} />
                      <span className="w-1.5 h-1.5 rounded-full bg-secondary/60 animate-bounce" style={{ animationDelay: "150ms" }} />
                      <span className="w-1.5 h-1.5 rounded-full bg-secondary/60 animate-bounce" style={{ animationDelay: "300ms" }} />
                    </div>
                    <span>Streaming...</span>
                  </div>
                )}
              </div>
            </div>
            {msg.from === "user" && msg.delivered && (
              <div className="flex justify-end mt-1 pr-1">
                <span className="text-[10px] text-secondary">
                  {deliveryLabel(msg.delivery) || "Delivered"}
                </span>
              </div>
            )}
            {msg.from === "user" && !msg.delivered && msg.delivery && (
              <div className="flex justify-end mt-1 pr-1">
                <span className="text-[10px] text-secondary">
                  {deliveryLabel(msg.delivery)}
                </span>
              </div>
            )}
          </div>
        ))}
        {showGlobalWaiting && (
          <div className="flex justify-start">
            <div className="rounded-2xl rounded-bl-md border border-outline-variant/20 bg-surface-container-high px-4 py-3 shadow-sm">
              <div className="flex items-center gap-1.5">
                <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "0ms" }} />
                <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "150ms" }} />
                <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "300ms" }} />
              </div>
              {slowWaiting && (
                <p className="mt-2 text-xs text-secondary">
                  Still working. Some agent requests can take close to a minute.
                </p>
              )}
            </div>
          </div>
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* Input */}
      <div className="px-8 py-4 border-t border-outline-variant/10">
        <div className="flex items-end gap-3 max-w-4xl mx-auto">
          <textarea
            ref={inputRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={`Message ${agentInfo.name}...`}
            rows={1}
            className="flex-1 resize-none rounded-xl bg-surface-container-lowest ring-1 ring-outline-variant/10 px-4 py-3 text-sm text-on-surface placeholder:text-outline focus:outline-none focus:ring-2 focus:ring-primary/30"
            style={{ maxHeight: "8rem", overflowY: "auto" }}
          />
          <button
            onClick={sendMessage}
            disabled={!input.trim() || sending || sandboxLookupPending}
            className="w-10 h-10 rounded-xl bg-primary text-on-primary flex items-center justify-center disabled:opacity-40 transition-opacity hover:shadow-lg active:scale-95"
          >
            <Icon name={sending ? "hourglass_empty" : "send"} className="text-lg" />
          </button>
        </div>
      </div>
    </div>
  );
}
