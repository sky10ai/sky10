import { useCallback, useEffect, useState } from "react";
import { SettingsPage } from "../components/SettingsPage";
import { DeleteKeysDialog } from "../components/kv/DeleteKeysDialog";
import { KeyEditorPane } from "../components/kv/KeyEditorPane";
import { KeyListPane } from "../components/kv/KeyListPane";
import { NamespaceBar } from "../components/kv/NamespaceBar";
import { KV_EVENT_TYPES } from "../lib/events";
import {
  buildKVBrowseQuery,
  isInternalKVKey,
  matchesKVBrowseView,
  normalizeKVBrowsePrefix,
} from "../lib/kvBrowse";
import { skykv, type KVDeleteMatchingResult } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

type DeleteDialogState =
  | { mode: "single"; key: string }
  | { mode: "pattern"; key: null };

export default function KVStore() {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const [showNew, setShowNew] = useState(false);
  const [isDirty, setIsDirty] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionNotice, setActionNotice] = useState<string | null>(null);
  const [showSystemValues, setShowSystemValues] = useState(false);
  const [systemPrefix, setSystemPrefix] = useState("");
  const [deleteDialog, setDeleteDialog] = useState<DeleteDialogState | null>(
    null,
  );
  const [deletePattern, setDeletePattern] = useState("");
  const [deleteIncludeInternal, setDeleteIncludeInternal] = useState(false);
  const [deleteConfirmText, setDeleteConfirmText] = useState("");
  const [deleteDialogError, setDeleteDialogError] = useState<string | null>(
    null,
  );
  const [deletePreview, setDeletePreview] =
    useState<KVDeleteMatchingResult | null>(null);
  const [deletePreviewing, setDeletePreviewing] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const normalizedSystemPrefix = normalizeKVBrowsePrefix(systemPrefix);
  const browseQuery = buildKVBrowseQuery(
    showSystemValues,
    normalizedSystemPrefix,
  );
  const systemFilterActive =
    showSystemValues && normalizedSystemPrefix.length > 0;

  const {
    data: allData,
    loading,
    error,
    mutate,
    refreshing,
    refetch,
  } = useRPC(
    () => skykv.getAll(browseQuery),
    [showSystemValues, normalizedSystemPrefix],
    {
      live: KV_EVENT_TYPES,
      refreshIntervalMs: 10_000,
    },
  );
  const {
    data: kvStatus,
    mutate: mutateStatus,
    refetch: refetchStatus,
    refreshing: statusRefreshing,
  } = useRPC(() => skykv.status(), [], {
    live: KV_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const entries = allData?.entries ?? {};
  const displayedKeyCount = allData?.count ?? kvStatus?.keys ?? 0;
  const combinedRefreshing = refreshing || statusRefreshing;
  const showSyncWarning = kvStatus && kvStatus.sync_state !== "ok";

  const resetDeleteDialog = useCallback(() => {
    setDeleteDialog(null);
    setDeletePattern("");
    setDeleteIncludeInternal(false);
    setDeleteConfirmText("");
    setDeleteDialogError(null);
    setDeletePreview(null);
    setDeletePreviewing(false);
    setDeleteBusy(false);
  }, []);

  useEffect(() => {
    if (!selectedKey || showNew) return;

    const nextValue = entries[selectedKey];
    if (typeof nextValue !== "string") {
      setSelectedKey(null);
      setEditValue("");
      setIsDirty(false);
      return;
    }

    if (!isDirty) {
      setEditValue(nextValue);
    }
  }, [entries, isDirty, selectedKey, showNew]);

  const selectKey = useCallback(
    (key: string) => {
      setShowNew(false);
      setActionError(null);
      setActionNotice(null);
      setSelectedKey(key);
      setEditValue(entries[key] ?? "");
      setIsDirty(false);
    },
    [entries],
  );

  const keyMatchesCurrentView = useCallback(
    (key: string) =>
      matchesKVBrowseView(key, showSystemValues, normalizedSystemPrefix),
    [normalizedSystemPrefix, showSystemValues],
  );

  const saveValue = useCallback(async () => {
    if (!selectedKey) return;

    const previousValue = entries[selectedKey] ?? "";
    setActionError(null);
    setActionNotice(null);
    setIsDirty(false);

    mutate((previous) => {
      if (!previous) return previous;
      return {
        ...previous,
        entries: {
          ...previous.entries,
          [selectedKey]: editValue,
        },
      };
    });

    try {
      await skykv.set({ key: selectedKey, value: editValue });
      refetch({ background: true });
      refetchStatus({ background: true });
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : "Failed to save value");
      setEditValue(previousValue);
      setIsDirty(false);
      refetch();
      refetchStatus();
    }
  }, [editValue, entries, mutate, refetch, refetchStatus, selectedKey]);

  const deleteKeyByName = useCallback(
    async (keyToDelete: string) => {
      const deletedKey = keyToDelete;
      const hadKey = Object.prototype.hasOwnProperty.call(entries, deletedKey);
      const visibleKey = hadKey && !isInternalKVKey(deletedKey);

      setActionError(null);
      setActionNotice(null);
      setSelectedKey(null);
      setEditValue("");
      setIsDirty(false);

      mutate((previous) => {
        if (!previous) return previous;

        const nextEntries = { ...previous.entries };
        delete nextEntries[deletedKey];

        return {
          ...previous,
          count: Math.max(0, previous.count - (hadKey ? 1 : 0)),
          entries: nextEntries,
        };
      });

      mutateStatus((previous) =>
        previous
          ? {
              ...previous,
              keys: Math.max(0, previous.keys - (visibleKey ? 1 : 0)),
            }
          : previous,
      );

      try {
        await skykv.delete({ key: deletedKey });
        refetch({ background: true });
        refetchStatus({ background: true });
      } catch (e: unknown) {
        refetch();
        refetchStatus();
        throw e instanceof Error
          ? e
          : new Error(`Failed to delete key "${deletedKey}"`);
      }
    },
    [entries, mutate, mutateStatus, refetch, refetchStatus],
  );

  const openSingleDeleteDialog = useCallback((key: string) => {
    setActionError(null);
    setActionNotice(null);
    setDeleteDialog({ mode: "single", key });
    setDeletePattern("");
    setDeleteIncludeInternal(false);
    setDeleteConfirmText("");
    setDeleteDialogError(null);
    setDeletePreview(null);
    setDeletePreviewing(false);
    setDeleteBusy(false);
  }, []);

  const openPatternDeleteDialog = useCallback(() => {
    const suggestedPattern = normalizedSystemPrefix
      ? `${normalizedSystemPrefix}*`
      : "";
    setActionError(null);
    setActionNotice(null);
    setDeleteDialog({ mode: "pattern", key: null });
    setDeletePattern(suggestedPattern);
    setDeleteIncludeInternal(showSystemValues);
    setDeleteConfirmText("");
    setDeleteDialogError(null);
    setDeletePreview(null);
    setDeletePreviewing(false);
    setDeleteBusy(false);
  }, [normalizedSystemPrefix, showSystemValues]);

  const handleDeletePatternChange = useCallback((value: string) => {
    setDeletePattern(value);
    setDeleteConfirmText("");
    setDeleteDialogError(null);
    setDeletePreview(null);
  }, []);

  const handleDeleteIncludeInternalChange = useCallback((value: boolean) => {
    setDeleteIncludeInternal(value);
    setDeleteConfirmText("");
    setDeleteDialogError(null);
    setDeletePreview(null);
  }, []);

  const previewPatternDelete = useCallback(async () => {
    const pattern = deletePattern.trim();
    if (!pattern) {
      setDeleteDialogError("Enter a pattern before previewing matches.");
      setDeletePreview(null);
      return;
    }

    setDeleteDialogError(null);
    setDeletePreviewing(true);
    setActionNotice(null);

    try {
      const result = await skykv.deleteMatching({
        pattern,
        dry_run: true,
        include_internal: deleteIncludeInternal,
      });
      setDeletePreview(result);
      setDeleteConfirmText("");
    } catch (e: unknown) {
      setDeleteDialogError(
        e instanceof Error ? e.message : "Failed to preview matching keys",
      );
      setDeletePreview(null);
    } finally {
      setDeletePreviewing(false);
    }
  }, [deleteIncludeInternal, deletePattern]);

  const confirmDelete = useCallback(async () => {
    if (!deleteDialog) {
      return;
    }

    setDeleteBusy(true);
    setDeleteDialogError(null);
    setActionError(null);
    setActionNotice(null);

    try {
      if (deleteDialog.mode === "single") {
        await deleteKeyByName(deleteDialog.key);
        setActionNotice(`Deleted "${deleteDialog.key}".`);
      } else {
        const pattern = deletePattern.trim();
        const result = await skykv.deleteMatching({
          pattern,
          include_internal: deleteIncludeInternal,
        });
        refetch({ background: true });
        refetchStatus({ background: true });
        setActionNotice(
          result.count === 1
            ? `Deleted 1 key matching "${pattern}".`
            : `Deleted ${result.count} keys matching "${pattern}".`,
        );
      }
      resetDeleteDialog();
    } catch (e: unknown) {
      setDeleteDialogError(
        e instanceof Error ? e.message : "Failed to delete keys",
      );
    } finally {
      setDeleteBusy(false);
    }
  }, [
    deleteDialog,
    deleteIncludeInternal,
    deleteKeyByName,
    deletePattern,
    refetch,
    refetchStatus,
    resetDeleteDialog,
  ]);

  const createKey = useCallback(async () => {
    const key = newKey.trim();
    if (!key) return;

    const existed = Object.prototype.hasOwnProperty.call(entries, key);
    const visibleInCurrentView = keyMatchesCurrentView(key);
    const optimisticStatusCountDelta =
      !systemFilterActive && !isInternalKVKey(key) && !existed ? 1 : 0;

    setActionError(null);
    setActionNotice(null);
    setShowNew(false);
    setSelectedKey(visibleInCurrentView ? key : null);
    setEditValue(newValue);
    setIsDirty(false);

    mutate((previous) => {
      if (!previous) return previous;
      return {
        ...previous,
        count: previous.count + (visibleInCurrentView && !existed ? 1 : 0),
        entries: visibleInCurrentView
          ? {
              ...previous.entries,
              [key]: newValue,
            }
          : previous.entries,
      };
    });

    mutateStatus((previous) =>
      previous
        ? {
            ...previous,
            keys: previous.keys + optimisticStatusCountDelta,
          }
        : previous,
    );

    setNewKey("");
    setNewValue("");

    try {
      await skykv.set({ key, value: newValue });
      refetch({ background: true });
      refetchStatus({ background: true });
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : "Failed to create key");
      refetch();
      refetchStatus();
    }
  }, [
    entries,
    keyMatchesCurrentView,
    mutate,
    mutateStatus,
    newKey,
    newValue,
    refetch,
    refetchStatus,
    systemFilterActive,
  ]);

  return (
    <SettingsPage
      backHref="/settings"
      description="Inspect replicated keys and edit live values."
      pinnablePageID="kv"
      title="Key-Value"
      width="wide"
    >
      <div className="flex min-h-[72vh] flex-1 flex-col overflow-hidden rounded-3xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
        <NamespaceBar
          countLabel={
            systemFilterActive
              ? "matching"
              : showSystemValues
                ? "shown"
                : "keys"
          }
          keyCount={displayedKeyCount}
          namespace={kvStatus?.namespace ?? "default"}
          onChangeSystemPrefix={setSystemPrefix}
          onCreate={() => {
            setShowNew(true);
            setActionError(null);
            setActionNotice(null);
            setSelectedKey(null);
            setNewKey("");
            setNewValue("");
            setIsDirty(false);
          }}
          onDeletePattern={openPatternDeleteDialog}
          onToggleSystemValues={() => {
            setShowSystemValues((previous) => !previous);
          }}
          refreshing={combinedRefreshing}
          showSystemValues={showSystemValues}
          systemPrefix={systemPrefix}
        />

        {error && (
          <div className="mx-8 mt-4 rounded-xl bg-error-container/20 p-4 text-sm text-error">
            {error}
          </div>
        )}

        {actionNotice && (
          <div className="mx-8 mt-4 rounded-xl border border-primary/20 bg-primary/10 p-4 text-sm text-primary">
            {actionNotice}
          </div>
        )}

        {showSyncWarning && (
          <div className="mx-8 mt-4 rounded-xl border border-warning/30 bg-warning/10 p-4 text-sm text-warning">
            <div className="font-medium">
              KV sync is{" "}
              {kvStatus.sync_state === "waiting" ? "waiting" : "degraded"}
            </div>
            {kvStatus.sync_message && (
              <div className="mt-1">{kvStatus.sync_message}</div>
            )}
            <div className="mt-1 text-xs opacity-80">
              peers: {kvStatus.peer_count}/{kvStatus.expected_peers}
              {kvStatus.nsid ? ` · nsid: ${kvStatus.nsid}` : ""}
            </div>
          </div>
        )}

        <div className="flex min-h-0 flex-1 overflow-hidden">
          <KeyListPane
            emptyDescription={
              systemFilterActive
                ? `No keys matched the prefix "${normalizedSystemPrefix}".`
                : "Create a key to start populating this replicated namespace."
            }
            emptyTitle={systemFilterActive ? "No matching keys" : "No keys yet"}
            entries={entries}
            loading={loading}
            onDelete={openSingleDeleteDialog}
            onSelect={selectKey}
            selectedKey={selectedKey}
          />
          <KeyEditorPane
            actionError={actionError}
            editValue={editValue}
            isDirty={isDirty}
            newKey={newKey}
            newValue={newValue}
            onCancelNew={() => {
              setShowNew(false);
              setActionError(null);
            }}
            onChangeEditValue={(value) => {
              setEditValue(value);
              setIsDirty(
                selectedKey ? value !== (entries[selectedKey] ?? "") : false,
              );
            }}
            onChangeNewKey={setNewKey}
            onChangeNewValue={setNewValue}
            onCreate={createKey}
            onDelete={() => {
              if (selectedKey) {
                openSingleDeleteDialog(selectedKey);
              }
            }}
            onSave={saveValue}
            refreshing={combinedRefreshing}
            selectedKey={selectedKey}
            showNew={showNew}
          />
        </div>
      </div>

      <DeleteKeysDialog
        busy={deleteBusy}
        confirmationText={deleteConfirmText}
        dialogError={deleteDialogError}
        includeInternal={deleteIncludeInternal}
        mode={deleteDialog?.mode ?? null}
        onChangeConfirmationText={setDeleteConfirmText}
        onChangeIncludeInternal={handleDeleteIncludeInternalChange}
        onChangePattern={handleDeletePatternChange}
        onClose={resetDeleteDialog}
        onConfirm={confirmDelete}
        onPreview={previewPatternDelete}
        open={deleteDialog !== null}
        pattern={deletePattern}
        preview={deletePreview}
        previewing={deletePreviewing}
        targetKey={deleteDialog?.key ?? null}
      />
    </SettingsPage>
  );
}
