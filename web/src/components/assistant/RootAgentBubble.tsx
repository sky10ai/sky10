import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { Link, useLocation } from "react-router";
import { CODEX_EVENT_TYPES, subscribe } from "../../lib/events";
import { executeRootAgentPrompt } from "../../lib/rootAgent";
import {
  buildRootAgentTroubleshootingDoc,
  collectRootAgentPageContext,
  formatRootAgentContextForPrompt,
  type RootAgentPageContext,
  type RootAgentScreenshotContext,
} from "../../lib/rootAgentContext";
import {
  buildDebugScreenshotUpload,
  captureBrowserScreenshot,
  downloadBlob,
  downloadText,
  safeTimestamp,
  type CapturedScreenshot,
} from "../../lib/rootAgentScreenshot";
import { codex, debug, rootAgent } from "../../lib/rpc";
import { formatBytes, useRPC } from "../../lib/useRPC";
import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";
import {
  createWorkspaceRun,
  runTone,
  toolTone,
  type WorkspaceRun,
} from "./workspaceTypes";

function authLabelForStatus(authMode?: string, authLabel?: string) {
  if (authLabel) return authLabel;
  if (authMode === "chatgpt") return "ChatGPT";
  if (authMode === "apikey") return "API key";
  return "AI";
}

type PendingScreenshotRequest = {
  requestID: string;
  requestedAt?: string;
  message?: string;
  source?: string;
};

type CodexAssistStatus = "blocked" | "offered" | "ready" | "running";

interface CodexAssist {
  contextItems: string[];
  id: string;
  prompt: string;
  status: CodexAssistStatus;
  summary: string;
}

