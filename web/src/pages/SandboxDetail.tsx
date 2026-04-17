import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { SandboxTerminal } from "../components/SandboxTerminal";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { subscribe } from "../lib/events";
import {
  sandbox,
  type SandboxLogEntry,
  type SandboxLogsResult,
  type SandboxRecord,
} from "../lib/rpc";
import {
  sandboxCurrentProgress,
  sandboxLabel,
  sandboxLogKey,
  sandboxTone,
} from "../lib/sandboxes";
import { timeAgo, useRPC } from "../lib/useRPC";

export default function SandboxDetail() {
  const navigate = useNavigate();
  const params = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const slug = decodeURIComponent(params.slug ?? params.name ?? "");
  const requestedPanel = useMemo<"logs" | "terminal">(() => (
    searchParams.get("panel") === "terminal" ? "terminal" : "logs"
  ), [searchParams]);
  const [logs, setLogs] = useState<SandboxLogEntry[]>([]);
  const [activePanel, setActivePanel] = useState<"logs" | "terminal">(requestedPanel);
  const [hasOpenedTerminal, setHasOpenedTerminal] = useState(requestedPanel === "terminal");
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [copyMessage, setCopyMessage] = useState<string | null>(null);

  const {
    data: selected,
    error: selectedError,
    refetch: refetchSelected,
  } = useRPC<SandboxRecord | null>(
    () => {
      if (!slug) return Promise.resolve(null);
      return sandbox.get({ slug });
    },
    [slug],
    {
      keepPreviousData: true,
      live: [
        (event, data) =>
          event === "sandbox:state" &&
          typeof data === "object" &&
          data !== null &&
          (data as { slug?: string }).slug === slug,
      ],
      refreshIntervalMs: 10_000,
    },
  );

  useEffect(() => {
    if (!selected?.slug || selected.slug === slug) return;
    navigate(`/settings/sandboxes/${encodeURIComponent(selected.slug)}`, { replace: true });
  }, [navigate, selected?.slug, slug]);

  useEffect(() => {
    setActivePanel(requestedPanel);
    if (requestedPanel === "terminal") {
      setHasOpenedTerminal(true);
    }
  }, [requestedPanel]);

  const switchPanel = useCallback((nextPanel: "logs" | "terminal") => {
    setActivePanel(nextPanel);
    if (nextPanel === "terminal") {
      setHasOpenedTerminal(true);
    }
    const nextParams = new URLSearchParams(searchParams);
    if (nextPanel === "terminal") {
      nextParams.set("panel", "terminal");
    } else {
      nextParams.delete("panel");
    }
    setSearchParams(nextParams, { replace: true });
  }, [searchParams, setSearchParams]);

  const loadLogs = useCallback(async () => {
    if (!slug) {
      setLogs([]);
      return;
    }
    const result: SandboxLogsResult = await sandbox.logs({ slug, limit: 400 });
    setLogs(Array.isArray(result.entries) ? result.entries : []);
  }, [slug]);

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event !== "sandbox:log") {
        return;
      }
      const payload = data as {
        slug?: string;
        stream?: string;
        time?: string;
        line?: string;
      };
      if (payload.slug !== slug || !payload.line) {
        return;
      }
      setLogs((prev) => {
        const next = [
          ...prev,
          {
            time: payload.time ?? new Date().toISOString(),
            stream: payload.stream ?? "stdout",
            line: payload.line ?? "",
          },
        ];
        return next.length > 500 ? next.slice(-500) : next;
      });
    });
  }, [slug]);

  const runAction = useCallback(async (action: "start" | "stop" | "delete") => {
    if (!slug) return;

    setBusyAction(action);
    setActionError(null);
    try {
      if (action === "start") {
        await sandbox.start({ slug });
        refetchSelected({ background: true });
        return;
      }
      if (action === "stop") {
        await sandbox.stop({ slug });
        refetchSelected({ background: true });
        return;
      }
      await sandbox.delete({ slug });
      navigate("/settings/sandboxes");
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : `${action} failed`);
    } finally {
      setBusyAction(null);
    }
  }, [navigate, refetchSelected, slug]);

  const handleCopyTerminal = useCallback(async () => {
    const command = selected?.shell || `limactl shell ${selected?.slug ?? slug}`;
    try {
      await navigator.clipboard.writeText(command);
      setCopyMessage("Shell command copied.");
      window.setTimeout(() => setCopyMessage(null), 2000);
    } catch {
      setCopyMessage("Copy failed.");
      window.setTimeout(() => setCopyMessage(null), 2000);
    }
  }, [selected?.shell, selected?.slug, slug]);

  if (!slug) {
    return (
      <section className="mx-auto flex w-full max-w-5xl flex-1 flex-col gap-6 p-12">
        <PageHeader
          eyebrow="Settings"
          title="Sandbox"
          description="No sandbox identifier was provided."
          actions={(
            <Link
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
              to="/settings/sandboxes"
            >
              <Icon className="text-base" name="arrow_back" />
              Back to Sandboxes
            </Link>
          )}
        />
      </section>
    );
  }

  const shellCommand = selected?.shell || `limactl shell ${selected?.slug ?? slug}`;
  const terminalEnabled = selected?.provider === "lima" && (selected.status === "ready" || selected.vm_status === "Running");
  const guestIP = selected?.ip_address?.trim() ?? "";
  const openClawURL = selected?.template === "openclaw" && guestIP ? `http://${guestIP}:18790/chat?session=main` : "";
  const guestSky10URL = selected?.template === "openclaw" && guestIP ? `http://${guestIP}:9101` : "";
  const progress = selected ? sandboxCurrentProgress(selected) : null;
  const progressWidth = Math.max(0, Math.min(progress?.percent ?? 0, 100));

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 p-12">
      <PageHeader
        eyebrow="Settings"
        title={selected?.name ?? slug}
        description="Detailed runtime status for this sandbox, including lifecycle actions, boot logs, and terminal access."
        actions={(
          <Link
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
            to="/settings/sandboxes"
          >
            <Icon className="text-base" name="arrow_back" />
            Back to Sandboxes
          </Link>
        )}
      />

      {(actionError || selectedError) && (
        <div className="rounded-2xl bg-error-container/20 p-4 text-sm text-error">
          {actionError ?? selectedError}
        </div>
      )}

      <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        {selected ? (
          <div className="space-y-6">
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="space-y-3">
                <div className="flex flex-wrap items-center gap-3">
                  <StatusBadge
                    pulse={selected.status === "creating" || selected.status === "starting"}
                    tone={sandboxTone(selected.status)}
                  >
                    {sandboxLabel(selected.status)}
                  </StatusBadge>
                  {selected.vm_status && (
                    <StatusBadge tone="neutral">
                      VM {selected.vm_status}
                    </StatusBadge>
                  )}
                </div>
                <div className="space-y-1">
                  <p className="text-sm text-secondary">
                    {selected.provider} / {selected.template}
                  </p>
                  <p className="text-xs text-secondary">
                    Updated {timeAgo(selected.updated_at)}
                    {selected.last_log_at ? ` • last log ${timeAgo(selected.last_log_at)}` : ""}
                  </p>
                </div>
                {progress && (
                  <div className="w-full max-w-xl space-y-2 pt-1">
                    <div className="flex items-center justify-between gap-3 text-sm">
                      <span className="font-medium text-on-surface">{progress.summary}</span>
                      <span className="font-semibold text-secondary">{progress.percent}%</span>
                    </div>
                    <div className="h-2 overflow-hidden rounded-full bg-surface-container">
                      <div
                        className={`h-full rounded-full transition-[width] duration-300 ${
                          selected.status === "error" ? "bg-error" : "bg-primary"
                        }`}
                        style={{ width: `${progressWidth}%` }}
                      />
                    </div>
                  </div>
                )}
              </div>

              <div className="flex flex-wrap items-center gap-3">
                <button
                  className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                  disabled={busyAction !== null}
                  onClick={() => void runAction("start")}
                  type="button"
                >
                  <Icon name="play_arrow" />
                  {busyAction === "start" ? "Starting..." : "Start"}
                </button>
                <button
                  className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                  disabled={busyAction !== null}
                  onClick={() => void runAction("stop")}
                  type="button"
                >
                  <Icon name="stop" />
                  {busyAction === "stop" ? "Stopping..." : "Stop"}
                </button>
                <button
                  className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-error transition-colors disabled:opacity-50"
                  disabled={busyAction !== null}
                  onClick={() => void runAction("delete")}
                  type="button"
                >
                  <Icon name="delete" />
                  {busyAction === "delete" ? "Deleting..." : "Delete"}
                </button>
              </div>
            </div>

            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              <InfoCard label="Runtime ID" mono value={selected.slug} />
              <InfoCard label="Provider" value={selected.provider} />
              <InfoCard label="Template" value={selected.template} />
              <InfoCard label="Guest IP" mono value={selected.ip_address || "Waiting..."} />
              <InfoCard label="Created" mono value={selected.created_at} />
              <InfoCard label="Updated" mono value={selected.updated_at} />
              <InfoCard label="Last Log" mono value={selected.last_log_at || "Waiting..."} />
              {openClawURL && (
                <InfoCard label="OpenClaw UI" mono value={openClawURL} href={openClawURL} />
              )}
              {guestSky10URL && (
                <InfoCard label="Guest sky10 UI" mono value={guestSky10URL} href={guestSky10URL} />
              )}
              <InfoCard
                className="md:col-span-2 xl:col-span-3"
                label="Shared Directory"
                mono
                value={selected.shared_dir || "—"}
              />
              {selected.last_error && (
                <div className="rounded-2xl bg-error-container/20 p-4 text-sm text-error md:col-span-2 xl:col-span-3">
                  {selected.last_error}
                </div>
              )}
            </div>
          </div>
        ) : selectedError ? (
          <div className="space-y-4">
            <p className="text-lg font-semibold text-on-surface">
              Sandbox not available
            </p>
            <p className="text-sm text-secondary">
              The runtime could not be loaded. It may have been deleted or the daemon state is stale.
            </p>
          </div>
        ) : (
          <div className="text-sm text-secondary">
            Loading sandbox details...
          </div>
        )}
      </section>

      <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Sandbox Console
            </p>
            <h2 className="text-2xl font-semibold text-on-surface">
              {activePanel === "logs" ? "Boot and runtime output" : "Interactive shell"}
            </h2>
            <p className="text-sm text-secondary">
              {activePanel === "logs"
                ? "Watch provisioning and runtime events as they stream in."
                : "Switch between live logs and the embedded shell without leaving this page."}
            </p>
          </div>

          <div className="flex flex-wrap items-center justify-end gap-3">
            {activePanel === "logs" && selected?.last_log_at && (
              <span className="text-xs text-secondary">
                Updated {timeAgo(selected.last_log_at)}
              </span>
            )}
            <div
              aria-label="Sandbox detail panel"
              className="inline-flex rounded-full bg-surface-container p-1"
              role="tablist"
            >
              <button
                aria-controls="sandbox-logs-panel"
                aria-selected={activePanel === "logs"}
                className={`rounded-full px-4 py-2 text-sm font-semibold transition-colors ${
                  activePanel === "logs"
                    ? "bg-surface-container-lowest text-on-surface shadow-sm"
                    : "text-secondary hover:text-on-surface"
                }`}
                id="sandbox-logs-tab"
                onClick={() => switchPanel("logs")}
                role="tab"
                type="button"
              >
                Logs
              </button>
              <button
                aria-controls="sandbox-terminal-panel"
                aria-selected={activePanel === "terminal"}
                className={`rounded-full px-4 py-2 text-sm font-semibold transition-colors ${
                  activePanel === "terminal"
                    ? "bg-surface-container-lowest text-on-surface shadow-sm"
                    : "text-secondary hover:text-on-surface"
                }`}
                id="sandbox-terminal-tab"
                onClick={() => switchPanel("terminal")}
                role="tab"
                type="button"
              >
                Terminal
              </button>
            </div>
          </div>
        </div>

        <div
          aria-labelledby="sandbox-logs-tab"
          className={activePanel === "logs" ? "" : "hidden"}
          id="sandbox-logs-panel"
          role="tabpanel"
        >
          <div className="h-[560px] overflow-y-auto rounded-2xl bg-[#111315] p-4 font-mono text-xs text-[#d7dadc]">
            {logs.length ? (
              <div className="space-y-1">
                {logs.map((entry, index) => (
                  <div key={sandboxLogKey(entry, index)} className="whitespace-pre-wrap break-words">
                    <span className="text-[#7f8c98]">{entry.time}</span>
                    {" "}
                    <span className={entry.stream === "stderr" ? "text-[#ffbf69]" : "text-[#8bd3dd]"}>
                      [{entry.stream}]
                    </span>
                    {" "}
                    <span>{entry.line}</span>
                  </div>
                ))}
              </div>
            ) : (
              <div className="text-[#7f8c98]">
                Waiting for sandbox log output...
              </div>
            )}
          </div>
        </div>

        <div
          aria-labelledby="sandbox-terminal-tab"
          className={`space-y-5 ${activePanel === "terminal" ? "" : "hidden"}`}
          id="sandbox-terminal-panel"
          role="tabpanel"
        >
          {selected ? (
            hasOpenedTerminal ? (
              <SandboxTerminal
                enabled={terminalEnabled}
                slug={selected.slug}
              />
            ) : (
              <div className="rounded-2xl bg-surface-container p-4 text-sm text-secondary">
                Open the terminal tab to connect to the sandbox shell.
              </div>
            )
          ) : (
            <div className="rounded-2xl bg-surface-container p-4 text-sm text-secondary">
              Terminal availability will appear once the sandbox record loads.
            </div>
          )}

          <div className="rounded-2xl bg-[#111315] p-4 font-mono text-xs text-[#d7dadc]">
            {shellCommand}
          </div>

          <div>
            <button
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
              onClick={() => void handleCopyTerminal()}
              type="button"
            >
              <Icon name="content_copy" />
              Copy shell command
            </button>
          </div>

          <p className="text-sm text-secondary">
            {selected?.template === "hermes"
              ? "Open a shell inside the sandbox or copy the host command that launches the Hermes TUI directly."
              : "Open a shell inside the sandbox or copy the host command."}
          </p>

          {copyMessage && (
            <p className="text-sm text-secondary">{copyMessage}</p>
          )}
        </div>
      </section>
    </section>
  );
}

function InfoCard({
  className = "",
  href,
  label,
  mono = false,
  value,
}: {
  className?: string;
  href?: string;
  label: string;
  mono?: boolean;
  value: string;
}) {
  return (
    <div className={`rounded-2xl bg-surface-container p-4 ${className}`.trim()}>
      <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
        {label}
      </p>
      {href ? (
        <a
          className={`mt-2 block break-all text-sm text-secondary underline decoration-outline-variant/30 underline-offset-4 transition-colors hover:text-on-surface ${mono ? "font-mono" : ""}`}
          href={href}
          rel="noreferrer"
          target="_blank"
        >
          {value}
        </a>
      ) : (
        <p className={`mt-2 break-all text-sm text-secondary ${mono ? "font-mono" : ""}`}>
          {value}
        </p>
      )}
    </div>
  );
}
