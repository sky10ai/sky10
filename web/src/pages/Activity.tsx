import { useEffect, useRef, useState } from "react";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { STORAGE_EVENT_TYPES, subscribe } from "../lib/events";
import {
  skyfs,
  type SyncActivityEntry,
  type SyncReadSourceEntry,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

interface ActivityEvent {
  id: string;
  event: string;
  detail: string;
  time: Date;
}

let eventSeq = 0;

function eventIcon(event: string): [string, string] {
  if (event.includes("upload")) return ["cloud_upload", "text-blue-500"];
  if (event.includes("download")) return ["cloud_download", "text-emerald-500"];
  if (event.includes("delete") || event.includes("remove"))
    return ["delete", "text-red-400"];
  if (event.includes("sync")) return ["sync", "text-amber-500"];
  if (event.includes("poll")) return ["refresh", "text-secondary"];
  if (event.includes("snapshot")) return ["backup", "text-purple-400"];
  if (event.includes("compact")) return ["compress", "text-secondary"];
  return ["info", "text-secondary"];
}

function formatTime(d: Date) {
  return d.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function pendingPhaseLabel(entry: SyncActivityEntry) {
  if (!entry.phase) {
    return "Queued";
  }
  if (entry.phase === "writing") {
    return entry.direction === "up" ? "Staging" : "Receiving";
  }
  if (entry.phase === "staged") {
    return "Publishing";
  }
  return entry.phase;
}

function pendingIcon(entry: SyncActivityEntry): [string, string] {
  if (entry.op === "put") {
    return eventIcon(entry.direction === "up" ? "upload" : "download");
  }
  return eventIcon(entry.op);
}

function readSourceTone(source?: string) {
  if (source === "peer") return "processing";
  if (source === "s3") return "neutral";
  if (source === "local") return "live";
  return "neutral";
}

function readSourceLabel(source?: string) {
  if (source === "peer") return "Peer";
  if (source === "s3") return "S3";
  if (source === "local") return "Cache";
  return "Unknown";
}

function sourceHealthBadges(entry: SyncReadSourceEntry) {
  const badges: Array<{ label: string; tone: "danger" | "neutral" }> = [];
  if (entry.peer_source_health?.degraded) {
    badges.push({ label: "Peer retry", tone: "danger" });
  }
  if (entry.s3_source_health?.degraded) {
    badges.push({ label: "S3 retry", tone: "danger" });
  }
  if (badges.length === 0 && entry.last_read_source === "s3") {
    badges.push({ label: "Durable path", tone: "neutral" });
  }
  return badges;
}

function transferSourceLabel(source?: string) {
  if (source === "peer") return "Peer";
  if (source === "s3") return "S3";
  if (source === "local") return "Local";
  return "Unknown";
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

function transferProgressLabel(entry: SyncActivityEntry) {
  if (!entry.bytes_total || entry.bytes_total <= 0) {
    return null;
  }
  const done = Math.min(entry.bytes_done ?? 0, entry.bytes_total);
  const pct = Math.round((done / entry.bytes_total) * 100);
  return `${pct}% · ${formatBytes(done)} / ${formatBytes(entry.bytes_total)}`;
}

export default function Activity() {
  const [events, setEvents] = useState<ActivityEvent[]>([]);
  const bottomRef = useRef<HTMLDivElement>(null);

  const { data: activity } = useRPC(() => skyfs.syncActivity(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  // Subscribe to SSE events and append to the log.
  useEffect(() => {
    const unsub = subscribe((event, data) => {
      const detail =
        typeof data === "object" && data !== null
          ? JSON.stringify(data)
          : String(data ?? "");
      setEvents((prev) => {
        const next = [
          ...prev,
          { id: String(++eventSeq), event, detail, time: new Date() },
        ];
        return next.length > 200 ? next.slice(-200) : next;
      });
    });
    return unsub;
  }, []);

  // Auto-scroll to bottom on new events.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [events.length]);

  const pending: SyncActivityEntry[] = activity?.pending ?? [];
  const reads: SyncReadSourceEntry[] = activity?.reads ?? [];

  return (
    <section className="mx-auto flex flex-1 w-full max-w-7xl flex-col gap-8 p-12">
      <PageHeader
        actions={
          <StatusBadge pulse tone="live">
            Streaming
          </StatusBadge>
        }
        description="Real-time sync events and pending operations."
        eyebrow="Monitoring"
        title="Activity"
      />

      {/* Pending operations */}
      {pending.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-xs font-bold uppercase tracking-wider text-secondary">
            Pending ({pending.length})
          </h3>
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-3 shadow-sm">
            {pending.map((entry, i) => {
              const [icon, color] = pendingIcon(entry);
              const progress = transferProgressLabel(entry);
              return (
                <div
                  key={`${entry.drive_id}-${entry.path}-${i}`}
                  className="flex items-center gap-4 px-4 py-3 text-sm"
                >
                  <Icon className={`text-lg ${color}`} name={icon} />
                  <span className="text-xs font-bold uppercase text-secondary">
                    {entry.direction === "up" ? "Upload" : "Download"}
                  </span>
                  <StatusBadge tone={entry.phase ? "processing" : "neutral"}>
                    {pendingPhaseLabel(entry)}
                  </StatusBadge>
                  {entry.active_source && (
                    <StatusBadge tone={readSourceTone(entry.active_source)}>
                      {transferSourceLabel(entry.active_source)}
                    </StatusBadge>
                  )}
                  <span className="flex-1 truncate font-mono text-xs text-on-surface">
                    {entry.path}
                  </span>
                  {progress && (
                    <span className="text-xs text-on-surface-variant">
                      {progress}
                    </span>
                  )}
                  <span className="text-xs text-outline">{entry.drive_name}</span>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {reads.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-xs font-bold uppercase tracking-wider text-secondary">
            Read Sources
          </h3>
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-3 shadow-sm">
            {reads.map((entry) => (
              <div
                key={entry.drive_id}
                className="flex items-center gap-4 px-4 py-3 text-sm"
              >
                <Icon className="text-lg text-secondary" name="source_environment" />
                <span className="w-24 truncate text-xs font-bold uppercase text-secondary">
                  {entry.drive_name}
                </span>
                <StatusBadge tone={readSourceTone(entry.last_read_source)}>
                  {readSourceLabel(entry.last_read_source)}
                </StatusBadge>
                {sourceHealthBadges(entry).map((badge) => (
                  <StatusBadge key={`${entry.drive_id}-${badge.label}`} tone={badge.tone}>
                    {badge.label}
                  </StatusBadge>
                ))}
                <span className="text-xs text-on-surface-variant">
                  Cache {entry.read_local_hits}
                </span>
                <span className="text-xs text-on-surface-variant">
                  Peer {entry.read_peer_hits}
                </span>
                <span className="text-xs text-on-surface-variant">
                  S3 {entry.read_s3_hits}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Live event log */}
      <div className="space-y-2 flex-1 flex flex-col min-h-0">
        <h3 className="text-xs font-bold uppercase tracking-wider text-secondary">
          Event Log ({events.length})
        </h3>
        <div className="flex-1 overflow-y-auto rounded-2xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
          {events.length === 0 ? (
            <div className="flex items-center justify-center py-16 text-sm text-secondary">
              Waiting for events...
            </div>
          ) : (
            <div className="divide-y divide-outline-variant/5">
              {events.map((ev) => {
                const [icon, color] = eventIcon(ev.event);
                return (
                  <div
                    key={ev.id}
                    className="flex items-center gap-4 px-4 py-3 text-sm hover:bg-surface-container-low transition-colors"
                  >
                    <Icon className={`text-lg ${color}`} name={icon} />
                    <span className="w-28 shrink-0 font-mono text-xs text-primary">
                      {ev.event}
                    </span>
                    <span className="flex-1 truncate font-mono text-xs text-on-surface-variant">
                      {ev.detail}
                    </span>
                    <span className="shrink-0 text-xs text-outline">
                      {formatTime(ev.time)}
                    </span>
                  </div>
                );
              })}
              <div ref={bottomRef} />
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
