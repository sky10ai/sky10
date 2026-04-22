import { useEffect, useEffectEvent, useRef, useState, type ChangeEvent, type ClipboardEvent, type DragEvent } from "react";
import { useNavigate, useParams } from "react-router";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES, SANDBOX_EVENT_TYPES, subscribe } from "../lib/events";
import {
  appendChatMessage,
  applyStreamingDelta,
  chatContentText,
  finalizeStreamingMessage,
  loadChatMessages,
  normalizeChatContent,
  readStreamingEnvelope,
  serializeChatMessages,
  type ChatMessage,
  type ChatMessageTiming,
} from "../lib/agentChat";
import {
  agent,
  agentChatWebSocketURL,
  guestAgentChatWebSocketURL,
  sandbox,
  type AgentInfo,
  type AgentSendResult,
  type ChatContent,
  type ChatContentPart,
  type DeliveryMetadata,
  type SandboxRecord,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

const maxAttachmentBytes = 8 * 1024 * 1024;
const maxAttachments = 4;

interface DraftAttachment {
  id: string;
  size: number;
  part: ChatContentPart;
}

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

interface PendingTurnTiming {
  startedAtMs: number;
  firstTokenMs?: number;
  completeMs?: number;
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

function formatLatency(ms?: number): string | null {
  if (typeof ms !== "number" || !Number.isFinite(ms) || ms < 0) return null;
  if (ms < 1_000) return `${Math.round(ms)}ms`;
  return `${(ms / 1_000).toFixed(ms < 10_000 ? 1 : 0)}s`;
}

function timingLabel(timing?: ChatMessageTiming): string | null {
  if (!timing) return null;
  const firstToken = formatLatency(timing.firstTokenMs);
  const complete = formatLatency(timing.completeMs);
  if (firstToken && complete) return `First token ${firstToken} • Complete ${complete}`;
  if (firstToken) return `First token ${firstToken}`;
  if (complete) return `Complete ${complete}`;
  return null;
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size >= 10 || unitIndex === 0 ? Math.round(size) : size.toFixed(1)} ${units[unitIndex]}`;
}

function guessMediaType(filename: string): string {
  const lower = filename.toLowerCase();
  if (lower.endsWith(".png")) return "image/png";
  if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) return "image/jpeg";
  if (lower.endsWith(".gif")) return "image/gif";
  if (lower.endsWith(".webp")) return "image/webp";
  if (lower.endsWith(".svg")) return "image/svg+xml";
  if (lower.endsWith(".pdf")) return "application/pdf";
  if (lower.endsWith(".txt")) return "text/plain";
  if (lower.endsWith(".md")) return "text/markdown";
  if (lower.endsWith(".json")) return "application/json";
  return "application/octet-stream";
}

function inferPartKind(part: ChatContentPart): "image" | "audio" | "video" | "file" {
  const mediaType = part.media_type || part.source?.media_type || "";
  if (part.type === "image" || mediaType.startsWith("image/")) return "image";
  if (part.type === "audio" || mediaType.startsWith("audio/")) return "audio";
  if (part.type === "video" || mediaType.startsWith("video/")) return "video";
  return "file";
}

function iconNameForPart(part: ChatContentPart): string {
  switch (inferPartKind(part)) {
    case "image":
      return "image";
    case "audio":
      return "audio_file";
    case "video":
      return "videocam";
    default:
      return "draft";
  }
}

function attachmentHref(part: ChatContentPart): string | null {
  if (part.source?.type === "url" && part.source.url) {
    return part.source.url;
  }
  if (part.source?.type === "base64" && part.source.data) {
    const mediaType = part.media_type || part.source.media_type || guessMediaType(part.filename || part.source.filename || "attachment.bin");
    return `data:${mediaType};base64,${part.source.data}`;
  }
  return null;
}

function attachmentName(part: ChatContentPart): string {
  return part.filename || part.source?.filename || "attachment";
}

function readFileAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error(`Could not read ${file.name}`));
        return;
      }
      resolve(reader.result);
    };
    reader.onerror = () => reject(new Error(`Could not read ${file.name}`));
    reader.readAsDataURL(file);
  });
}

async function fileToDraftAttachment(file: File): Promise<DraftAttachment> {
  const dataUrl = await readFileAsDataURL(file);
  const comma = dataUrl.indexOf(",");
  const data = comma === -1 ? dataUrl : dataUrl.slice(comma + 1);
  const mediaType = file.type || guessMediaType(file.name);
  const kind = mediaType.startsWith("image/")
    ? "image"
    : mediaType.startsWith("audio/")
      ? "audio"
      : mediaType.startsWith("video/")
        ? "video"
        : "file";

  return {
    id: uuid(),
    size: file.size,
    part: {
      type: kind,
      filename: file.name,
      media_type: mediaType,
      source: {
        type: "base64",
        data,
        filename: file.name,
        media_type: mediaType,
      },
    },
  };
}

function MessageText({ text }: { text: string }) {
  if (!text) return null;
  return (
    <Markdown
      remarkPlugins={[remarkGfm]}
      components={{
        p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
        ul: ({ children }) => <ul className="mb-2 ml-4 list-disc">{children}</ul>,
        ol: ({ children }) => <ol className="mb-2 ml-4 list-decimal">{children}</ol>,
        li: ({ children }) => <li className="mb-0.5">{children}</li>,
        code: ({ children, className }) =>
          className ? (
            <pre className="my-2 overflow-x-auto rounded-lg border border-outline-variant/20 bg-surface-container-low px-3 py-2 text-xs text-on-surface">
              <code>{children}</code>
            </pre>
          ) : (
            <code className="rounded bg-surface-container-low px-1.5 py-0.5 text-xs text-on-surface">{children}</code>
          ),
        h1: ({ children }) => <h1 className="mb-2 text-base font-bold">{children}</h1>,
        h2: ({ children }) => <h2 className="mb-1.5 text-sm font-bold">{children}</h2>,
        h3: ({ children }) => <h3 className="mb-1 text-sm font-semibold">{children}</h3>,
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
      {text}
    </Markdown>
  );
}

function AttachmentView({ part, userBubble }: { part: ChatContentPart; userBubble: boolean }) {
  const kind = inferPartKind(part);
  const href = attachmentHref(part);
  const filename = attachmentName(part);
  const mediaType = part.media_type || part.source?.media_type || "";
  const frameTone = userBubble
    ? "border-white/20 bg-white/10 text-on-primary"
    : "border-outline-variant/20 bg-surface-container-low text-on-surface";

  if (kind === "image" && href) {
    return (
      <a
        href={href}
        download={filename}
        target="_blank"
        rel="noopener noreferrer"
        className="block overflow-hidden rounded-xl border border-white/10 bg-black/10"
      >
        <img src={href} alt={filename} className="max-h-72 w-full object-contain" />
      </a>
    );
  }

  if (kind === "audio" && href) {
    return (
      <div className={`rounded-xl border p-3 ${frameTone}`}>
        <audio controls src={href} className="w-full" />
        <div className="mt-2 text-xs opacity-80">{filename}</div>
      </div>
    );
  }

  if (kind === "video" && href) {
    return (
      <div className={`rounded-xl border p-3 ${frameTone}`}>
        <video controls src={href} className="max-h-72 w-full rounded-lg" />
        <div className="mt-2 text-xs opacity-80">{filename}</div>
      </div>
    );
  }

  return (
    <div className={`rounded-xl border px-3 py-2 ${frameTone}`}>
      <div className="flex items-start gap-3">
        <div className="mt-0.5">
          <Icon name={iconNameForPart(part)} className="text-lg" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{filename}</div>
          <div className="truncate text-xs opacity-70">{mediaType || kind}</div>
          {part.caption && <div className="mt-1 text-xs opacity-80">{part.caption}</div>}
        </div>
        {href && (
          <a
            href={href}
            download={filename}
            target="_blank"
            rel="noopener noreferrer"
            className="shrink-0 rounded-md border border-current/20 px-2 py-1 text-xs font-medium hover:bg-black/5"
          >
            Open
          </a>
        )}
      </div>
    </div>
  );
}

function MessageBody({ message }: { message: ChatMessage }) {
  const text = chatContentText(message.content);
  const attachments = (message.content.parts ?? []).filter((part) => part.type !== "text");
  const isUser = message.from === "user";

  return (
    <div className="space-y-3">
      {text && (
        isUser || message.type === "error"
          ? <div className="whitespace-pre-wrap">{text}</div>
          : <MessageText text={text} />
      )}
      {attachments.map((part, index) => (
        <AttachmentView
          key={`${message.id}:${part.type}:${attachmentName(part)}:${index}`}
          part={part}
          userBubble={isUser}
        />
      ))}
    </div>
  );
}

export default function AgentChat() {
  const { agentId } = useParams<{ agentId: string }>();
  const navigate = useNavigate();
  const storageKey = `sky10:chat:${agentId}`;
  const sessionKey = `sky10:session:${agentId}`;
  const initialMessages = loadChatMessages(localStorage.getItem(storageKey));

  const [messages, setMessages] = useState<ChatMessage[]>(initialMessages);
  const [input, setInput] = useState("");
  const [attachments, setAttachments] = useState<DraftAttachment[]>([]);
  const [composerError, setComposerError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [slowWaiting, setSlowWaiting] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [transport, setTransport] = useState<ChatTransport>("connecting");
  const [sessionId] = useState(() => {
    const existing = localStorage.getItem(sessionKey);
    if (existing && initialMessages.length > 0) return existing;
    const id = uuid();
    localStorage.setItem(sessionKey, id);
    return id;
  });
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const slowWaitingTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const websocketRef = useRef<WebSocket | null>(null);
  const websocketRetryTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
  const nextWSRequestIDRef = useRef(1);
  const pendingWSRequestsRef = useRef(new Map<string, PendingWSRequest>());
  const pendingTurnTimingsRef = useRef(new Map<string, PendingTurnTiming>());
  const [websocketRetryToken, setWebsocketRetryToken] = useState(0);

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

  const noteFirstTokenTiming = useEffectEvent((clientRequestID?: string): ChatMessageTiming | undefined => {
    if (!clientRequestID) return undefined;
    const pending = pendingTurnTimingsRef.current.get(clientRequestID);
    if (!pending) return undefined;
    if (pending.firstTokenMs == null) {
      pending.firstTokenMs = Math.max(0, Date.now() - pending.startedAtMs);
    }
    return {
      firstTokenMs: pending.firstTokenMs,
      completeMs: pending.completeMs,
    };
  });

  const noteCompletionTiming = useEffectEvent((clientRequestID?: string): ChatMessageTiming | undefined => {
    if (!clientRequestID) return undefined;
    const pending = pendingTurnTimingsRef.current.get(clientRequestID);
    if (!pending) return undefined;
    if (pending.firstTokenMs == null) {
      pending.firstTokenMs = Math.max(0, Date.now() - pending.startedAtMs);
    }
    if (pending.completeMs == null) {
      pending.completeMs = Math.max(0, Date.now() - pending.startedAtMs);
    }
    const timing: ChatMessageTiming = {
      firstTokenMs: pending.firstTokenMs,
      completeMs: pending.completeMs,
    };
    pendingTurnTimingsRef.current.delete(clientRequestID);
    return timing;
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
      const timing = noteFirstTokenTiming(envelope.client_request_id);
      setMessages((prev) => applyStreamingDelta(prev, envelope.stream_id!, envelope.text!, timestamp, timing));
      return;
    }

    clearWaitingState();

    const nextMessage: ChatMessage = {
      id: msg.id || uuid(),
      from: "agent",
      type: msgType,
      content: normalizeChatContent(msg.content),
      timestamp,
      timing: noteCompletionTiming(envelope.client_request_id),
    };

    if (envelope.stream_id && msgType !== "delta" && msgType !== "done") {
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

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    try {
      localStorage.setItem(storageKey, serializeChatMessages(messages));
    } catch {
      // storage full or unavailable
    }
  }, [messages, storageKey]);

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
      pendingTurnTimingsRef.current.clear();
    };
  }, []);

  async function queueFiles(files: File[]) {
    if (files.length === 0) return;

    setComposerError(null);
    const slotsLeft = maxAttachments - attachments.length;
    if (slotsLeft <= 0) {
      setComposerError(`You can attach up to ${maxAttachments} files per message.`);
      return;
    }

    const accepted = files.slice(0, slotsLeft);
    if (files.length > accepted.length) {
      setComposerError(`Only the first ${slotsLeft} file${slotsLeft === 1 ? "" : "s"} were added.`);
    }

    const next: DraftAttachment[] = [];
    for (const file of accepted) {
      if (file.size > maxAttachmentBytes) {
        setComposerError(`${file.name} is too large. Limit ${formatBytes(maxAttachmentBytes)}.`);
        continue;
      }
      next.push(await fileToDraftAttachment(file));
    }
    if (next.length > 0) {
      setAttachments((prev) => [...prev, ...next]);
    }
  }

  async function handleFileSelection(event: ChangeEvent<HTMLInputElement>) {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    await queueFiles(files);
  }

  async function handleDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault();
    setDragging(false);
    const files = Array.from(event.dataTransfer.files ?? []);
    await queueFiles(files);
  }

  async function handlePaste(event: ClipboardEvent<HTMLTextAreaElement>) {
    const files = Array.from(event.clipboardData?.items ?? [])
      .map((item) => (item.kind === "file" ? item.getAsFile() : null))
      .filter((file): file is File => file !== null);
    if (files.length === 0) return;
    event.preventDefault();
    await queueFiles(files);
  }

  function removeAttachment(id: string) {
    setAttachments((prev) => prev.filter((item) => item.id !== id));
  }

  const sendWebSocketMessage = async (content: ChatContent, clientRequestID: string): Promise<AgentSendResult> => {
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
            content: {
              text: content.text,
              parts: content.parts,
              client_request_id: clientRequestID,
            },
          },
        }));
      } catch (error) {
        clearTimeout(timeout);
        pendingWSRequestsRef.current.delete(requestID);
        reject(error instanceof Error ? error : new Error("Failed to send over chat websocket"));
      }
    });
  };

  async function sendMessage() {
    const text = input.trim();
    if ((!text && attachments.length === 0) || !agentInfo) return;
    if (sandboxLookupPending) return;

    const parts: ChatContentPart[] = [];
    if (text) {
      parts.push({ type: "text", text });
    }
    for (const attachment of attachments) {
      parts.push(attachment.part);
    }

    const content: ChatContent = {
      text: text || undefined,
      parts,
    };
    const messageType = attachments.length > 0 ? "chat" : "text";
    const userMsg: ChatMessage = {
      id: uuid(),
      from: "user",
      type: messageType,
      content,
      timestamp: new Date(),
    };

    setMessages((prev) => [...prev, userMsg]);
    setInput("");
    setAttachments([]);
    setComposerError(null);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    setSending(true);
    const clientRequestID = uuid();
    pendingTurnTimingsRef.current.set(clientRequestID, { startedAtMs: Date.now() });

    try {
      let result: AgentSendResult;
      try {
        result = await sendWebSocketMessage(content, clientRequestID);
      } catch (error) {
        if (!(error instanceof ChatWebSocketUnavailableError) || requiresGuestWebSocket) {
          throw error;
        }
        result = await agent.send({
          to: agentInfo.id,
          device_id: agentInfo.device_id,
          session_id: sessionId,
          type: messageType,
          content: attachments.length > 0
            ? {
                ...content,
                client_request_id: clientRequestID,
              }
            : {
                text,
                client_request_id: clientRequestID,
              },
        });
      }
      applySendResult(userMsg.id, result);
    } catch (error) {
      pendingTurnTimingsRef.current.delete(clientRequestID);
      clearWaitingState();
      const message = error instanceof Error ? error.message : "Failed to send";
      setMessages((prev) => [
        ...prev,
        {
          id: uuid(),
          from: "agent",
          type: "error",
          content: { text: message, parts: [{ type: "text", text: message }] },
          timestamp: new Date(),
        },
      ]);
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  }

  const canSend = (!!input.trim() || attachments.length > 0) && !sending;

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void sendMessage();
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
                className={`max-w-[75%] rounded-2xl px-4 py-3 text-sm leading-relaxed shadow-sm ${
                  msg.from === "user"
                    ? "bg-primary text-on-primary rounded-br-md"
                    : msg.type === "error"
                      ? "bg-error-container/20 text-error rounded-bl-md"
                      : "rounded-bl-md border border-outline-variant/20 bg-surface-container-high text-on-surface shadow-[0_10px_30px_-24px_rgba(0,0,0,0.7)]"
                }`}
              >
                <MessageBody message={msg} />
                {msg.from === "agent" && (msg.streaming || msg.timing) && (
                  <div className="mt-3 flex flex-wrap items-center gap-2 text-[11px] text-secondary">
                    {msg.streaming && (
                      <>
                        <div className="flex items-center gap-1">
                          <span className="w-1.5 h-1.5 rounded-full bg-secondary/60 animate-bounce" style={{ animationDelay: "0ms" }} />
                          <span className="w-1.5 h-1.5 rounded-full bg-secondary/60 animate-bounce" style={{ animationDelay: "150ms" }} />
                          <span className="w-1.5 h-1.5 rounded-full bg-secondary/60 animate-bounce" style={{ animationDelay: "300ms" }} />
                        </div>
                        <span>Streaming...</span>
                      </>
                    )}
                    {timingLabel(msg.timing) && (
                      <span>{timingLabel(msg.timing)}</span>
                    )}
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

      <div className="px-8 py-4 border-t border-outline-variant/10">
        <div
          className={`mx-auto max-w-4xl rounded-2xl border border-outline-variant/10 bg-surface-container p-3 transition ${
            dragging ? "border-primary/50 bg-primary/5 ring-2 ring-primary/20" : ""
          }`}
          onDragOver={(event) => {
            event.preventDefault();
            setDragging(true);
          }}
          onDragLeave={(event) => {
            event.preventDefault();
            if (event.currentTarget === event.target) {
              setDragging(false);
            }
          }}
          onDrop={(event) => {
            void handleDrop(event);
          }}
        >
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={(event) => {
              void handleFileSelection(event);
            }}
          />

          {attachments.length > 0 && (
            <div className="mb-3 flex flex-wrap gap-2">
              {attachments.map((attachment) => (
                <div
                  key={attachment.id}
                  className="flex items-center gap-2 rounded-xl border border-outline-variant/15 bg-surface-container-low px-3 py-2 text-xs text-on-surface"
                >
                  <Icon name={iconNameForPart(attachment.part)} className="text-base" />
                  <div className="min-w-0">
                    <div className="truncate font-medium">{attachmentName(attachment.part)}</div>
                    <div className="truncate text-[10px] text-secondary">{formatBytes(attachment.size)}</div>
                  </div>
                  <button
                    type="button"
                    onClick={() => removeAttachment(attachment.id)}
                    className="rounded-md p-1 text-secondary hover:bg-surface-container-high hover:text-on-surface"
                  >
                    <Icon name="close" className="text-sm" />
                  </button>
                </div>
              ))}
            </div>
          )}

          <div className="flex items-end gap-3">
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              aria-label="Attach photo or file"
              className="inline-flex h-10 shrink-0 items-center gap-2 rounded-full border border-primary/20 bg-surface-container-lowest px-4 text-sm font-semibold text-on-surface shadow-sm transition hover:border-primary/35 hover:bg-surface-container-low"
              title="Attach photo or file"
            >
              <Icon name="attach_file" className="text-lg" />
              <span>Attach</span>
            </button>
            <textarea
              ref={inputRef}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onPaste={(event) => {
                void handlePaste(event);
              }}
              onKeyDown={handleKeyDown}
              placeholder={`Message ${agentInfo.name}...`}
              rows={1}
              className="flex-1 resize-none rounded-xl bg-surface-container-lowest ring-1 ring-outline-variant/10 px-4 py-3 text-sm text-on-surface placeholder:text-outline focus:outline-none focus:ring-2 focus:ring-primary/30"
              style={{ maxHeight: "8rem", overflowY: "auto" }}
            />
            <button
              onClick={() => {
                void sendMessage();
              }}
              disabled={!canSend || sandboxLookupPending}
              className="flex h-10 w-10 items-center justify-center rounded-xl bg-primary text-on-primary transition-opacity hover:shadow-lg active:scale-95 disabled:opacity-40"
            >
              <Icon name={sending ? "hourglass_empty" : "send"} className="text-lg" />
            </button>
          </div>

          <div className="mt-2 flex items-center justify-between gap-3 px-1 text-[11px] text-secondary">
            <span>Attach a photo or file here, or drag and drop it in.</span>
            <span>{maxAttachments} files max, {formatBytes(maxAttachmentBytes)} each</span>
          </div>
          {composerError && (
            <div className="mt-2 rounded-lg bg-error-container/15 px-3 py-2 text-xs text-error">
              {composerError}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
