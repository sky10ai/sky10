import { useNavigate } from "react-router";
import { BucketAccessCard } from "../components/drives/BucketAccessCard";
import { DriveCard } from "../components/drives/DriveCard";
import { EmptyState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { openCommandPalette } from "../lib/commandPalette";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { skyfs } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function Drives() {
  const navigate = useNavigate();
  const { data: driveList, loading, error, refreshing } = useRPC(
    () => skyfs.driveList(),
    [],
    {
      live: STORAGE_EVENT_TYPES,
      refreshIntervalMs: 10_000,
    }
  );
  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const drives = driveList?.drives ?? [];
  const pending = health?.outbox_pending ?? 0;

  return (
    <section className="mx-auto flex flex-1 w-full max-w-7xl flex-col gap-10 p-12">
      <PageHeader
        actions={
          <>
            {pending > 0 ? (
              <StatusBadge icon="sync" pulse tone="processing">
                {pending} queued
              </StatusBadge>
            ) : (
              <StatusBadge pulse tone="live">
                Live
              </StatusBadge>
            )}
            {refreshing && (
              <StatusBadge icon="sync" tone="neutral">
                Refreshing
              </StatusBadge>
            )}
            <button
              className="flex items-center gap-2 rounded-full bg-surface-container-high px-4 py-2 text-sm font-medium text-on-surface transition-colors hover:bg-surface-container-highest"
              onClick={openCommandPalette}
              type="button"
            >
              <Icon name="search" />
              Command Palette
            </button>
          </>
        }
        description={
          drives.length === 0
            ? "No drives are configured yet. Once a drive is mounted, its sync state and file counts will update here automatically."
            : `Managing ${drives.length} encrypted volume${drives.length === 1 ? "" : "s"} with live sync status.`
        }
        eyebrow="Encrypted Storage"
        title="Drives"
      />

      {error && (
        <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
          {error}
        </div>
      )}

      {loading && drives.length === 0 && (
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
          {[1, 2, 3].map((index) => (
            <div
              key={index}
              className="h-[220px] animate-pulse rounded-2xl bg-surface-container-lowest"
            />
          ))}
        </div>
      )}

      {!loading && drives.length === 0 ? (
        <EmptyState
          action={
            <button
              className="rounded-full bg-primary px-5 py-2 text-sm font-semibold text-white shadow-lg shadow-primary/20"
              onClick={openCommandPalette}
              type="button"
            >
              Open Command Palette
            </button>
          }
          description="Mount a drive and it will appear here with file counts, running state, and sync activity."
          icon="folder_open"
          title="No drives yet"
        />
      ) : (
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
          {drives.map((drive) => (
            <DriveCard
              drive={drive}
              key={drive.id}
              onOpen={(nextDrive) =>
                navigate(`/drives/${encodeURIComponent(nextDrive.name)}`)
              }
            />
          ))}
        </div>
      )}

      <div className="mt-auto pt-10">
        <BucketAccessCard />
      </div>
    </section>
  );
}