function makeCodexAssistID() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `codex-assist-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function codexAssistTone(status: CodexAssistStatus) {
  if (status === "ready") return "success";
  if (status === "blocked") return "warning";
  if (status === "offered") return "neutral";
  return "processing";
}

function sourceHelpLikely(value: string) {
  return /\b(bug|broken|build|crash|debug|diagnos|error|fail|fix|issue|repair|test)\b/i.test(
    value,
  );
}

async function codexAssistPause(ms: number) {
  await new Promise((resolve) => window.setTimeout(resolve, ms));
}

function parseScreenshotRequest(data: unknown): PendingScreenshotRequest {
  const payload =
    data && typeof data === "object" ? (data as Record<string, unknown>) : {};
  const requestedAt =
    typeof payload.requested_at === "string" ? payload.requested_at : undefined;
  const requestID =
    typeof payload.request_id === "string" && payload.request_id
      ? payload.request_id
      : `debug-screenshot-${Date.now()}`;
  return {
    message: typeof payload.message === "string" ? payload.message : undefined,
    requestID,
    requestedAt,
    source: typeof payload.source === "string" ? payload.source : undefined,
  };
}

export function RootAgentBubble() {
  const location = useLocation();
  const {
    data: codexStatus,
    error: codexStatusError,
    loading: codexStatusLoading,
  } = useRPC(() => codex.status(), [], {
    live: CODEX_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const [open, setOpen] = useState(false);
  const [prompt, setPrompt] = useState("");
  const [status, setStatus] = useState("");
  const [pageContext, setPageContext] = useState<RootAgentPageContext | null>(
    null,
  );
  const [run, setRun] = useState<WorkspaceRun | null>(null);
  const [screenshot, setScreenshot] = useState<CapturedScreenshot | null>(null);
  const [captureBusy, setCaptureBusy] = useState(false);
  const [pendingScreenshotRequest, setPendingScreenshotRequest] =
    useState<PendingScreenshotRequest | null>(null);
  const [codexAssist, setCodexAssist] = useState<CodexAssist | null>(null);
  const codexAssistToken = useRef(0);

  function refreshContext() {
    const next = collectRootAgentPageContext();
    setPageContext(next);
    return next;
  }

  useEffect(() => {
    if (!open) return;
    const frame = window.requestAnimationFrame(() => {
      refreshContext();
    });
    return () => window.cancelAnimationFrame(frame);
  }, [location.hash, location.pathname, location.search, open]);

  useEffect(() => {
    return () => {
      if (screenshot?.url) URL.revokeObjectURL(screenshot.url);
    };
  }, [screenshot]);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event !== "debug.screenshot.request") return;
      const request = parseScreenshotRequest(data);
      setPendingScreenshotRequest(request);
      setOpen(true);
      setStatus(request.message || "Debug screenshot requested.");
      window.requestAnimationFrame(() => {
        setPageContext(collectRootAgentPageContext());
      });
    });
  }, []);

  useEffect(() => {
    return () => {
      codexAssistToken.current += 1;
    };
  }, []);

  const aiLinked = Boolean(codexStatus?.linked);
  const pendingCodexLogin = Boolean(codexStatus?.pending_login);
  const authLabel = authLabelForStatus(
    codexStatus?.auth_mode,
    codexStatus?.auth_label,
  );

  async function saveRun(nextRun: WorkspaceRun) {
    try {
      await rootAgent.runSave({ run: nextRun });
    } catch {
      setStatus("History save failed.");
    }
  }

  function buildCodexAssist(
    nextPrompt: string,
    context: RootAgentPageContext,
    status: CodexAssistStatus,
    summary: string,
  ): CodexAssist {
    return {
      contextItems: [
        context.pageLabel,
        context.route || "current route",
        "managed Codex default",
        screenshot ? "screenshot attached" : "screen context only",
        aiLinked ? `Codex linked via ${authLabel}` : "ChatGPT link needed",
      ],
      id: makeCodexAssistID(),
      prompt: nextPrompt,
      status,
      summary,
    };
  }

  function offerCodexAssist(nextPrompt: string, context: RootAgentPageContext) {
    setCodexAssist(
      buildCodexAssist(
        nextPrompt,
        context,
        "offered",
        "This looks like source-level work. RootAgent can hand the current context to Codex when you want deeper diagnosis.",
      ),
    );
  }

  async function startCodexAssistPreview(nextPrompt?: string) {
    const token = codexAssistToken.current + 1;
    codexAssistToken.current = token;
    const context = refreshContext();
    const assistPrompt =
      nextPrompt?.trim() ||
      prompt.trim() ||
      run?.prompt ||
      `Investigate the current ${context.pageLabel.toLowerCase()} view.`;

    setOpen(true);
    setStatus("Preparing Codex context...");
    setCodexAssist(
      buildCodexAssist(
        assistPrompt,
        context,
        "running",
        "Gathering page state, screenshot metadata, and managed checkout details for Codex.",
      ),
    );

    await codexAssistPause(700);
    if (codexAssistToken.current !== token) return;

    if (!aiLinked) {
      setCodexAssist(
        buildCodexAssist(
          assistPrompt,
          context,
          "blocked",
          "Connect ChatGPT before Codex can inspect source from this device.",
        ),
      );
      setStatus("Codex link required.");
      return;
    }

    setCodexAssist(
      buildCodexAssist(
        assistPrompt,
        context,
        "ready",
        "Codex is ready to inspect the managed sky10 checkout with this conversation and screen context.",
      ),
    );
    setStatus("Codex context ready.");
  }

  async function submitPrompt(nextPrompt: string) {
    const trimmed = nextPrompt.trim();
    if (!trimmed) return;

    const context = refreshContext();
    let currentRun = createWorkspaceRun(trimmed);
    setPrompt("");
    setStatus("Reading context...");
    setRun(currentRun);
    void saveRun(currentRun);

    const screenshotContext: RootAgentScreenshotContext | null = screenshot
      ? {
          capturedAt: screenshot.capturedAt,
          filename: screenshot.filename,
          height: screenshot.height,
          sizeBytes: screenshot.sizeBytes,
          width: screenshot.width,
        }
      : null;

    const patchRun = (updater: (current: WorkspaceRun) => WorkspaceRun) => {
      currentRun = updater(currentRun);
      setRun(currentRun);
      void saveRun(currentRun);
    };

    try {
      const result = await executeRootAgentPrompt(
        trimmed,
        {
          onStatus(value) {
            setStatus(value);
          },
          onText(value) {
            patchRun((current) => ({
              ...current,
              answer: value,
              updatedAt: new Date().toISOString(),
            }));
          },
          onTool(trace) {
            patchRun((current) => {
              const existing = current.toolTraces.find((item) => item.id === trace.id);
              const toolTraces = existing
                ? current.toolTraces.map((item) =>
                    item.id === trace.id ? trace : item,
                  )
                : [...current.toolTraces, trace];
              return {
                ...current,
                toolTraces,
                updatedAt: new Date().toISOString(),
              };
            });
          },
        },
        {
          audience: "for_me",
          pageContext: context,
          screenshot: screenshotContext,
        },
      );

      patchRun((current) => ({
        ...current,
        answer: result.answer,
        followUps: result.followUps,
        status: result.status,
        updatedAt: new Date().toISOString(),
      }));
      if (sourceHelpLikely(trimmed)) {
        offerCodexAssist(trimmed, context);
      }
      setStatus("Done.");
    } catch (error) {
      const message = error instanceof Error ? error.message : "RootAgent failed.";
      patchRun((current) => ({
        ...current,
        answer: current.answer ? `${current.answer}\n\n${message}` : message,
        status: "error",
        updatedAt: new Date().toISOString(),
      }));
      setStatus("Failed.");
    }
  }

  async function captureScreenshot() {
    setCaptureBusy(true);
    setStatus("Opening screen capture...");
    try {
      const context = refreshContext();
      const next = await captureBrowserScreenshot();
      setScreenshot((current) => {
        if (current?.url) URL.revokeObjectURL(current.url);
        return next;
      });
      setStatus("Saving debug screenshot...");
      try {
        const upload = await buildDebugScreenshotUpload(next, context);
        const saved = await debug.screenshot(upload);
        setPendingScreenshotRequest(null);
        if (saved.s3_error) {
          setStatus("Screenshot captured and saved locally. S3 sync failed.");
        } else if (saved.s3_synced) {
          setStatus("Screenshot captured, saved locally, and synced to S3.");
        } else {
          setStatus("Screenshot captured and saved locally.");
        }
      } catch (error) {
        const message = error instanceof Error ? error.message : "Debug upload failed.";
        setStatus(`Screenshot captured. ${message}`);
      }
    } catch (error) {
      setStatus(error instanceof Error ? error.message : "Screenshot failed.");
    } finally {
      setCaptureBusy(false);
    }
  }

  function removeScreenshot() {
    setScreenshot((current) => {
      if (current?.url) URL.revokeObjectURL(current.url);
      return null;
    });
    setStatus("Attachment removed.");
  }

  async function copyContext() {
    const context = refreshContext();
    const text = formatRootAgentContextForPrompt(context, screenshot);
    try {
      await navigator.clipboard.writeText(text);
      setStatus("Context copied.");
    } catch {
      setStatus("Clipboard is not available.");
    }
  }

  function downloadContextDoc() {
    const context = refreshContext();
    const doc = buildRootAgentTroubleshootingDoc(
      context,
      run?.answer ?? "",
      run?.toolTraces ?? [],
      screenshot,
    );
    downloadText(doc, `sky10-context-${safeTimestamp()}.md`, "text/markdown");
    setStatus("Context doc created.");
  }

  function handlePromptKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== "Enter" || event.shiftKey) return;
    event.preventDefault();
    void submitPrompt(prompt);
  }

  const contextLabel = pageContext?.pageLabel ?? "Current page";

  return (
    <>
      {open && (
        <aside className="fixed bottom-[5.75rem] left-4 right-4 z-[90] flex max-h-[calc(100vh-7rem)] flex-col overflow-hidden rounded-2xl border border-outline-variant/20 bg-surface-container-lowest shadow-2xl sm:left-auto sm:right-6 sm:w-[30rem]">
          <header className="flex items-start justify-between gap-3 border-b border-outline-variant/10 px-4 py-3">
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-primary text-on-primary">
                  <Icon name="support_agent" className="text-lg" />
                </span>
                <div className="min-w-0">
                  <p className="text-sm font-semibold text-on-surface">RootAgent</p>
                  <p className="truncate text-xs text-secondary">
                    {contextLabel}
                    {pageContext?.route ? ` · ${pageContext.route}` : ""}
                  </p>
                </div>
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-1">
              <button
                aria-label="Refresh context"
                className="grid h-8 w-8 place-items-center rounded-full text-secondary transition-colors hover:bg-surface-container hover:text-on-surface"
                onClick={refreshContext}
                type="button"
              >
                <Icon name="refresh" className="text-lg" />
              </button>
              <button
                aria-label="Close RootAgent"
                className="grid h-8 w-8 place-items-center rounded-full text-secondary transition-colors hover:bg-surface-container hover:text-on-surface"
                onClick={() => setOpen(false)}
                type="button"
              >
                <Icon name="close" className="text-lg" />
              </button>
            </div>
          </header>

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4">
            <div className="flex flex-wrap items-center gap-2">
              <StatusBadge tone="neutral">{contextLabel}</StatusBadge>
              {codexStatusLoading ? (
                <StatusBadge pulse tone="processing">AI checking</StatusBadge>
              ) : codexStatusError ? (
                <StatusBadge tone="danger">AI status unknown</StatusBadge>
              ) : aiLinked ? (
                <StatusBadge tone="success">AI linked via {authLabel}</StatusBadge>
              ) : pendingCodexLogin ? (
                <StatusBadge pulse tone="processing">AI link pending</StatusBadge>
              ) : (
                <StatusBadge tone="danger">AI not linked</StatusBadge>
              )}
              {status && <span className="truncate text-xs text-secondary">{status}</span>}
            </div>

            {pendingScreenshotRequest && (
              <div className="rounded-xl border border-primary/20 bg-primary-container/20 px-3 py-3">
                <div className="flex items-start gap-3">
                  <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-full bg-primary text-on-primary">
                    <Icon name="photo_camera" className="text-base" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-semibold text-on-surface">
                      Debug screenshot requested
                    </p>
                    <p className="mt-1 text-xs leading-5 text-secondary">
                      {pendingScreenshotRequest.message ||
                        "Capture the current browser view for debugging."}
                    </p>
                    <div className="mt-3 flex flex-wrap items-center gap-2">
                      <button
                        className="inline-flex items-center gap-1.5 rounded-full bg-primary px-3 py-1.5 text-xs font-semibold text-on-primary transition-colors hover:bg-primary/90 disabled:opacity-50"
                        disabled={captureBusy}
                        onClick={() => void captureScreenshot()}
                        type="button"
                      >
                        <Icon name="photo_camera" className="text-sm" />
                        Capture
                      </button>
                      <button
                        className="inline-flex items-center gap-1 rounded-full px-2 py-1.5 text-xs font-semibold text-secondary transition-colors hover:bg-surface-container hover:text-on-surface"
                        onClick={() => setPendingScreenshotRequest(null)}
                        type="button"
                      >
                        <Icon name="close" className="text-sm" />
                        Dismiss
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {!codexStatusLoading && (!aiLinked || codexStatusError) && (
              <div className="rounded-xl border border-outline-variant/15 bg-surface px-3 py-3 text-sm leading-6 text-secondary">
                <div className="flex items-start gap-2">
                  <Icon
                    name={codexStatusError ? "error" : "link_off"}
                    className={codexStatusError ? "mt-0.5 text-base text-error" : "mt-0.5 text-base text-outline"}
                  />
                  <div className="min-w-0">
                    <p className="font-semibold text-on-surface">
                      {codexStatusError
                        ? "Could not verify the AI connection."
                        : pendingCodexLogin
                          ? "ChatGPT linking is still in progress."
                          : "ChatGPT is not linked on this device."}
                    </p>
                    <p className="mt-1">
                      RootAgent can still inspect this page and daemon state, but
                      model-backed planning is unavailable until Codex access is linked.
                    </p>
                    <Link
                      className="mt-2 inline-flex items-center gap-1.5 text-xs font-semibold text-primary hover:underline"
                      to="/settings/codex"
                    >
                      <Icon name="link" className="text-sm" />
                      Connect ChatGPT
                    </Link>
                  </div>
                </div>
              </div>
            )}

            {codexAssist && (
              <section className="rounded-xl border border-outline-variant/15 bg-surface px-3 py-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex min-w-0 items-start gap-3">
                    <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-primary/10 text-primary">
                      <Icon
                        name={
                          codexAssist.status === "running"
                            ? "progress_activity"
                            : "terminal"
                        }
                        className={`text-xl ${
                          codexAssist.status === "running" ? "animate-spin" : ""
                        }`}
                      />
                    </span>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <p className="text-sm font-semibold text-on-surface">
                          Codex assist
                        </p>
                        <StatusBadge tone={codexAssistTone(codexAssist.status)}>
                          {codexAssist.status}
                        </StatusBadge>
                      </div>
                      <p className="mt-1 text-xs leading-5 text-secondary">
                        {codexAssist.summary}
                      </p>
                    </div>
                  </div>
                  <button
                    aria-label="Clear Codex assist"
                    className="grid h-8 w-8 shrink-0 place-items-center rounded-full text-secondary transition-colors hover:bg-surface-container hover:text-on-surface"
                    onClick={() => {
                      codexAssistToken.current += 1;
                      setCodexAssist(null);
                      setStatus("");
                    }}
                    type="button"
                  >
                    <Icon name="close" className="text-base" />
                  </button>
                </div>

                <p className="mt-3 line-clamp-2 rounded-xl bg-surface-container-low px-3 py-2 text-xs leading-5 text-on-surface">
                  {codexAssist.prompt}
                </p>

                <div className="mt-3 flex flex-wrap gap-1.5">
                  {codexAssist.contextItems.map((item) => (
                    <span
                      className="rounded-full bg-surface-container-high px-2.5 py-1 text-[11px] font-medium text-secondary"
                      key={item}
                    >
                      {item}
                    </span>
                  ))}
                </div>

                <div className="mt-3 flex flex-wrap items-center gap-2">
                  {codexAssist.status === "offered" && (
                    <button
                      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-3 py-2 text-xs font-semibold text-on-primary transition-colors hover:bg-primary/90"
                      onClick={() => void startCodexAssistPreview(codexAssist.prompt)}
                      type="button"
                    >
                      <Icon name="terminal" className="text-sm" />
                      Use Codex
                    </button>
                  )}
                  {codexAssist.status === "running" && (
                    <button
                      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-3 py-2 text-xs font-semibold text-on-primary opacity-70"
                      disabled
                      type="button"
                    >
                      <Icon name="progress_activity" className="text-sm animate-spin" />
                      Preparing
                    </button>
                  )}
                  {codexAssist.status === "ready" && (
                    <button
                      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-3 py-2 text-xs font-semibold text-on-primary transition-colors hover:bg-primary/90"
                      onClick={() => setStatus("Codex runner wiring is next.")}
                      type="button"
                    >
                      <Icon name="terminal" className="text-sm" />
                      Start Codex
                    </button>
                  )}
                  {codexAssist.status === "blocked" && (
                    <Link
                      className="inline-flex items-center gap-1.5 rounded-full border border-primary/20 px-3 py-2 text-xs font-semibold text-primary transition-colors hover:bg-primary/5"
                      to="/settings/codex"
                    >
                      <Icon name="link" className="text-sm" />
                      Connect ChatGPT
                    </Link>
                  )}
                </div>
              </section>
            )}

            {screenshot && (
              <div className="relative overflow-hidden rounded-xl border border-outline-variant/10 bg-surface">
                <img
                  alt="Captured sky10 screen"
                  className="h-28 w-full object-cover object-top"
                  src={screenshot.url}
                />
                <button
                  aria-label="Remove screenshot attachment"
                  className="absolute right-2 top-2 grid h-7 w-7 place-items-center rounded-full bg-inverse-surface/85 text-inverse-on-surface shadow-sm transition-colors hover:bg-inverse-surface"
                  onClick={removeScreenshot}
                  type="button"
                >
                  <Icon name="close" className="text-base" />
                </button>
                <div className="flex items-center justify-between gap-3 px-3 py-2 text-xs text-secondary">
                  <span className="truncate">{screenshot.filename}</span>
                  <button
                    className="shrink-0 font-semibold text-primary hover:underline"
                    onClick={() => downloadBlob(screenshot.blob, screenshot.filename)}
                    type="button"
                  >
                    {formatBytes(screenshot.sizeBytes)}
                  </button>
                </div>
              </div>
            )}

            {run && (
              <article className="space-y-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-semibold text-on-surface">
                      {run.prompt}
                    </p>
                  </div>
                  <StatusBadge tone={runTone(run.status)}>{run.status}</StatusBadge>
                </div>
                <div className="max-h-60 overflow-y-auto whitespace-pre-wrap rounded-xl bg-surface px-3 py-3 text-sm leading-6 text-on-surface">
                  {run.answer || "..."}
                </div>
                {run.followUps && run.followUps.length > 0 && (
                  <div className="flex flex-wrap gap-2">
                    {run.followUps.map((item) => (
                      <button
                        className="rounded-full border border-outline-variant/20 px-3 py-1.5 text-xs text-secondary transition-colors hover:border-primary/25 hover:text-on-surface"
                        key={item}
                        onClick={() => void submitPrompt(item)}
                        type="button"
                      >
                        {item}
                      </button>
                    ))}
                  </div>
                )}
                {run.toolTraces.length > 0 && (
                  <details className="rounded-xl border border-outline-variant/10 px-3 py-2">
                    <summary className="cursor-pointer text-xs font-semibold text-secondary">
                      Trace
                    </summary>
                    <div className="mt-3 space-y-3">
                      {run.toolTraces.map((trace) => (
                        <div
                          className="border-t border-outline-variant/10 pt-3 first:border-t-0 first:pt-0"
                          key={trace.id}
                        >
                          <div className="flex items-start justify-between gap-3">
                            <p className="text-xs font-semibold text-on-surface">
                              {trace.title}
                            </p>
                            <StatusBadge tone={toolTone(trace.status)}>
                              {trace.status}
                            </StatusBadge>
                          </div>
                          <p className="mt-1 font-mono text-[11px] text-secondary">
                            {trace.rpcMethod}
                          </p>
                          <p className="mt-1 text-xs leading-5 text-secondary">
                            {trace.detail}
                          </p>
                        </div>
                      ))}
                    </div>
                  </details>
                )}
              </article>
            )}

          </div>

          <form
            className="border-t border-outline-variant/10 p-3"
            onSubmit={(event) => {
              event.preventDefault();
              void submitPrompt(prompt);
            }}
          >
            <textarea
              className="min-h-[84px] w-full resize-none rounded-xl border border-outline-variant/15 bg-surface px-3 py-2 text-sm leading-6 text-on-surface outline-none transition-colors placeholder:text-secondary/80 focus:border-primary/35"
              onChange={(event) => setPrompt(event.target.value)}
              onFocus={refreshContext}
              onKeyDown={handlePromptKeyDown}
              placeholder="How can I help?"
              value={prompt}
            />
            <p className="mt-2 text-[11px] text-secondary">
              Press Enter to send. Use Shift+Enter for a newline.
            </p>
            <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap gap-2">
                <button
                  className="inline-flex items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-2 text-xs font-semibold text-on-surface transition-colors hover:bg-surface-container disabled:opacity-50"
                  disabled={captureBusy}
                  onClick={() => void captureScreenshot()}
                  type="button"
                >
                  <Icon name="photo_camera" className="text-sm" />
                  Screen
                </button>
                <button
                  className="inline-flex items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-2 text-xs font-semibold text-on-surface transition-colors hover:bg-surface-container"
                  onClick={() => void copyContext()}
                  type="button"
                >
                  <Icon name="content_copy" className="text-sm" />
                  Copy
                </button>
                <button
                  className="inline-flex items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-2 text-xs font-semibold text-on-surface transition-colors hover:bg-surface-container"
                  onClick={downloadContextDoc}
                  type="button"
                >
                  <Icon name="description" className="text-sm" />
                  Doc
                </button>
                <button
                  className="inline-flex items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-2 text-xs font-semibold text-on-surface transition-colors hover:bg-surface-container disabled:opacity-50"
                  disabled={codexAssist?.status === "running"}
                  onClick={() => void startCodexAssistPreview()}
                  type="button"
                >
                  <Icon name="terminal" className="text-sm" />
                  Codex
                </button>
              </div>
              <button
                className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
                disabled={!prompt.trim()}
                type="submit"
              >
                <Icon name="send" className="text-base" />
                {aiLinked ? "Ask" : "Inspect"}
              </button>
            </div>
          </form>
        </aside>
      )}

      <button
        aria-label="Open RootAgent"
        className="root-agent-bubble-attention fixed bottom-5 right-5 z-[80] inline-flex h-14 w-14 items-center justify-center overflow-hidden rounded-full bg-primary text-on-primary shadow-xl ring-1 ring-primary/20 transition-transform hover:scale-105 focus:outline-none focus:ring-4 focus:ring-primary/25 sm:bottom-6 sm:right-6"
        onClick={() => {
          setOpen((current) => !current);
          window.requestAnimationFrame(() => {
            refreshContext();
          });
        }}
        type="button"
      >
        <Icon name={open ? "close" : "support_agent"} className="relative z-10 text-2xl" filled={!open} />
      </button>
    </>
  );
}
