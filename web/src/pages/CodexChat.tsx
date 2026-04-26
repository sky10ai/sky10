import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { CODEX_EVENT_TYPES } from "../lib/events";
import {
  codex,
  type CodexChatMessage,
  type CodexReasoningEffort,
} from "../lib/rpc";
import { timeAgo, useRPC } from "../lib/useRPC";

interface LocalChatMessage extends CodexChatMessage {
  id: string;
  createdAt: string;
  model?: string;
  responseId?: string;
}

const STORAGE_KEY = "sky10:codex:chat:v1";
const DEFAULT_MODEL = "gpt-5.5";
const DEFAULT_REASONING_EFFORT: CodexReasoningEffort = "medium";

function createMessage(
  role: "assistant" | "user",
  content: string,
  extra: Partial<LocalChatMessage> = {},
): LocalChatMessage {
  return {
    id:
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(16).slice(2)}`,
    role,
    content,
    createdAt: new Date().toISOString(),
    ...extra,
  };
}

function loadStoredMessages(): LocalChatMessage[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as LocalChatMessage[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export default function CodexChat() {
  const {
    data: status,
    error: statusError,
    loading,
    refetch,
  } = useRPC(() => codex.status(), [], {
    live: CODEX_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  const [model, setModel] = useState(DEFAULT_MODEL);
  const [reasoningEffort, setReasoningEffort] =
    useState<CodexReasoningEffort>(DEFAULT_REASONING_EFFORT);
  const [messages, setMessages] = useState<LocalChatMessage[]>(() =>
    loadStoredMessages(),
  );
  const [input, setInput] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [usageLine, setUsageLine] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement | null>(null);

  const canChat = Boolean(status?.linked);

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(messages.slice(-40)));
    } catch {
      // ignore localStorage failures
    }
  }, [messages]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [busy, messages]);

  async function handleSend() {
    const trimmed = input.trim();
    if (!trimmed || busy || !canChat) return;

    const userMessage = createMessage("user", trimmed);
    const nextMessages = [...messages, userMessage];
    setMessages(nextMessages);
    setInput("");
    setError(null);
    setUsageLine(null);
    setBusy(true);

    try {
      const result = await codex.chat({
        model,
        reasoning_effort: reasoningEffort,
        messages: nextMessages.map((message) => ({
          role: message.role,
          content: message.content,
        })),
      });
      setMessages((current) => [
        ...current,
        createMessage("assistant", result.text, {
          model: result.model,
          responseId: result.response_id,
        }),
      ]);
      if (result.usage) {
        const usageParts = [
          result.usage.input_tokens ? `${result.usage.input_tokens} in` : null,
          result.usage.output_tokens
            ? `${result.usage.output_tokens} out`
            : null,
          result.usage.total_tokens
            ? `${result.usage.total_tokens} total`
            : null,
        ].filter(Boolean);
        setUsageLine(usageParts.length > 0 ? usageParts.join(" · ") : null);
      }
    } catch (sendError: unknown) {
      setError(
        sendError instanceof Error ? sendError.message : "Codex chat failed",
      );
    } finally {
      setBusy(false);
      refetch({ background: true });
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void handleSend();
    }
  }

  function clearTranscript() {
    setMessages([]);
    setUsageLine(null);
    setError(null);
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // ignore localStorage failures
    }
  }

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-1 flex-col gap-8 p-12">
      <PageHeader
        title="Codex Chat"
        description="Use your linked ChatGPT Codex session from inside sky10. This is a minimal local chat surface backed by the ChatGPT Codex responses transport."
        actions={
          <>
            <Link
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
              to="/settings/codex"
            >
              <Icon className="text-base" name="tune" />
              Connection
            </Link>
            <button
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface disabled:opacity-50"
              disabled={messages.length === 0}
              onClick={clearTranscript}
              type="button"
            >
              <Icon className="text-base" name="delete" />
              Clear chat
            </button>
          </>
        }
      />

      <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge tone={canChat ? "success" : "neutral"}>
              {canChat ? "Ready" : loading ? "Checking" : "Not ready"}
            </StatusBadge>
            <StatusBadge tone="neutral">{model}</StatusBadge>
            <StatusBadge tone="neutral">thinking {reasoningEffort}</StatusBadge>
            {status?.email && (
              <StatusBadge tone="neutral">{status.email}</StatusBadge>
            )}
          </div>
          <div className="flex items-center gap-3 text-sm text-secondary">
            <label className="flex items-center gap-2">
              <span>Model</span>
              <select
                className="rounded-full border border-outline-variant/20 bg-surface px-3 py-2 text-sm text-on-surface outline-none"
                onChange={(event) => setModel(event.target.value)}
                value={model}
              >
                <option value="gpt-5.5">gpt-5.5</option>
                <option value="gpt-5.5-pro">gpt-5.5-pro</option>
              </select>
            </label>
            <label className="flex items-center gap-2">
              <span>Thinking</span>
              <select
                className="rounded-full border border-outline-variant/20 bg-surface px-3 py-2 text-sm text-on-surface outline-none"
                onChange={(event) =>
                  setReasoningEffort(
                    event.target.value as CodexReasoningEffort,
                  )
                }
                value={reasoningEffort}
              >
                <option value="none">none</option>
                <option value="low">low</option>
                <option value="medium">medium</option>
                <option value="high">high</option>
                <option value="xhigh">xhigh</option>
              </select>
            </label>
          </div>
        </div>

        {(error || statusError || status?.last_error) && (
          <div className="mt-4 rounded-2xl border border-error/20 bg-error-container/20 px-4 py-3 text-sm text-error">
            {error || statusError || status?.last_error}
          </div>
        )}

        {usageLine && (
          <div className="mt-4 rounded-2xl border border-primary/20 bg-primary/10 px-4 py-3 text-sm text-on-surface">
            Last response usage: {usageLine}
          </div>
        )}

        {!canChat && !loading && (
          <div className="mt-6 rounded-3xl border border-outline-variant/10 bg-surface p-8">
            <div className="space-y-3">
              <h2 className="text-xl font-semibold text-on-surface">
                Connect ChatGPT to continue
              </h2>
              <p className="text-sm text-secondary">
                Link a ChatGPT Codex account in sky10 first, then this page can
                send prompts through the Codex responses transport.
              </p>
              <Link
                className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-colors hover:bg-primary/90"
                to="/settings/codex"
              >
                <Icon className="text-base" name="chat" />
                Connect ChatGPT
              </Link>
            </div>
          </div>
        )}

        <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
          <div className="min-h-[28rem] rounded-3xl border border-outline-variant/10 bg-surface px-5 py-5">
            {messages.length === 0 ? (
              <div className="flex h-full min-h-[22rem] flex-col items-center justify-center text-center">
                <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                  <Icon className="text-3xl" name="chat" />
                </div>
                <h2 className="mt-4 text-lg font-semibold text-on-surface">
                  Start a Codex conversation
                </h2>
                <p className="mt-2 max-w-md text-sm text-secondary">
                  Ask a coding question, request a patch outline, or
                  sanity-check an implementation idea. This transcript stays
                  local in your browser.
                </p>
              </div>
            ) : (
              <div className="space-y-4">
                {messages.map((message) => (
                  <article
                    key={message.id}
                    className={`rounded-3xl border px-5 py-4 ${
                      message.role === "assistant"
                        ? "border-outline-variant/10 bg-surface-container-low"
                        : "border-primary/15 bg-primary/5"
                    }`}
                  >
                    <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                      <div className="flex items-center gap-2">
                        <StatusBadge
                          tone={
                            message.role === "assistant"
                              ? "success"
                              : "processing"
                          }
                        >
                          {message.role}
                        </StatusBadge>
                        {message.model && (
                          <StatusBadge tone="neutral">
                            {message.model}
                          </StatusBadge>
                        )}
                      </div>
                      <p className="text-xs text-secondary">
                        {timeAgo(message.createdAt)}
                      </p>
                    </div>

                    {message.role === "assistant" ? (
                      <div className="prose prose-sm max-w-none text-on-surface prose-headings:text-on-surface prose-p:text-on-surface prose-strong:text-on-surface prose-code:text-on-surface prose-pre:bg-surface-container">
                        <Markdown remarkPlugins={[remarkGfm]}>
                          {message.content}
                        </Markdown>
                      </div>
                    ) : (
                      <p className="whitespace-pre-wrap text-sm text-on-surface">
                        {message.content}
                      </p>
                    )}
                  </article>
                ))}

                {busy && (
                  <div className="rounded-3xl border border-outline-variant/10 bg-surface-container-low px-5 py-4">
                    <div className="flex items-center gap-2">
                      <StatusBadge pulse tone="processing">
                        assistant
                      </StatusBadge>
                      <p className="text-sm text-secondary">
                        Codex is thinking…
                      </p>
                    </div>
                  </div>
                )}
                <div ref={bottomRef} />
              </div>
            )}
          </div>

          <div className="rounded-3xl border border-outline-variant/10 bg-surface p-5">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Compose
            </p>
            <textarea
              className="mt-4 min-h-48 w-full rounded-3xl border border-outline-variant/20 bg-surface-container-low px-4 py-4 text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary/30"
              disabled={!canChat || busy}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={
                canChat ? "Ask Codex anything..." : "Connect ChatGPT first"
              }
              value={input}
            />
            <p className="mt-3 text-xs text-secondary">
              Press Enter to send. Use Shift+Enter for a newline.
            </p>
            <button
              className="mt-4 inline-flex w-full items-center justify-center gap-2 rounded-full bg-primary px-5 py-3 text-sm font-semibold text-on-primary shadow-lg transition-colors hover:bg-primary/90 disabled:opacity-50"
              disabled={!canChat || busy || input.trim() === ""}
              onClick={() => void handleSend()}
              type="button"
            >
              <Icon className="text-base" name="send" />
              Send to Codex
            </button>

            <div className="mt-6 rounded-2xl border border-outline-variant/10 bg-surface-container-low px-4 py-4 text-sm text-secondary">
              <p className="font-semibold text-on-surface">Session</p>
              <p className="mt-2">
                {status?.email
                  ? `Linked as ${status.email}.`
                  : "No linked email available yet."}
              </p>
              <p className="mt-2">
                This page sends your full visible transcript on each turn. There
                is no separate daemon-side thread state yet.
              </p>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
