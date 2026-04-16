import { useState } from "react";
import { type Drive, skyfs } from "../../lib/rpc";
import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";

function syncTone(state?: string) {
  if (state === "error") return "danger";
  if (state === "waiting") return "neutral";
  if (state === "ok") return "live";
  return "neutral";
}

function syncLabel(state?: string) {
  if (state === "error") return "Degraded";
  if (state === "waiting") return "Waiting";
  if (state === "ok") return "Ready";
  if (state === "stopped") return "Stopped";
  return "Unknown";
}

export function DriveCard({
  drive,
  onOpen,
  onChanged,
}: {
  drive: Drive;
  onOpen: (drive: Drive) => void;
  onChanged?: () => void;
}) {
  const isSyncing = drive.outbox_pending > 0 || drive.transfer_pending > 0;
  const totalReads =
    (drive.read_local_hits ?? 0) +
    (drive.read_peer_hits ?? 0) +
    (drive.read_s3_hits ?? 0);
  const sourceWarnings = [
    drive.peer_source_health?.degraded ? "Peer retrying" : "",
    drive.s3_source_health?.degraded ? "S3 retrying" : "",
  ].filter(Boolean);
  const conflictFiles = drive.conflict_files ?? 0;
  const syncSummary =
    drive.sync_state === "error"
      ? drive.sync_message || drive.last_sync_error || "FS sync degraded"
      : drive.sync_state === "waiting"
        ? drive.sync_message || "Waiting for anti-entropy"
        : drive.last_sync_peer
          ? `Anti-entropy ok with ${drive.last_sync_peer}`
          : drive.peer_count && drive.peer_count > 0
            ? `${drive.peer_count} peer${drive.peer_count === 1 ? "" : "s"} connected`
            : "";
  const [toggling, setToggling] = useState(false);

  const toggleDrive = async (event: React.MouseEvent) => {
    event.stopPropagation();
    setToggling(true);
    try {
      if (drive.running) {
        await skyfs.driveStop({ name: drive.name });
      } else {
        await skyfs.driveStart({ name: drive.name });
      }
      onChanged?.();
    } finally {
      setToggling(false);
    }
  };

  return (
    <button
      className="group relative overflow-hidden rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 text-left transition-all duration-300 hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-xl"
      onClick={() => onOpen(drive)}
      type="button"
    >
      <div className="absolute inset-x-0 top-0 h-1 bg-gradient-to-r from-primary via-primary-container to-tertiary-container opacity-0 transition-opacity group-hover:opacity-100" />
      <div className="flex items-start justify-between gap-4">
        <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary transition-colors group-hover:bg-primary group-hover:text-white">
          <Icon name="folder_open" className="text-3xl" />
        </div>
        <div className="flex items-center gap-2">
          {drive.running ? (
            isSyncing ? (
              <StatusBadge icon="sync" pulse tone="processing">
                Syncing
              </StatusBadge>
            ) : (
              <StatusBadge icon="check_circle" tone="live">
                Synced
              </StatusBadge>
            )
          ) : (
            <StatusBadge tone="neutral">Stopped</StatusBadge>
          )}
          <button
            className={`flex h-8 w-8 items-center justify-center rounded-full transition-colors ${
              drive.running
                ? "bg-error/10 text-error hover:bg-error/20"
                : "bg-primary/10 text-primary hover:bg-primary/20"
            } ${toggling ? "opacity-50" : ""}`}
            disabled={toggling}
            onClick={toggleDrive}
            title={drive.running ? "Stop drive" : "Start drive"}
            type="button"
          >
            <Icon className="text-base" name={drive.running ? "stop" : "play_arrow"} />
          </button>
        </div>
      </div>

      <div className="mt-6">
        <h3 className="text-xl font-semibold text-on-surface">{drive.name}</h3>
        <p className="mt-2 text-sm text-secondary">
          {drive.snapshot_files} file{drive.snapshot_files === 1 ? "" : "s"}
          {drive.outbox_pending > 0 ? ` • ${drive.outbox_pending} pending` : ""}
          {drive.transfer_pending > 0 ? ` • ${drive.transfer_pending} transfer${drive.transfer_pending === 1 ? "" : "s"}` : ""}
        </p>
        {totalReads > 0 && (
          <p className="mt-2 text-xs text-outline">
            Reads: cache {drive.read_local_hits} • peer {drive.read_peer_hits} • s3{" "}
            {drive.read_s3_hits}
            {drive.last_read_source ? ` • last ${drive.last_read_source}` : ""}
          </p>
        )}
        {sourceWarnings.length > 0 && (
          <p className="mt-2 text-xs text-error">
            Source health: {sourceWarnings.join(" • ")}
          </p>
        )}
        {conflictFiles > 0 && (
          <div className="mt-3 flex items-center gap-2">
            <StatusBadge tone="danger">
              {conflictFiles} conflict{conflictFiles === 1 ? "" : "s"}
            </StatusBadge>
            <span className="text-xs text-outline">
              Conflict copies are present in this drive.
            </span>
          </div>
        )}
        {drive.running && drive.sync_state && (
          <div className="mt-3 flex items-center gap-2">
            <StatusBadge tone={syncTone(drive.sync_state)}>{syncLabel(drive.sync_state)}</StatusBadge>
            {syncSummary && <span className="text-xs text-outline">{syncSummary}</span>}
          </div>
        )}
      </div>

      <div className="mt-8 border-t border-outline-variant/10 pt-4">
        <p className="truncate font-mono text-[11px] text-outline">
          {drive.local_path}
        </p>
      </div>
    </button>
  );
}
