import { useCallback, useRef, useState } from "react";
import { EmptyState } from "../EmptyState";
import { Icon } from "../Icon";

function isJSONValue(value: string) {
  try {
    JSON.parse(value);
    return true;
  } catch {
    return false;
  }
}

export function KeyListPane({
  entries,
  loading,
  onSelect,
  onDelete,
  selectedKey,
}: {
  entries: Record<string, string>;
  loading: boolean;
  onSelect: (key: string) => void;
  onDelete: (key: string) => void;
  selectedKey: string | null;
}) {
  const keys = Object.keys(entries).sort();
  const [contextMenu, setContextMenu] = useState<{
    key: string;
    x: number;
    y: number;
  } | null>(null);
  const paneRef = useRef<HTMLDivElement>(null);

  const handleContextMenu = useCallback(
    (e: React.MouseEvent, key: string) => {
      e.preventDefault();
      setContextMenu({ key, x: e.clientX, y: e.clientY });
    },
    []
  );

  const closeMenu = useCallback(() => setContextMenu(null), []);

  return (
    <div
      className="flex w-80 flex-col border-r border-outline-variant/10 bg-surface-container-low/50"
      onClick={closeMenu}
      ref={paneRef}
    >
      <div className="border-b border-outline-variant/10 px-4 py-3">
        <div className="flex items-center gap-2 rounded-xl bg-surface-container-lowest px-3 py-2 text-[11px] font-semibold text-secondary shadow-sm">
          <Icon className="text-xs" name="filter_list" />
          Sorted A-Z
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-2 py-4">
        {loading && keys.length === 0 && (
          <div className="space-y-2 px-3">
            {[1, 2, 3].map((index) => (
              <div
                key={index}
                className="h-14 animate-pulse rounded-xl bg-surface-container-highest/50"
              />
            ))}
          </div>
        )}

        {!loading && keys.length === 0 && (
          <EmptyState
            description="Create a key to start populating this replicated namespace."
            icon="vpn_key"
            title="No keys yet"
          />
        )}

        {keys.length > 0 && (
          <div className="space-y-1">
            {keys.map((key) => (
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
