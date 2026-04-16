import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES, subscribe } from "../lib/events";
import { appendChatMessage, loadChatMessages, type ChatMessage } from "../lib/agentChat";
import { agent, type AgentInfo, type AgentSendResult, type DeliveryMetadata } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

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

  // Fetch agent info.
  const { data, loading } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  const agentInfo: AgentInfo | undefined = data?.agents?.find(
    (a) => a.id === agentId || a.name === agentId
  );

  // Keep a ref so the SSE callback always sees the latest agentInfo.
  const agentInfoRef = useRef(agentInfo);
  useEffect(() => {
    agentInfoRef.current = agentInfo;
  }, [agentInfo]);

  // Subscribe to incoming messages via the shared SSE connection.
  useEffect(() => {
    return subscribe((event, data) => {
      if (event !== "agent.message") return;
      const msg = data as Record<string, unknown> | null;
      if (!msg || !msg.to) return;

      // Only show messages for this session.
      if (msg.session_id !== sessionId) return;

      // Skip echoes — outbound messages have `to` set to the agent.
      // Check both the URL param (available immediately) and the
      // resolved agent info (available after first fetch).
      if (msg.to === agentId) return;
      const ai = agentInfoRef.current;
      if (ai && (msg.to === ai.id || msg.to === ai.name)) return;

      clearTimeout(slowWaitingTimerRef.current);
      setWaiting(false);
      setSlowWaiting(false);
      setMessages((prev) => appendChatMessage(prev, {
          id: (msg.id as string) || uuid(),
          from: "agent" as const,
          type: (msg.type as string) || "text",
          content:
            (msg.content as { text?: string })?.text ||
            JSON.stringify(msg.content),
          timestamp: new Date(),
        }));
    });
  }, [sessionId, agentId]);

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
    return () => clearTimeout(slowWaitingTimerRef.current);
  }, []);

  const sendMessage = async () => {
    const text = input.trim();
    if (!text || !agentInfo) return;

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
      const result: AgentSendResult = await agent.send({
        to: agentInfo.id,
        device_id: agentInfo.device_id,
        session_id: sessionId,
        type: "text",
        content: { text },
      });
      // Mark the outbound delivery mode explicitly so queued mailbox fallback is
      // visible instead of being treated as a normal live send.
      setMessages((prev) =>
        prev.map((m) => (
          m.id === userMsg.id
            ? {
                ...m,
                delivered: result.status === "sent" && result.delivery.status === "sent",
                delivery: result.delivery,
              }
            : m
        ))
      );
      if (result.status === "sent" && result.delivery.status === "sent") {
        setWaiting(true);
        setSlowWaiting(false);
        clearTimeout(slowWaitingTimerRef.current);
        slowWaitingTimerRef.current = setTimeout(() => setSlowWaiting(true), 30_000);
      } else {
        clearTimeout(slowWaitingTimerRef.current);
        setWaiting(false);
        setSlowWaiting(false);
      }
    } catch (e) {
      clearTimeout(slowWaitingTimerRef.current);
      setWaiting(false);
      setSlowWaiting(false);
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
        {waiting && (
          <div className="flex justify-start">
            <div className="rounded-2xl rounded-bl-md border border-outline-variant/20 bg-surface-container-high px-4 py-3 shadow-sm">
              <div className="flex items-center gap-1.5">
                <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "0ms" }} />
                <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "150ms" }} />
                <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "300ms" }} />
              </div>
              {slowWaiting && (
                <p className="mt-2 text-xs text-secondary">
                  Still working. Hermes searches can take close to a minute.
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
            disabled={!input.trim() || sending}
            className="w-10 h-10 rounded-xl bg-primary text-on-primary flex items-center justify-center disabled:opacity-40 transition-opacity hover:shadow-lg active:scale-95"
          >
            <Icon name={sending ? "hourglass_empty" : "send"} className="text-lg" />
          </button>
        </div>
      </div>
    </div>
  );
}
