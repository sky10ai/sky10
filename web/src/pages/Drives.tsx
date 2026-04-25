import { useState } from "react";
import { useNavigate } from "react-router";
import { BucketAccessCard } from "../components/drives/BucketAccessCard";
import { DriveCard } from "../components/drives/DriveCard";
import { NewDriveForm } from "../components/drives/NewDriveForm";
import { EmptyState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import {
  PageDescription,
  PageHeader,
  PageTitle,
} from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { skyfs, system } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function Drives() {
  const navigate = useNavigate();
  const [showNewDrive, setShowNewDrive] = useState(false);
  const {
    data: driveList,
    loading,
    error,
    refreshing,
    refetch,
  } = useRPC(() => skyfs.driveList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: health } = useRPC(() => system.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const drives = driveList?.drives ?? [];
  const queued = health?.outbox_pending ?? 0;
  const transfers = health?.transfer_pending ?? 0;
  const waiting = health?.sync_waiting_drives ?? 0;
  const degraded = health?.sync_error_drives ?? 0;
  const pathIssues = health?.path_issue_drives ?? 0;
  const conflicts = health?.conflict_drives ?? 0;

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 px-6 pb-12 pt-6 sm:px-8 sm:pt-7 lg:px-10">
      <PageHeader
        actions={
          <>
            {degraded > 0 && (
              <StatusBadge icon="error" tone="danger">
                {degraded} degraded
              </StatusBadge>
            )}
            {pathIssues > 0 && (
              <StatusBadge icon="warning" tone="danger">
                {pathIssues} path issue{pathIssues === 1 ? "" : "s"}
              </StatusBadge>
            )}
            {conflicts > 0 && (
              <StatusBadge icon="warning" tone="danger">
                {conflicts} conflict{conflicts === 1 ? "" : "s"}
              </StatusBadge>
            )}
            {waiting > 0 && (
              <StatusBadge icon="schedule" tone="neutral">
                {waiting} waiting
              </StatusBadge>
            )}
            {transfers > 0 && (
              <StatusBadge icon="sync" pulse tone="processing">
                {transfers} transfer{transfers === 1 ? "" : "s"}
              </StatusBadge>
            )}
            {queued > 0 ? (
              <StatusBadge icon="sync" pulse tone="processing">
                {queued} queued
              </StatusBadge>
            ) : transfers === 0 ? (
              <StatusBadge pulse tone="live">
                Live
              </StatusBadge>
            ) : null}
            {refreshing && (
              <StatusBadge icon="sync" tone="neutral">
                Refreshing
              </StatusBadge>
            )}
            <button
              className="flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary shadow-lg shadow-primary/20 transition-colors hover:bg-primary/90"
              onClick={() => setShowNewDrive(true)}
              type="button"
            >
              <Icon name="add" />
              New Drive
            </button>
          </>
        }
      >
        <PageTitle>Drives</PageTitle>
        <PageDescription>
          {drives.length === 0
            ? "No drives are configured yet. Create a drive to start syncing files."
            : `Managing ${drives.length} encrypted volume${drives.length === 1 ? "" : "s"} with live sync status.`}
        </PageDescription>
      </PageHeader>

      {error && (
        <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
          {error}
        </div>
      )}

      {showNewDrive && (
        <NewDriveForm
          onCancel={() => setShowNewDrive(false)}
          onCreated={() => {
            setShowNewDrive(false);
            refetch();
          }}
        />
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

      {!loading && drives.length === 0 && !showNewDrive ? (
        <EmptyState
          action={
            <button
              className="rounded-full bg-primary px-5 py-2 text-sm font-semibold text-on-primary shadow-lg shadow-primary/20"
              onClick={() => setShowNewDrive(true)}
              type="button"
            >
              Create Your First Drive
            </button>
          }
          description="Create a drive to start syncing encrypted files across your devices."
          icon="folder_open"
          title="No drives yet"
        />
      ) : (
        <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
          {drives.map((drive) => (
            <DriveCard
              drive={drive}
              key={drive.id}
              onChanged={() => refetch({ background: true })}
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
