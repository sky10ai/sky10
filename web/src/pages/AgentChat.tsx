import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "react-router";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES } from "../lib/events";
import { agent, AgentInfo } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

interface ChatMessage {
  id: string;
  from: "user" | "agent";
  type: string;
  content: string;
  timestamp: Date;
  delivered?: boolean;
}

export default function AgentChat() {
  const { agentId } = useParams<{ agentId: string }>();
  const navigate = useNavigate();
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [sessionId] = useState(() => crypto.randomUUID());
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const waitingTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  // Fetch agent info.
  const { data } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  const agentInfo: AgentInfo | undefined = data?.agents?.find(
    (a) => a.id === agentId || a.name === agentId
  );

  // Subscribe to incoming messages via SSE.
  useEffect(() => {
    const es = new EventSource("/rpc/events");
    es.addEventListener("agent.message", (e) => {
      try {
        const parsed = JSON.parse(e.data);
        const msg = parsed.data ?? parsed;
        if (!msg || !msg.to) return;

        // Only show messages for this session.
        if (msg.session_id !== sessionId) return;

        setWaiting(false);
        setMessages((prev) => [
          ...prev,
          {
            id: msg.id || crypto.randomUUID(),
            from: "agent" as const,
            type: msg.type || "text",
            content: msg.content?.text || JSON.stringify(msg.content),
            timestamp: new Date(),
          },
        ]);
      } catch {
        // ignore parse errors
      }
    });
    return () => es.close();
  }, [sessionId]);

  // Auto-scroll to bottom.
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // Focus input on mount, clean up timer on unmount.
  useEffect(() => {
    inputRef.current?.focus();
    return () => clearTimeout(waitingTimerRef.current);
  }, []);

  const sendMessage = async () => {
    const text = input.trim();
    if (!text || !agentInfo) return;

    const userMsg: ChatMessage = {
      id: crypto.randomUUID(),
      from: "user",
      type: "text",
      content: text,
      timestamp: new Date(),
    };
    setMessages((prev) => [...prev, userMsg]);
    setInput("");
    setSending(true);

    try {
      await agent.send({
        to: agentInfo.id,
        device_id: agentInfo.device_id,
        session_id: sessionId,
        type: "text",
        content: { text },
      });
      // Mark as delivered, show typing indicator with timeout.
      setMessages((prev) =>
        prev.map((m) => (m.id === userMsg.id ? { ...m, delivered: true } : m))
      );
      setWaiting(true);
      clearTimeout(waitingTimerRef.current);
      waitingTimerRef.current = setTimeout(() => setWaiting(false), 30_000);
    } catch (e) {
      setMessages((prev) => [
        ...prev,
        {
          id: crypto.randomUUID(),
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
    <div className="flex flex-col flex-1 min-h-0">
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
      <div className="flex-1 overflow-y-auto px-8 py-6 space-y-4">
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
                className={`max-w-[70%] rounded-2xl px-4 py-3 text-sm whitespace-pre-wrap ${
                  msg.from === "user"
                    ? "bg-primary text-on-primary rounded-br-md"
                    : msg.type === "error"
                      ? "bg-error-container/20 text-error rounded-bl-md"
                      : "bg-surface-container-lowest ring-1 ring-outline-variant/10 text-on-surface rounded-bl-md"
                }`}
              >
                {msg.content}
              </div>
            </div>
            {msg.from === "user" && msg.delivered && (
              <div className="flex justify-end mt-1 pr-1">
                <span className="text-[10px] text-secondary">Delivered</span>
              </div>
            )}
          </div>
        ))}
        {waiting && (
          <div className="flex justify-start">
            <div className="bg-surface-container-lowest ring-1 ring-outline-variant/10 rounded-2xl rounded-bl-md px-4 py-3 flex items-center gap-1.5">
              <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "0ms" }} />
              <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "150ms" }} />
              <span className="w-2 h-2 bg-secondary/50 rounded-full animate-bounce" style={{ animationDelay: "300ms" }} />
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
