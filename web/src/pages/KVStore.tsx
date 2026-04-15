import { useCallback, useEffect, useState } from "react";
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
import { skykv } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function KVStore() {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const [showNew, setShowNew] = useState(false);
  const [isDirty, setIsDirty] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [showSystemValues, setShowSystemValues] = useState(false);
  const [systemPrefix, setSystemPrefix] = useState("");

  const normalizedSystemPrefix = normalizeKVBrowsePrefix(systemPrefix);
  const browseQuery = buildKVBrowseQuery(showSystemValues, normalizedSystemPrefix);
  const systemFilterActive =
    showSystemValues && normalizedSystemPrefix.length > 0;

  const {
    data: allData,
    loading,
    error,
    mutate,
    refreshing,
    refetch,
  } = useRPC(() => skykv.getAll(browseQuery), [showSystemValues, normalizedSystemPrefix], {
    live: KV_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
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
      setSelectedKey(key);
      setEditValue(entries[key] ?? "");
      setIsDirty(false);
    },
    [entries]
  );

  const keyMatchesCurrentView = useCallback(
    (key: string) =>
      matchesKVBrowseView(key, showSystemValues, normalizedSystemPrefix),
    [normalizedSystemPrefix, showSystemValues]
  );

  const saveValue = useCallback(async () => {
    if (!selectedKey) return;

    const previousValue = entries[selectedKey] ?? "";
    setActionError(null);
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

  const deleteKeyByName = useCallback(async (keyToDelete: string) => {
    const deletedKey = keyToDelete;
    const hadKey = Object.prototype.hasOwnProperty.call(entries, deletedKey);
    const visibleKey = hadKey && !isInternalKVKey(deletedKey);

    setActionError(null);
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
        : previous
    );

    try {
      await skykv.delete({ key: deletedKey });
      refetch({ background: true });
      refetchStatus({ background: true });
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : "Failed to delete key");
      refetch();
      refetchStatus();
    }
  }, [entries, mutate, mutateStatus, refetch, refetchStatus]);

  const deleteKey = useCallback(async () => {
    if (!selectedKey) return;
    deleteKeyByName(selectedKey);
  }, [deleteKeyByName, selectedKey]);

  const createKey = useCallback(async () => {
    const key = newKey.trim();
    if (!key) return;

    const existed = Object.prototype.hasOwnProperty.call(entries, key);
    const visibleInCurrentView = keyMatchesCurrentView(key);
    const optimisticStatusCountDelta =
      !systemFilterActive && !isInternalKVKey(key) && !existed ? 1 : 0;

    setActionError(null);
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
        : previous
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
    mutate,
    mutateStatus,
    newKey,
    newValue,
    keyMatchesCurrentView,
    refetch,
    refetchStatus,
    systemFilterActive,
  ]);

  return (
    <div className="flex flex-1 flex-col overflow-hidden">
      <NamespaceBar
        countLabel={systemFilterActive ? "matching" : showSystemValues ? "shown" : "keys"}
        keyCount={displayedKeyCount}
        namespace={kvStatus?.namespace ?? "default"}
        onChangeSystemPrefix={setSystemPrefix}
        onCreate={() => {
          setShowNew(true);
          setActionError(null);
          setSelectedKey(null);
          setNewKey("");
          setNewValue("");
          setIsDirty(false);
        }}
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

      {showSyncWarning && (
        <div className="mx-8 mt-4 rounded-xl border border-warning/30 bg-warning/10 p-4 text-sm text-warning">
          <div className="font-medium">
            KV sync is {kvStatus.sync_state === "waiting" ? "waiting" : "degraded"}
          </div>
          {kvStatus.sync_message && <div className="mt-1">{kvStatus.sync_message}</div>}
          <div className="mt-1 text-xs opacity-80">
            peers: {kvStatus.peer_count}/{kvStatus.expected_peers}
            {kvStatus.nsid ? ` · nsid: ${kvStatus.nsid}` : ""}
          </div>
        </div>
      )}

      <div className="flex flex-1 overflow-hidden">
        <KeyListPane
          emptyDescription={
            systemFilterActive
              ? `No keys matched the prefix "${normalizedSystemPrefix}".`
              : "Create a key to start populating this replicated namespace."
          }
          emptyTitle={systemFilterActive ? "No matching keys" : "No keys yet"}
          entries={entries}
          loading={loading}
          onDelete={deleteKeyByName}
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
            setIsDirty(selectedKey ? value !== (entries[selectedKey] ?? "") : false);
          }}
          onChangeNewKey={setNewKey}
          onChangeNewValue={setNewValue}
          onCreate={createKey}
          onDelete={deleteKey}
          onSave={saveValue}
          refreshing={combinedRefreshing}
          selectedKey={selectedKey}
          showNew={showNew}
        />
      </div>
    </div>
  );
}
