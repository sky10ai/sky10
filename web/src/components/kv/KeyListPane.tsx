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
  selectedKey,
}: {
  entries: Record<string, string>;
  loading: boolean;
  onSelect: (key: string) => void;
  selectedKey: string | null;
}) {
  const keys = Object.keys(entries).sort();

  return (
    <div className="flex w-80 flex-col border-r border-outline-variant/10 bg-surface-container-low/50">
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
    </div>
  );
}
