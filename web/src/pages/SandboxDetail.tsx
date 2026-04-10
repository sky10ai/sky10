import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
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
  sandboxLabel,
  sandboxLogKey,
  sandboxTone,
} from "../lib/sandboxes";
import { timeAgo, useRPC } from "../lib/useRPC";

export default function SandboxDetail() {
  const navigate = useNavigate();
  const params = useParams();
  const name = decodeURIComponent(params.name ?? "");
  const [logs, setLogs] = useState<SandboxLogEntry[]>([]);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [copyMessage, setCopyMessage] = useState<string | null>(null);

  const {
    data: selected,
    error: selectedError,
    refetch: refetchSelected,
  } = useRPC<SandboxRecord | null>(
    () => {
      if (!name) return Promise.resolve(null);
      return sandbox.get({ name });
    },
    [name],
    {
      keepPreviousData: true,
      live: [
        (event, data) =>
          event === "sandbox:state" &&
          typeof data === "object" &&
          data !== null &&
          (data as { name?: string }).name === name,
      ],
      refreshIntervalMs: 10_000,
    },
  );

  const loadLogs = useCallback(async () => {
    if (!name) {
      setLogs([]);
      return;
    }
    const result: SandboxLogsResult = await sandbox.logs({ name, limit: 400 });
    setLogs(result.entries);
  }, [name]);

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event === "sandbox:log") {
        const payload = data as {
          name?: string;
          stream?: string;
          time?: string;
          line?: string;
        };
        if (payload.name !== name || !payload.line) {
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
      }
    });
  }, [name]);

  const runAction = useCallback(async (action: "start" | "stop" | "delete") => {
    if (!name) return;

    setBusyAction(action);
    setActionError(null);
    try {
      if (action === "start") {
        await sandbox.start({ name });
        refetchSelected({ background: true });
        return;
      }
      if (action === "stop") {
        await sandbox.stop({ name });
        refetchSelected({ background: true });
        return;
      }
      await sandbox.delete({ name });
      navigate("/settings/sandboxes");
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : `${action} failed`);
    } finally {
      setBusyAction(null);
    }
  }, [name, navigate, refetchSelected]);

  const handleCopyTerminal = useCallback(async () => {
    const command = selected?.shell || `limactl shell ${name}`;
    try {
      await navigator.clipboard.writeText(command);
      setCopyMessage("Shell command copied.");
      window.setTimeout(() => setCopyMessage(null), 2000);
    } catch {
      setCopyMessage("Copy failed.");
      window.setTimeout(() => setCopyMessage(null), 2000);
    }
  }, [name, selected?.shell]);

  if (!name) {
    return (
      <section className="mx-auto flex w-full max-w-5xl flex-1 flex-col gap-6 p-12">
        <PageHeader
          eyebrow="Settings"
          title="Sandbox"
          description="No sandbox name was provided."
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

  const shellCommand = selected?.shell || `limactl shell ${name}`;

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 p-12">
      <PageHeader
        eyebrow="Settings"
        title={name}
        description="Detailed runtime status for this sandbox, including provisioning logs, lifecycle actions, and terminal access."
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

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_360px]">
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
                <InfoCard label="Provider" value={selected.provider} />
                <InfoCard label="Template" value={selected.template} />
                <InfoCard label="Guest IP" value={selected.ip_address || "Waiting..."} mono />
                <InfoCard label="Created" value={selected.created_at} mono />
                <InfoCard label="Updated" value={selected.updated_at} mono />
                <InfoCard label="Last Log" value={selected.last_log_at || "Waiting..."} mono />
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

        <aside className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
          <div className="space-y-5">
            <div>
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Terminal
              </p>
              <h2 className="mt-2 text-xl font-semibold text-on-surface">
                Host shell access
              </h2>
            </div>
            <p className="text-sm text-secondary">
              Use the host terminal to enter this sandbox directly while the in-browser terminal work lands.
            </p>
            <div className="rounded-2xl bg-[#111315] p-4 font-mono text-xs text-[#d7dadc]">
              {shellCommand}
            </div>
            <button
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
              onClick={() => void handleCopyTerminal()}
              type="button"
            >
              <Icon name="content_copy" />
              Copy shell command
            </button>
            {copyMessage && (
              <p className="text-sm text-secondary">{copyMessage}</p>
            )}
          </div>
        </aside>
      </div>

      <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="mb-4 flex items-center justify-between gap-4">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Provisioning Log
            </p>
            <h2 className="mt-2 text-2xl font-semibold text-on-surface">
              Boot and runtime output
            </h2>
          </div>
          {selected?.last_log_at && (
            <span className="text-xs text-secondary">
              Updated {timeAgo(selected.last_log_at)}
            </span>
          )}
        </div>

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
      </section>
    </section>
  );
}

function InfoCard({
  className = "",
  label,
  mono = false,
  value,
}: {
  className?: string;
  label: string;
  mono?: boolean;
  value: string;
}) {
  return (
    <div className={`rounded-2xl bg-surface-container p-4 ${className}`.trim()}>
      <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
        {label}
      </p>
      <p className={`mt-2 break-all text-sm text-secondary ${mono ? "font-mono" : ""}`}>
        {value}
      </p>
    </div>
  );
}
