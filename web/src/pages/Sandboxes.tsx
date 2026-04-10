import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { SANDBOX_EVENT_TYPES, subscribe } from "../lib/events";
import {
  sandbox,
  type SandboxLogEntry,
  type SandboxLogsResult,
  type SandboxRecord,
} from "../lib/rpc";
import { timeAgo, useRPC } from "../lib/useRPC";

function nextSandboxName() {
  return `linux-${Math.random().toString(36).slice(2, 6)}`;
}

function toneForStatus(status: string): "processing" | "success" | "neutral" | "danger" {
  switch (status) {
    case "creating":
    case "starting":
      return "processing";
    case "ready":
      return "success";
    case "error":
      return "danger";
    default:
      return "neutral";
  }
}

function labelForStatus(status: string) {
  switch (status) {
    case "creating":
      return "Creating";
    case "starting":
      return "Starting";
    case "ready":
      return "Ready";
    case "stopped":
      return "Stopped";
    case "error":
      return "Error";
    default:
      return status || "Unknown";
  }
}

function logKey(entry: SandboxLogEntry, index: number) {
  return `${entry.time}:${entry.stream}:${index}`;
}

export default function Sandboxes() {
  const [draftName, setDraftName] = useState(nextSandboxName);
  const [selectedName, setSelectedName] = useState("");
  const [logs, setLogs] = useState<SandboxLogEntry[]>([]);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);

  const {
    data: listData,
    error: listError,
    refetch: refetchList,
  } = useRPC(() => sandbox.list(), [], {
    live: SANDBOX_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const sandboxes = listData?.sandboxes ?? [];

  const selectedExists = useMemo(
    () => sandboxes.some((item) => item.name === selectedName),
    [sandboxes, selectedName],
  );

  useEffect(() => {
    const firstSandbox = sandboxes[0];
    if (!selectedName && firstSandbox) {
      setSelectedName(firstSandbox.name);
      return;
    }
    if (selectedName && !selectedExists) {
      setSelectedName(firstSandbox?.name ?? "");
    }
  }, [sandboxes, selectedExists, selectedName]);

  const {
    data: selected,
    error: selectedError,
    refetch: refetchSelected,
  } = useRPC<SandboxRecord | null>(
    () => {
      if (!selectedName) return Promise.resolve(null);
      return sandbox.get({ name: selectedName });
    },
    [selectedName],
    {
      keepPreviousData: true,
      live: [
        (event, data) =>
          event === "sandbox:state" &&
          typeof data === "object" &&
          data !== null &&
          (data as { name?: string }).name === selectedName,
      ],
      refreshIntervalMs: 5_000,
    },
  );

  const loadLogs = useCallback(async (name: string) => {
    if (!name) {
      setLogs([]);
      return;
    }
    const result: SandboxLogsResult = await sandbox.logs({ name, limit: 200 });
    setLogs(result.entries);
  }, []);

  useEffect(() => {
    void loadLogs(selectedName);
  }, [loadLogs, selectedName]);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event === "sandbox:state") {
        const name = typeof data === "object" && data !== null
          ? (data as { name?: string }).name
          : "";
        if (name && name === selectedName) {
          refetchSelected({ background: true });
        }
        refetchList({ background: true });
        return;
      }
      if (event === "sandbox:log") {
        const payload = data as {
          name?: string;
          stream?: string;
          time?: string;
          line?: string;
        };
        if (payload.name !== selectedName || !payload.line) {
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
          return next.length > 400 ? next.slice(-400) : next;
        });
      }
    });
  }, [refetchList, refetchSelected, selectedName]);

  const handleCreate = useCallback(async () => {
    const name = draftName.trim();
    if (!name) return;
    setBusyAction("create");
    setActionError(null);
    try {
      await sandbox.create({
        name,
        provider: "lima",
        template: "ubuntu",
      });
      setSelectedName(name);
      setLogs([]);
      setDraftName(nextSandboxName());
      refetchList();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Create failed");
    } finally {
      setBusyAction(null);
    }
  }, [draftName, refetchList]);

  const handleStart = useCallback(async () => {
    if (!selectedName) return;
    setBusyAction("start");
    setActionError(null);
    try {
      await sandbox.start({ name: selectedName });
      refetchSelected();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Start failed");
    } finally {
      setBusyAction(null);
    }
  }, [refetchSelected, selectedName]);

  const handleStop = useCallback(async () => {
    if (!selectedName) return;
    setBusyAction("stop");
    setActionError(null);
    try {
      await sandbox.stop({ name: selectedName });
      refetchSelected();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Stop failed");
    } finally {
      setBusyAction(null);
    }
  }, [refetchSelected, selectedName]);

  const handleDelete = useCallback(async () => {
    if (!selectedName) return;
    setBusyAction("delete");
    setActionError(null);
    try {
      await sandbox.delete({ name: selectedName });
      setLogs([]);
      setSelectedName("");
      refetchList();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Delete failed");
    } finally {
      setBusyAction(null);
    }
  }, [refetchList, selectedName]);

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 p-12">
      <PageHeader
        eyebrow="Settings"
        title="Local Agents"
        description="Manage isolated Linux runtimes for local agents on this Mac. Today this flow provisions Ubuntu with Lima and streams boot output while the guest comes up."
        actions={(
          <Link
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
            to="/settings"
          >
            <Icon className="text-base" name="arrow_back" />
            Back to Settings
          </Link>
        )}
      />

      {(actionError || listError || selectedError) && (
        <div className="rounded-2xl bg-error-container/20 p-4 text-sm text-error">
          {actionError ?? listError ?? selectedError}
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_340px]">
        <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
          <div className="space-y-6">
            <div className="space-y-3">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge tone="processing">Lima</StatusBadge>
                <StatusBadge tone="neutral">Ubuntu 24.04</StatusBadge>
                <StatusBadge tone="neutral">macOS</StatusBadge>
              </div>
              <div className="space-y-2">
                <h2 className="text-2xl font-semibold text-on-surface">
                  Provision a local agent runtime
                </h2>
                <p className="max-w-2xl text-sm text-secondary">
                  This creates an isolated Ubuntu VM under your local sky10 workspace. It does not install sky10 or OpenClaw inside the guest yet; this pass is just about getting Linux up reliably and making the boot process visible.
                </p>
              </div>
            </div>

            <div className="flex flex-col gap-3 md:flex-row">
              <input
                className="min-w-0 flex-1 rounded-full border border-outline-variant/20 bg-surface-container px-5 py-3 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                onChange={(e) => setDraftName(e.target.value)}
                placeholder="local agent name"
                value={draftName}
              />
              <button
                className="inline-flex items-center justify-center gap-2 rounded-full bg-primary px-6 py-3 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                disabled={busyAction !== null}
                onClick={handleCreate}
                type="button"
              >
                <Icon name="add" />
                {busyAction === "create" ? "Provisioning..." : "Provision Ubuntu Runtime"}
              </button>
            </div>
          </div>
        </section>

        <aside className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
          <div className="space-y-4">
            <div>
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Current Scope
              </p>
              <h2 className="mt-2 text-xl font-semibold text-on-surface">
                Linux first
              </h2>
            </div>
            <div className="space-y-3 text-sm text-secondary">
              <p>
                Creates a Lima-managed Ubuntu VM and a shared host directory at the sandbox path.
              </p>
              <p>
                Streams provisioning logs so boot failures are visible instead of disappearing into the daemon.
              </p>
              <p>
                Leaves room for the next step: install sky10 in-guest, attach it over Skylink, and turn the runtime into a real local agent.
              </p>
            </div>
          </div>
        </aside>
      </div>

      <div className="grid flex-1 grid-cols-1 gap-6 lg:grid-cols-[320px_minmax(0,1fr)]">
        <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-4 shadow-sm">
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-sm font-bold uppercase tracking-[0.2em] text-outline">
              Local Runtimes
            </h2>
            <span className="text-xs text-secondary">
              {sandboxes.length}
            </span>
          </div>
          <div className="space-y-2">
            {sandboxes.length ? (
              sandboxes.map((item) => (
                <button
                  key={item.name}
                  className={`w-full rounded-xl border px-4 py-3 text-left transition-colors ${
                    selectedName === item.name
                      ? "border-primary/40 bg-primary/10"
                      : "border-outline-variant/10 bg-surface-container hover:bg-surface-container-high"
                  }`}
                  onClick={() => setSelectedName(item.name)}
                  type="button"
                >
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <p className="font-semibold text-on-surface">{item.name}</p>
                      <p className="text-xs text-secondary">
                        {item.provider} / {item.template}
                      </p>
                    </div>
                    <StatusBadge tone={toneForStatus(item.status)}>
                      {labelForStatus(item.status)}
                    </StatusBadge>
                  </div>
                </button>
              ))
            ) : (
              <div className="rounded-xl bg-surface-container p-4 text-sm text-secondary">
                No local runtimes yet.
              </div>
            )}
          </div>
        </div>

        <div className="flex min-h-0 flex-col gap-6">
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
            {selected ? (
              <div className="space-y-5">
                <div className="flex flex-wrap items-center justify-between gap-4">
                  <div className="space-y-2">
                    <div className="flex items-center gap-3">
                      <h2 className="text-2xl font-semibold text-on-surface">
                        {selected.name}
                      </h2>
                      <StatusBadge
                        pulse={selected.status === "creating" || selected.status === "starting"}
                        tone={toneForStatus(selected.status)}
                      >
                        {labelForStatus(selected.status)}
                      </StatusBadge>
                    </div>
                    <p className="text-sm text-secondary">
                      {selected.provider} / {selected.template}
                      {selected.vm_status ? ` • VM ${selected.vm_status}` : ""}
                    </p>
                  </div>

                  <div className="flex flex-wrap items-center gap-3">
                    <button
                      className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                      disabled={busyAction !== null}
                      onClick={handleStart}
                      type="button"
                    >
                      <Icon name="play_arrow" />
                      Start
                    </button>
                    <button
                      className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                      disabled={busyAction !== null}
                      onClick={handleStop}
                      type="button"
                    >
                      <Icon name="stop" />
                      Stop
                    </button>
                    <button
                      className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-error transition-colors disabled:opacity-50"
                      disabled={busyAction !== null}
                      onClick={handleDelete}
                      type="button"
                    >
                      <Icon name="delete" />
                      Delete
                    </button>
                  </div>
                </div>

                <div className="grid gap-4 md:grid-cols-2">
                  <div className="rounded-xl bg-surface-container p-4">
                    <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                      Shared Directory
                    </p>
                    <p className="mt-2 break-all font-mono text-xs text-secondary">
                      {selected.shared_dir || "—"}
                    </p>
                  </div>
                  <div className="rounded-xl bg-surface-container p-4">
                    <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                      Guest IP
                    </p>
                    <p className="mt-2 font-mono text-xs text-secondary">
                      {selected.ip_address || "Waiting..."}
                    </p>
                  </div>
                  <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
                    <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                      Terminal Command
                    </p>
                    <p className="mt-2 font-mono text-xs text-secondary">
                      {selected.shell || `limactl shell ${selected.name}`}
                    </p>
                  </div>
                  {selected.last_error && (
                    <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error md:col-span-2">
                      {selected.last_error}
                    </div>
                  )}
                </div>
              </div>
            ) : (
              <div className="text-sm text-secondary">
                Select a local runtime to view its status and logs.
              </div>
            )}
          </div>

          <div className="min-h-0 flex-1 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-sm font-bold uppercase tracking-[0.2em] text-outline">
                Provisioning Log
              </h2>
              {selected?.last_log_at && (
                <span className="text-xs text-secondary">
                  Updated {timeAgo(selected.last_log_at)}
                </span>
              )}
            </div>
            <div className="h-[480px] overflow-y-auto rounded-xl bg-[#111315] p-4 font-mono text-xs text-[#d7dadc]">
              {logs.length ? (
                <div className="space-y-1">
                  {logs.map((entry, index) => (
                    <div key={logKey(entry, index)} className="whitespace-pre-wrap break-words">
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
                  {selectedName ? "Waiting for Lima boot output..." : "No local runtime selected."}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
