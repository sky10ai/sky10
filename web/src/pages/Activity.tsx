import { useEffect, useRef, useState } from "react";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { STORAGE_EVENT_TYPES, subscribe } from "../lib/events";
import { skyfs, type SyncActivityEntry } from "../lib/rpc";
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
    }, STORAGE_EVENT_TYPES);
    return unsub;
  }, []);

  // Auto-scroll to bottom on new events.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [events.length]);

  const pending: SyncActivityEntry[] = activity?.pending ?? [];

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
              const [icon, color] = eventIcon(entry.op);
              return (
                <div
                  key={`${entry.drive_id}-${entry.path}-${i}`}
                  className="flex items-center gap-4 px-4 py-3 text-sm"
                >
                  <Icon className={`text-lg ${color}`} name={icon} />
                  <span className="text-xs font-bold uppercase text-secondary">
                    {entry.direction === "up" ? "Upload" : "Download"}
                  </span>
                  <span className="flex-1 truncate font-mono text-xs text-on-surface">
                    {entry.path}
                  </span>
                  <span className="text-xs text-outline">{entry.drive_name}</span>
                </div>
              );
            })}
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
