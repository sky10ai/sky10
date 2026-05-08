import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import {
  secrets,
  type SecretDevice,
  type SecretReference,
  type SecretRecord,
  type SecretSummary,
} from "../lib/rpc";
import { formatBytes, timeAgo, useRPC } from "../lib/useRPC";

type DraftScope = "current" | "trusted" | "explicit";
type DraftSource = "value" | "file";

interface DraftFile {
  bytes: Uint8Array;
  contentType: string;
  name: string;
}

function messagingRefs(
  summary: Pick<SecretSummary, "references">,
): SecretReference[] {
  return (summary.references ?? []).filter(
    (ref) => ref.manager === "messaging",
  );
}

export default function SettingsSecrets() {
  const [searchParams] = useSearchParams();
  const didPrefillFromQuery = useRef(false);
  const {
    data: listData,
    error: listError,
    loading: listLoading,
    refetch: refetchList,
    refreshing: listRefreshing,
  } = useRPC(() => secrets.list(), [], {
    live: ["messaging:event"],
    refreshIntervalMs: 10_000,
  });
  const {
    data: devicesData,
    error: devicesError,
    refetch: refetchDevices,
  } = useRPC(() => secrets.devices(), [], {
    refreshIntervalMs: 10_000,
  });
  const {
    data: status,
    error: statusError,
    refetch: refetchStatus,
  } = useRPC(() => secrets.status(), [], {
    refreshIntervalMs: 10_000,
  });

  const [draftName, setDraftName] = useState("");
  const [draftKind, setDraftKind] = useState("api-key");
  const [draftScope, setDraftScope] = useState<DraftScope>("current");
  const [draftSource, setDraftSource] = useState<DraftSource>("value");
  const [draftValue, setDraftValue] = useState("");
  const [draftContentType, setDraftContentType] = useState("");
  const [draftRecipients, setDraftRecipients] = useState<string[]>([]);
  const [draftFile, setDraftFile] = useState<DraftFile | null>(null);

  const [selectedSecretID, setSelectedSecretID] = useState<string | null>(null);
  const [rewrapScope, setRewrapScope] = useState<DraftScope>("current");
  const [rewrapRecipients, setRewrapRecipients] = useState<string[]>([]);

  const [activeSecret, setActiveSecret] = useState<SecretRecord | null>(null);
  const [loadingSecret, setLoadingSecret] = useState(false);
  const [storing, setStoring] = useState(false);
  const [rewrapping, setRewrapping] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [syncing, setSyncing] = useState(false);

  const [actionError, setActionError] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);

  const items = listData?.items ?? [];
  const devices = devicesData?.devices ?? [];
  const trustedDevices = useMemo(
    () => devices.filter((device) => device.role !== "sandbox"),
    [devices],
  );
  const sandboxDevices = useMemo(
    () => devices.filter((device) => device.role === "sandbox"),
    [devices],
  );
  const selectedSecret = useMemo(
    () => items.find((item) => item.id === selectedSecretID) ?? null,
    [items, selectedSecretID],
  );

  useEffect(() => {
    if (didPrefillFromQuery.current) return;
    const nextName = searchParams.get("name");
    const nextKind = searchParams.get("kind");
    if (!nextName && !nextKind) {
      didPrefillFromQuery.current = true;
      return;
    }
    if (nextName) setDraftName(nextName);
    if (nextKind) setDraftKind(nextKind);
    didPrefillFromQuery.current = true;
  }, [searchParams]);

  useEffect(() => {
    if (!selectedSecret) {
      setRewrapScope("current");
      setRewrapRecipients([]);
      setActiveSecret(null);
      return;
    }
    setRewrapScope(selectedSecret.scope);
    setRewrapRecipients(selectedSecret.recipient_device_ids);
    setActiveSecret((previous) =>
      previous && previous.id === selectedSecret.id ? previous : null,
    );
  }, [selectedSecret]);

  const devicesByID = useMemo(() => {
    const next = new Map<string, SecretDevice>();
    for (const device of devices) {
      next.set(device.id, device);
    }
    return next;
  }, [devices]);

  const visibleRecipientLabels = useCallback(
    (recipientIDs: string[]) => {
      return recipientIDs.map((id) => devicesByID.get(id)?.name || id);
    },
    [devicesByID],
  );

  const clearDraftPayload = useCallback(() => {
    setDraftValue("");
    setDraftFile(null);
    setDraftContentType("");
  }, []);

  const refetchAll = useCallback(() => {
    refetchList({ background: true });
    refetchDevices({ background: true });
    refetchStatus({ background: true });
  }, [refetchDevices, refetchList, refetchStatus]);

  const handleFileChange = useCallback(
    async (file: File | null) => {
      if (!file) {
        setDraftFile(null);
        return;
      }
      const bytes = new Uint8Array(await file.arrayBuffer());
      setDraftFile({
        bytes,
        contentType: file.type || "application/octet-stream",
        name: file.name,
      });
      setDraftSource("file");
      if (!draftContentType) {
        setDraftContentType(file.type || "application/octet-stream");
      }
    },
    [draftContentType],
  );

  const handleStore = useCallback(async () => {
    const trimmedName = draftName.trim();
    if (!trimmedName) {
      setActionError("Name is required.");
      return;
    }

    let payload: Uint8Array;
    let contentType = draftContentType.trim();
    if (draftSource === "file") {
      if (!draftFile) {
        setActionError("Choose a file to store.");
        return;
      }
      payload = draftFile.bytes;
      if (!contentType) {
        contentType = draftFile.contentType || "application/octet-stream";
      }
    } else {
      if (!draftValue) {
        setActionError("Value is required.");
        return;
      }
      payload = new TextEncoder().encode(draftValue);
      if (!contentType) {
        contentType = "text/plain; charset=utf-8";
      }
    }

    if (draftScope === "explicit" && draftRecipients.length === 0) {
      setActionError(
        "Select at least one recipient device for explicit scope.",
      );
      return;
    }

    setStoring(true);
    setActionError(null);
    setActionMessage(null);
    try {
      const stored = await secrets.put({
        name: trimmedName,
        kind: draftKind.trim() || "blob",
        content_type: contentType,
        scope: draftScope,
        payload: bytesToBase64(payload),
        recipient_devices:
          draftScope === "explicit" ? draftRecipients : undefined,
      });
      setActionMessage(`Stored ${stored.name} with ${stored.scope} scope.`);
      setSelectedSecretID(stored.id);
      if (draftSource === "value") {
        setDraftValue("");
      } else {
        setDraftFile(null);
      }
      refetchAll();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Store failed");
    } finally {
      setStoring(false);
    }
  }, [
    draftContentType,
    draftFile,
    draftKind,
    draftName,
    draftRecipients,
    draftScope,
    draftSource,
    draftValue,
    refetchAll,
  ]);

  const handleReveal = useCallback(async () => {
    if (!selectedSecret) return;
    setLoadingSecret(true);
    setActionError(null);
    try {
      const record = await secrets.get({ id_or_name: selectedSecret.id });
      setActiveSecret(record);
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Reveal failed");
    } finally {
      setLoadingSecret(false);
    }
  }, [selectedSecret]);

  const handleDownload = useCallback(() => {
    if (!activeSecret) return;
    const bytes = base64ToBytes(activeSecret.payload);
    const contentType = activeSecret.content_type || "application/octet-stream";
    const extension = inferFileExtension(activeSecret);
    const filename = extension
      ? `${activeSecret.name}.${extension}`
      : activeSecret.name;
    downloadBytes(bytes, filename, contentType);
  }, [activeSecret]);

  const handleRewrap = useCallback(async () => {
    if (!selectedSecret) return;
    if (rewrapScope === "explicit" && rewrapRecipients.length === 0) {
      setActionError(
        "Select at least one recipient device for explicit scope.",
      );
      return;
    }

    setRewrapping(true);
    setActionError(null);
    setActionMessage(null);
    try {
      const updated = await secrets.rewrap({
        id_or_name: selectedSecret.id,
        scope: rewrapScope,
        recipient_devices:
          rewrapScope === "explicit" ? rewrapRecipients : undefined,
      });
      setActionMessage(
        `Updated ${updated.name} recipient scope to ${updated.scope}.`,
      );
      setSelectedSecretID(updated.id);
      refetchAll();
      if (activeSecret?.id === updated.id) {
        setActiveSecret(null);
      }
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Rewrap failed");
    } finally {
      setRewrapping(false);
    }
  }, [
    activeSecret?.id,
    refetchAll,
    rewrapRecipients,
    rewrapScope,
    selectedSecret,
  ]);

  const handleSync = useCallback(async () => {
    setSyncing(true);
    setActionError(null);
    setActionMessage(null);
    try {
      await secrets.sync();
      setActionMessage("Secrets sync requested.");
      refetchAll();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Sync failed");
    } finally {
      setSyncing(false);
    }
  }, [refetchAll]);

  const handleDelete = useCallback(async () => {
    if (!selectedSecret) return;
    if (
      !window.confirm(
        `Delete secret "${selectedSecret.name}"? This removes it from synced secrets storage.`,
      )
    ) {
      return;
    }

    setDeleting(true);
    setActionError(null);
    setActionMessage(null);
    try {
      await secrets.delete({ id_or_name: selectedSecret.id });
      setActionMessage(`Deleted ${selectedSecret.name}.`);
      setSelectedSecretID(null);
      setActiveSecret(null);
      refetchAll();
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [refetchAll, selectedSecret]);

  const preview = useMemo(() => {
    if (!activeSecret) return null;
    const bytes = base64ToBytes(activeSecret.payload);
    const text = maybeDecodeText(bytes, activeSecret.content_type);
    return { bytes, text };
  }, [activeSecret]);

  const sandboxRecipientWarning = useMemo(() => {
    const ids = new Set([
      ...(draftScope === "explicit" ? draftRecipients : []),
      ...(rewrapScope === "explicit" ? rewrapRecipients : []),
    ]);
    return devices.filter(
      (device) => ids.has(device.id) && device.role === "sandbox",
    );
  }, [devices, draftRecipients, draftScope, rewrapRecipients, rewrapScope]);

  return (
    <SettingsPage
      actions={
        <button
          className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95 disabled:opacity-60"
          disabled={syncing}
          onClick={() => void handleSync()}
          type="button"
        >
          <Icon
            className={syncing ? "animate-spin text-base" : "text-base"}
            name="sync"
          />
          {syncing ? "Syncing..." : "Sync Now"}
        </button>
      }
      backHref="/settings"
      description="Manage encrypted secrets and device access."
      pinnablePageID="secrets"
      title="Secrets"
      width="wide"
    >
      {(actionError || listError || devicesError || statusError) && (
        <div className="rounded-2xl bg-error-container/20 p-4 text-sm text-error">
          {actionError ?? listError ?? devicesError ?? statusError}
        </div>
      )}

      {actionMessage && (
        <div className="rounded-2xl bg-primary/10 p-4 text-sm text-primary">
          {actionMessage}
        </div>
      )}

      {sandboxRecipientWarning.length > 0 && (
        <div className="rounded-2xl border border-warning/30 bg-warning/10 p-4 text-sm text-warning">
          Sandbox recipients can decrypt the plaintext secret directly:{" "}
          {sandboxRecipientWarning.map((device) => device.name).join(", ")}.
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard
          icon="key_vertical"
          label="Visible Secrets"
          tone="processing"
          value={String(status?.count ?? items.length)}
        />
        <MetricCard
          icon="verified_user"
          label="Trusted Devices"
          tone="success"
          value={String(trustedDevices.length)}
        />
        <MetricCard
          icon="deployed_code"
          label="Sandbox Devices"
          tone="neutral"
          value={String(sandboxDevices.length)}
        />
        <MetricCard
          icon="database"
          label="Namespace"
          tone="neutral"
          value={status?.namespace ?? "secrets"}
          detail={status?.device_id ? `device ${status.device_id}` : undefined}
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.3fr)_380px]">
        <section
          className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm scroll-mt-24"
          id="store-secret"
        >
          <div className="space-y-6">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Store Secret
              </p>
              <h2 className="text-2xl font-semibold text-on-surface">
                Add a new value or roll a new version by name
              </h2>
              <p className="max-w-2xl text-sm text-secondary">
                Reusing the same secret name writes a new version. Names and
                payloads stay out of raw KV keys and visible values.
              </p>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <label className="space-y-2 text-sm text-secondary">
                <span className="font-medium text-on-surface">Name</span>
                <input
                  className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container px-4 py-3 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                  onChange={(event) => setDraftName(event.target.value)}
                  placeholder="openai"
                  value={draftName}
                />
              </label>
              <label className="space-y-2 text-sm text-secondary">
                <span className="font-medium text-on-surface">Kind</span>
                <input
                  className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container px-4 py-3 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                  onChange={(event) => setDraftKind(event.target.value)}
                  placeholder="api-key"
                  value={draftKind}
                />
              </label>
            </div>

            <ScopeSelector
              currentLabel="Current device only"
              onChange={setDraftScope}
              scope={draftScope}
              trustedLabel="All trusted devices"
            />

            {draftScope === "explicit" && (
              <DevicePicker
                devices={devices}
                onChange={setDraftRecipients}
                selected={draftRecipients}
              />
            )}

            <div className="space-y-3">
              <p className="text-sm font-medium text-on-surface">Payload</p>
              <div className="inline-flex rounded-full border border-outline-variant/20 bg-surface-container p-1">
                <SourceButton
                  active={draftSource === "value"}
                  label="Value"
                  onClick={() => {
                    setDraftSource("value");
                    setDraftFile(null);
                  }}
                />
                <SourceButton
                  active={draftSource === "file"}
                  label="File"
                  onClick={() => {
                    setDraftSource("file");
                    setDraftValue("");
                  }}
                />
              </div>

              {draftSource === "value" ? (
                <textarea
                  className="min-h-40 w-full rounded-3xl border border-outline-variant/20 bg-surface-container px-5 py-4 font-mono text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                  onChange={(event) => setDraftValue(event.target.value)}
                  placeholder="Paste an API key, DSN, or token value here."
                  value={draftValue}
                />
              ) : (
                <label className="flex cursor-pointer flex-col gap-3 rounded-3xl border border-dashed border-outline-variant/30 bg-surface-container p-6 text-sm text-secondary transition-colors hover:border-primary/30 hover:text-on-surface">
                  <div className="flex items-center gap-3">
                    <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                      <Icon className="text-2xl" name="upload_file" />
                    </div>
                    <div className="space-y-1">
                      <p className="font-semibold text-on-surface">
                        {draftFile ? draftFile.name : "Choose a file"}
                      </p>
                      <p>
                        {draftFile
                          ? `${formatBytes(draftFile.bytes.byteLength)} • ${draftFile.contentType || "application/octet-stream"}`
                          : "Drag a cert, backup, or other small artifact into the secrets store."}
                      </p>
                    </div>
                  </div>
                  <input
                    className="hidden"
                    onChange={(event) => {
                      void handleFileChange(event.target.files?.[0] ?? null);
                    }}
                    type="file"
                  />
                </label>
              )}
            </div>

            <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_auto]">
              <label className="space-y-2 text-sm text-secondary">
                <span className="font-medium text-on-surface">
                  Content Type Override
                </span>
                <input
                  className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container px-4 py-3 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                  onChange={(event) => setDraftContentType(event.target.value)}
                  placeholder={
                    draftSource === "value"
                      ? "text/plain; charset=utf-8"
                      : "application/octet-stream"
                  }
                  value={draftContentType}
                />
              </label>
              <div className="flex items-end">
                <button
                  className="inline-flex items-center gap-2 rounded-full bg-primary px-6 py-3 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                  disabled={storing}
                  onClick={() => void handleStore()}
                  type="button"
                >
                  <Icon
                    name={storing ? "sync" : "save"}
                    className={storing ? "animate-spin text-base" : "text-base"}
                  />
                  {storing ? "Storing..." : "Store Secret"}
                </button>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3 text-xs text-secondary">
              <button
                className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-3 py-1.5 font-semibold transition-colors hover:text-on-surface"
                onClick={clearDraftPayload}
                type="button"
              >
                <Icon className="text-sm" name="ink_eraser" />
                Clear payload
              </button>
              <span>
                Trusted scope automatically follows trusted-device joins.
              </span>
            </div>
          </div>
        </section>

        <aside className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
          <div className="space-y-6">
            <div>
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Recipient Model
              </p>
              <h2 className="mt-2 text-xl font-semibold text-on-surface">
                Device trust classes
              </h2>
            </div>
            <div className="space-y-3 text-sm text-secondary">
              <p>
                <strong className="text-on-surface">Current</strong> keeps the
                secret pinned to this device only.
              </p>
              <p>
                <strong className="text-on-surface">Trusted</strong> includes
                current and future trusted devices after reconciliation.
              </p>
              <p>
                <strong className="text-on-surface">Explicit</strong> pins
                custody to the exact devices you select.
              </p>
            </div>

            <div className="space-y-3">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Available Devices
              </p>
              {devices.length === 0 ? (
                <div className="rounded-2xl bg-surface-container p-4 text-sm text-secondary">
                  No devices available yet.
                </div>
              ) : (
                <div className="space-y-3">
                  {devices.map((device) => (
                    <div
                      key={device.id}
                      className="rounded-2xl bg-surface-container p-4"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <div className="space-y-1">
                          <p className="font-semibold text-on-surface">
                            {device.name}
                            {device.current ? " (current)" : ""}
                          </p>
                          <p className="text-xs text-secondary">{device.id}</p>
                        </div>
                        <StatusBadge
                          tone={
                            device.role === "sandbox" ? "neutral" : "success"
                          }
                        >
                          {device.role}
                        </StatusBadge>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </aside>
      </div>

      <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="mb-5 flex items-center justify-between gap-4">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Inventory
            </p>
            <h2 className="mt-2 text-2xl font-semibold text-on-surface">
              Secrets visible to this device
            </h2>
          </div>
          <div className="flex items-center gap-3">
            {listRefreshing && (
              <StatusBadge icon="sync" tone="neutral">
                Refreshing
              </StatusBadge>
            )}
            <span className="rounded-full bg-surface-container px-3 py-1 text-xs font-semibold text-secondary">
              {items.length}
            </span>
          </div>
        </div>

        {listLoading ? (
          <div className="rounded-2xl bg-surface-container p-6 text-sm text-secondary">
            Loading secrets...
          </div>
        ) : items.length === 0 ? (
          <div className="rounded-2xl bg-surface-container p-6 text-sm text-secondary">
            No secrets yet. Store one above to start syncing API keys or small
            private files across your trusted devices.
          </div>
        ) : (
          <div className="space-y-3">
            {items.map((item) => {
              const expanded = item.id === selectedSecret?.id;
              const detailID = `secret-${item.id}-configuration`;
              const refs = messagingRefs(item);

              return (
                <details
                  key={item.id}
                  className={`overflow-hidden rounded-2xl border transition-all ${
                    expanded
                      ? "border-primary/30 bg-primary/5 shadow-sm"
                      : "border-outline-variant/10 bg-surface-container hover:bg-surface-container-high"
                  }`}
                  open={expanded}
                >
                  <summary
                    aria-controls={detailID}
                    aria-expanded={expanded}
                    className="grid cursor-pointer list-none gap-4 px-5 py-4 text-left transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary/40 [&::-webkit-details-marker]:hidden"
                    onClick={(event) => {
                      event.preventDefault();
                      setSelectedSecretID(expanded ? null : item.id);
                    }}
                  >
                    <div className="flex flex-wrap items-start justify-between gap-4">
                      <div className="min-w-0 space-y-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <h3 className="break-all text-lg font-semibold text-on-surface">
                            {item.name}
                          </h3>
                          <StatusBadge tone="processing">
                            {item.scope}
                          </StatusBadge>
                          <StatusBadge tone="neutral">{item.kind}</StatusBadge>
                          {refs.length > 0 && (
                            <StatusBadge tone="success" icon="forum">
                              Managed by Messaging
                            </StatusBadge>
                          )}
                        </div>
                        <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-secondary">
                          <span>{formatBytes(item.size)}</span>
                          <span>{item.content_type}</span>
                          <span>Updated {timeAgo(item.updated_at)}</span>
                        </div>
                        <p className="break-all text-xs text-secondary">
                          Recipients:{" "}
                          {visibleRecipientLabels(
                            item.recipient_device_ids,
                          ).join(", ")}
                        </p>
                      </div>
                      <div className="flex items-center gap-3">
                        <span className="font-mono text-xs text-secondary">
                          {truncateSHA(item.sha256)}
                        </span>
                        <span
                          className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full transition-colors ${
                            expanded
                              ? "bg-primary/10 text-primary"
                              : "bg-surface-container-high text-secondary"
                          }`}
                        >
                          <Icon
                            className={`text-base transition-transform ${
                              expanded ? "" : "-rotate-90"
                            }`}
                            name="expand_more"
                          />
                        </span>
                      </div>
                    </div>
                  </summary>

                  {expanded && selectedSecret && (
                    <div
                      className="border-t border-outline-variant/10 bg-surface-container-lowest px-5 py-5"
                      id={detailID}
                    >
                      <div className="space-y-6">
                        {refs.length > 0 && (
                          <div className="rounded-2xl border border-primary/20 bg-primary/5 p-4 text-sm text-on-surface">
                            <div className="flex items-center gap-2 font-semibold text-primary">
                              <Icon className="text-base" name="forum" />
                              Managed by Messaging
                            </div>
                            <p className="mt-1 text-secondary">
                              Edit or rotate this secret from the Messaging tab
                              so the connection stays in sync.
                            </p>
                            <ul className="mt-2 space-y-1">
                              {refs.map((ref, idx) => (
                                <li
                                  key={`${ref.kind}:${ref.subject ?? ""}:${idx}`}
                                  className="text-secondary"
                                >
                                  <span className="font-medium text-on-surface">
                                    {ref.subject || "(unnamed connection)"}
                                  </span>
                                  {ref.detail ? ` — ${ref.detail}` : ""}
                                </li>
                              ))}
                            </ul>
                            <Link
                              className="mt-3 inline-flex items-center gap-1 text-primary hover:underline"
                              to={refs[0]?.route ?? "/settings/messaging"}
                            >
                              Open Messaging settings
                              <Icon
                                className="text-xs"
                                name="arrow_forward"
                              />
                            </Link>
                          </div>
                        )}

                        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                          <InfoTile
                            label="Size"
                            value={formatBytes(selectedSecret.size)}
                          />
                          <InfoTile
                            label="Updated"
                            value={timeAgo(selectedSecret.updated_at)}
                          />
                          <InfoTile
                            label="Recipients"
                            value={String(
                              selectedSecret.recipient_device_ids.length,
                            )}
                          />
                          <InfoTile
                            label="SHA-256"
                            mono
                            value={truncateSHA(selectedSecret.sha256)}
                          />
                        </div>

                        <div className="space-y-3">
                          <div className="flex flex-wrap items-center gap-3">
                            <button
                              className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                              disabled={loadingSecret}
                              onClick={() => void handleReveal()}
                              type="button"
                            >
                              <Icon
                                className={
                                  loadingSecret
                                    ? "animate-spin text-base"
                                    : "text-base"
                                }
                                name={loadingSecret ? "sync" : "visibility"}
                              />
                              {loadingSecret
                                ? "Loading..."
                                : activeSecret?.id === selectedSecret.id
                                  ? "Refresh Value"
                                  : "Reveal Value"}
                            </button>
                            <button
                              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                              disabled={activeSecret?.id !== selectedSecret.id}
                              onClick={handleDownload}
                              type="button"
                            >
                              <Icon className="text-base" name="download" />
                              Download
                            </button>
                            <button
                              className="inline-flex items-center gap-2 rounded-full border border-error/30 px-4 py-2 text-sm font-semibold text-error transition-colors active:scale-95 disabled:opacity-60"
                              disabled={deleting}
                              onClick={() => void handleDelete()}
                              type="button"
                            >
                              <Icon
                                className={
                                  deleting
                                    ? "animate-spin text-base"
                                    : "text-base"
                                }
                                name={deleting ? "sync" : "delete"}
                              />
                              {deleting ? "Deleting..." : "Delete Secret"}
                            </button>
                          </div>

                          {activeSecret?.id === selectedSecret.id && preview ? (
                            preview.text !== null ? (
                              <pre className="max-h-72 overflow-auto rounded-3xl bg-surface-container-high p-5 font-mono text-xs text-on-surface">
                                {preview.text}
                              </pre>
                            ) : (
                              <div className="rounded-3xl bg-surface-container-high p-5 text-sm text-secondary">
                                Binary payload loaded. Use Download to write the
                                bytes locally.
                              </div>
                            )
                          ) : (
                            <div className="rounded-3xl bg-surface-container-high p-5 text-sm text-secondary">
                              Reveal only loads the selected secret into this
                              browser session on demand.
                            </div>
                          )}
                        </div>

                        <div className="space-y-4">
                          <div className="space-y-2">
                            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                              Recipient Scope
                            </p>
                            <ScopeSelector
                              currentLabel="Current device only"
                              onChange={setRewrapScope}
                              scope={rewrapScope}
                              trustedLabel="All trusted devices"
                            />
                          </div>

                          {rewrapScope === "explicit" && (
                            <DevicePicker
                              devices={devices}
                              onChange={setRewrapRecipients}
                              selected={rewrapRecipients}
                            />
                          )}

                          <button
                            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors active:scale-95 disabled:opacity-60"
                            disabled={rewrapping}
                            onClick={() => void handleRewrap()}
                            type="button"
                          >
                            <Icon
                              className={
                                rewrapping
                                  ? "animate-spin text-base"
                                  : "text-base"
                              }
                              name="sync_lock"
                            />
                            {rewrapping ? "Updating..." : "Update Recipients"}
                          </button>
                        </div>
                      </div>
                    </div>
                  )}
                </details>
              );
            })}
          </div>
        )}
      </section>
    </SettingsPage>
  );
}

function MetricCard({
  detail,
  icon,
  label,
  tone,
  value,
}: {
  detail?: string;
  icon: string;
  label: string;
  tone: "neutral" | "processing" | "success";
  value: string;
}) {
  const iconTone =
    tone === "success"
      ? "bg-primary-fixed/60 text-on-primary-fixed-variant"
      : tone === "processing"
        ? "bg-primary/10 text-primary"
        : "bg-surface-container text-secondary";

  return (
    <div className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-2">
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            {label}
          </p>
          <p className="text-3xl font-semibold tracking-tight text-on-surface">
            {value}
          </p>
          {detail && <p className="text-xs text-secondary">{detail}</p>}
        </div>
        <div
          className={`flex h-12 w-12 items-center justify-center rounded-2xl ${iconTone}`}
        >
          <Icon className="text-2xl" name={icon} />
        </div>
      </div>
    </div>
  );
}

function ScopeSelector({
  currentLabel,
  onChange,
  scope,
  trustedLabel,
}: {
  currentLabel: string;
  onChange: (scope: DraftScope) => void;
  scope: DraftScope;
  trustedLabel: string;
}) {
  const options: Array<{ description: string; value: DraftScope }> = [
    { value: "current", description: currentLabel },
    { value: "trusted", description: trustedLabel },
    { value: "explicit", description: "Choose exact devices" },
  ];

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium text-on-surface">Scope</p>
      <div className="grid gap-3 md:grid-cols-3">
        {options.map((option) => {
          const active = scope === option.value;
          return (
            <button
              key={option.value}
              className={`rounded-2xl border px-4 py-4 text-left transition-all ${
                active
                  ? "border-primary/30 bg-primary/10 shadow-sm"
                  : "border-outline-variant/10 bg-surface-container hover:bg-surface-container-high"
              }`}
              onClick={() => onChange(option.value)}
              type="button"
            >
              <div className="space-y-1">
                <p className="font-semibold text-on-surface">{option.value}</p>
                <p className="text-sm text-secondary">{option.description}</p>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function DevicePicker({
  devices,
  onChange,
  selected,
}: {
  devices: SecretDevice[];
  onChange: (selected: string[]) => void;
  selected: string[];
}) {
  const selectedSet = new Set(selected);
  return (
    <div className="space-y-3">
      <p className="text-sm font-medium text-on-surface">Recipient Devices</p>
      <div className="space-y-2">
        {devices.map((device) => {
          const checked = selectedSet.has(device.id);
          return (
            <label
              key={device.id}
              className={`flex cursor-pointer items-center justify-between gap-3 rounded-2xl border px-4 py-3 transition-all ${
                checked
                  ? "border-primary/30 bg-primary/10"
                  : "border-outline-variant/10 bg-surface-container hover:bg-surface-container-high"
              }`}
            >
              <div className="space-y-1">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-on-surface">
                    {device.name}
                    {device.current ? " (current)" : ""}
                  </span>
                  <StatusBadge
                    tone={device.role === "sandbox" ? "neutral" : "success"}
                  >
                    {device.role}
                  </StatusBadge>
                </div>
                <p className="text-xs text-secondary">{device.id}</p>
              </div>
              <input
                checked={checked}
                className="h-4 w-4 accent-primary"
                onChange={() => {
                  if (checked) {
                    onChange(selected.filter((id) => id !== device.id));
                    return;
                  }
                  onChange([...selected, device.id]);
                }}
                type="checkbox"
              />
            </label>
          );
        })}
      </div>
    </div>
  );
}

function SourceButton({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      className={`rounded-full px-4 py-2 text-sm font-semibold transition-colors ${
        active
          ? "bg-primary text-on-primary"
          : "text-secondary hover:text-on-surface"
      }`}
      onClick={onClick}
      type="button"
    >
      {label}
    </button>
  );
}

function InfoTile({
  label,
  mono = false,
  value,
}: {
  label: string;
  mono?: boolean;
  value: string;
}) {
  return (
    <div className="rounded-2xl bg-surface-container-high p-4">
      <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
        {label}
      </p>
      <p
        className={`mt-2 text-sm text-on-surface ${mono ? "font-mono" : "font-semibold"}`}
      >
        {value}
      </p>
    </div>
  );
}

function bytesToBase64(bytes: Uint8Array) {
  let binary = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    const chunk = bytes.subarray(offset, offset + chunkSize);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

function base64ToBytes(value: string) {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

function maybeDecodeText(bytes: Uint8Array, contentType: string) {
  const normalized = contentType.toLowerCase();
  const likelyText =
    normalized.startsWith("text/") ||
    normalized.includes("json") ||
    normalized.includes("xml") ||
    normalized.includes("yaml") ||
    normalized.includes("toml") ||
    normalized.includes("x-www-form-urlencoded");

  if (!likelyText && !isPrintableASCII(bytes)) {
    return null;
  }

  try {
    return new TextDecoder().decode(bytes);
  } catch {
    return null;
  }
}

function isPrintableASCII(bytes: Uint8Array) {
  for (const value of bytes) {
    if (value === 9 || value === 10 || value === 13) continue;
    if (value < 0x20 || value > 0x7e) {
      return false;
    }
  }
  return true;
}

function truncateSHA(value: string) {
  if (value.length <= 16) return value;
  return `${value.slice(0, 8)}...${value.slice(-8)}`;
}

function inferFileExtension(secret: SecretSummary) {
  const contentType = secret.content_type.toLowerCase();
  if (contentType.includes("json")) return "json";
  if (contentType.includes("pem")) return "pem";
  if (contentType.startsWith("text/")) return "txt";
  if (secret.kind === "cert") return "crt";
  return "";
}

function downloadBytes(
  bytes: Uint8Array,
  filename: string,
  contentType: string,
) {
  const copy = new Uint8Array(bytes.byteLength);
  copy.set(bytes);
  const blob = new Blob([copy.buffer], { type: contentType });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}
