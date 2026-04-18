import { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from "react";
import { EmptyState } from "../EmptyState";
import { Icon } from "../Icon";

const pageSize = 400;

function isJSONValue(value: string) {
  try {
    JSON.parse(value);
    return true;
  } catch {
    return false;
  }
}

export function KeyListPane({
  emptyDescription = "Create a key to start populating this replicated namespace.",
  emptyTitle = "No keys yet",
  entries,
  loading,
  onSelect,
  onDelete,
  selectedKey,
}: {
  emptyDescription?: string;
  emptyTitle?: string;
  entries: Record<string, string>;
  loading: boolean;
  onSelect: (key: string) => void;
  onDelete: (key: string) => void;
  selectedKey: string | null;
}) {
  const [query, setQuery] = useState("");
  const [contextMenu, setContextMenu] = useState<{
    key: string;
    x: number;
    y: number;
  } | null>(null);
  const [visibleCount, setVisibleCount] = useState(pageSize);
  const paneRef = useRef<HTMLDivElement>(null);
  const deferredQuery = useDeferredValue(query.trim().toLowerCase());

  const keys = useMemo(() => Object.keys(entries).sort(), [entries]);
  const filteredKeys = useMemo(() => {
    if (!deferredQuery) {
      return keys;
    }
    return keys.filter((key) => key.toLowerCase().includes(deferredQuery));
  }, [deferredQuery, keys]);
  const visibleKeys = filteredKeys.slice(0, visibleCount);
  const hiddenCount = Math.max(0, filteredKeys.length - visibleKeys.length);
  const pinnedSelectedKey =
    selectedKey &&
    filteredKeys.includes(selectedKey) &&
    !visibleKeys.includes(selectedKey)
      ? selectedKey
      : null;

  useEffect(() => {
    setVisibleCount(pageSize);
  }, [deferredQuery, entries]);

  const handleContextMenu = useCallback(
    (e: React.MouseEvent, key: string) => {
      e.preventDefault();
      setContextMenu({ key, x: e.clientX, y: e.clientY });
    },
    []
  );

  const closeMenu = useCallback(() => setContextMenu(null), []);
  const hasFilter = deferredQuery.length > 0;

  return (
    <div
      className="flex w-80 flex-col border-r border-outline-variant/10 bg-surface-container-low/50"
      onClick={closeMenu}
      ref={paneRef}
    >
      <div className="border-b border-outline-variant/10 px-4 py-3">
        <div className="space-y-3">
          <div className="flex items-center gap-2 rounded-xl bg-surface-container-lowest px-3 py-2 text-[11px] font-semibold text-secondary shadow-sm">
            <Icon className="text-xs" name="filter_list" />
            Sorted A-Z
          </div>
          <label className="relative block">
            <Icon
              className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-sm text-outline"
              name="search"
            />
            <input
              className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container-lowest px-4 py-2.5 pl-10 text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary"
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Filter keys"
              value={query}
            />
          </label>
          <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-secondary">
            <span>{filteredKeys.length.toLocaleString()} matching keys</span>
            {hiddenCount > 0 && (
              <span>Showing {visibleKeys.length.toLocaleString()} now</span>
            )}
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-2 py-4">
        {loading && filteredKeys.length === 0 && (
          <div className="space-y-2 px-3">
            {[1, 2, 3].map((index) => (
              <div
                key={index}
                className="h-14 animate-pulse rounded-xl bg-surface-container-highest/50"
              />
            ))}
          </div>
        )}

        {!loading && filteredKeys.length === 0 && (
          <EmptyState
            description={
              hasFilter
                ? <>No keys match <span className="font-mono text-on-surface">{query}</span>.</>
                : emptyDescription
            }
            icon="vpn_key"
            title={hasFilter ? "No matching keys" : emptyTitle}
          />
        )}

        {filteredKeys.length > 0 && (
          <div className="space-y-1">
            {pinnedSelectedKey && (
              <button
                className="w-full rounded-xl border border-primary/15 bg-surface-container-lowest px-3 py-3 text-left shadow-sm"
                onClick={() => onSelect(pinnedSelectedKey)}
                onContextMenu={(e) => handleContextMenu(e, pinnedSelectedKey)}
                type="button"
              >
                <div className="mb-1 flex items-start justify-between gap-2">
                  <span className="truncate font-mono text-xs font-semibold text-primary">
                    {pinnedSelectedKey}
                  </span>
                  <span className="shrink-0 rounded bg-primary/10 px-1.5 py-0.5 text-[9px] font-bold uppercase text-primary">
                    selected
                  </span>
                </div>
                <p className="truncate font-mono text-[11px] text-secondary">
                  {entries[pinnedSelectedKey]}
                </p>
              </button>
            )}

            {visibleKeys.map((key) => (
              <button
                className={`w-full rounded-xl px-3 py-3 text-left transition-all ${
                  selectedKey === key
                    ? "border border-primary/15 bg-surface-container-lowest shadow-sm"
                    : "hover:bg-surface-container-highest/50"
                }`}
                key={key}
                onClick={() => onSelect(key)}
                onContextMenu={(e) => handleContextMenu(e, key)}
                type="button"
              >
                <div className="mb-1 flex items-start justify-between gap-2">
                  <span
                    className={`truncate font-mono text-xs font-semibold ${
                      selectedKey === key ? "text-primary" : "text-on-surface"
                    }`}
                  >
                    {key}
                  </span>
                  <span className="shrink-0 rounded bg-surface-container-high px-1.5 py-0.5 text-[9px] font-bold uppercase text-outline">
                    {isJSONValue(entries[key] ?? "") ? "JSON" : "STR"}
                  </span>
                </div>
                <p className="truncate font-mono text-[11px] text-secondary">
                  {entries[key]}
                </p>
              </button>
            ))}

            {hiddenCount > 0 && (
              <button
                className="mt-3 flex w-full items-center justify-center gap-2 rounded-xl border border-outline-variant/20 bg-surface-container-lowest px-3 py-3 text-xs font-semibold text-secondary transition-colors hover:border-primary/20 hover:text-on-surface"
                onClick={() => setVisibleCount((count) => count + pageSize)}
                type="button"
              >
                <Icon className="text-sm" name="expand_more" />
                Load {Math.min(pageSize, hiddenCount).toLocaleString()} More
              </button>
            )}
          </div>
        )}
      </div>

      {contextMenu && (
        <>
          <div className="fixed inset-0 z-40" onClick={closeMenu} onContextMenu={(e) => { e.preventDefault(); closeMenu(); }} />
          <div
            className="fixed z-50 min-w-[160px] rounded-lg border border-outline-variant/15 bg-surface-container-lowest py-1 shadow-xl"
            style={{ top: contextMenu.y, left: contextMenu.x }}
          >
            <button
              className="flex w-full items-center gap-2 px-3 py-2 text-xs font-medium text-error transition-colors hover:bg-error-container/20"
              onClick={() => {
                onDelete(contextMenu.key);
                setContextMenu(null);
              }}
              type="button"
            >
              <Icon className="text-sm" name="delete" />
              Delete "{contextMenu.key}"
            </button>
          </div>
        </>
      )}
    </div>
  );
}
