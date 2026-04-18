import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";

export function NamespaceBar({
  countLabel = "keys",
  keyCount,
  namespace,
  onChangeSystemPrefix,
  onCreate,
  onDeletePattern,
  onToggleSystemValues,
  refreshing,
  showSystemValues,
  systemPrefix,
}: {
  countLabel?: string;
  keyCount: number;
  namespace: string;
  onChangeSystemPrefix: (value: string) => void;
  onCreate: () => void;
  onDeletePattern: () => void;
  onToggleSystemValues: () => void;
  refreshing: boolean;
  showSystemValues: boolean;
  systemPrefix: string;
}) {
  return (
    <div className="border-b border-outline-variant/10 px-8 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <button
          className="rounded-full border border-primary/10 bg-primary-fixed/40 px-4 py-2 text-xs font-semibold text-primary"
          type="button"
        >
          {namespace}
        </button>
        <button
          aria-pressed={showSystemValues}
          className={`inline-flex items-center gap-2 rounded-full border px-4 py-2 text-xs font-semibold transition-colors ${
            showSystemValues
              ? "border-primary/20 bg-primary-fixed/50 text-primary"
              : "border-outline-variant/20 bg-surface-container-lowest text-secondary hover:border-primary/20 hover:text-on-surface"
          }`}
          onClick={onToggleSystemValues}
          type="button"
        >
          <Icon
            className="text-sm"
            filled={showSystemValues}
            name={showSystemValues ? "visibility" : "visibility_off"}
          />
          Show _sys
        </button>
        <div className="ml-auto flex items-center gap-3">
          <span className="font-mono text-[10px] text-secondary">
            {keyCount} {countLabel}
          </span>
          {refreshing ? (
            <StatusBadge icon="sync" tone="neutral">
              Refreshing
            </StatusBadge>
          ) : (
            <StatusBadge pulse tone="live">
              Live
            </StatusBadge>
          )}
          <button
            className="flex items-center gap-2 rounded-full border border-error/25 bg-error/5 px-4 py-2 text-xs font-semibold text-error transition-colors hover:bg-error/10"
            onClick={onDeletePattern}
            type="button"
          >
            <Icon className="text-sm" name="warning" />
            Delete Pattern
          </button>
          <button
            className="flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-xs font-semibold text-on-primary shadow-lg shadow-primary/15"
            onClick={onCreate}
            type="button"
          >
            <Icon className="text-sm" name="add" />
            New Key
          </button>
        </div>
      </div>

      {showSystemValues && (
        <div className="mt-4 flex flex-col gap-3 lg:flex-row lg:items-end">
          <label className="min-w-0 flex-1 space-y-2">
            <span className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Prefix Filter
            </span>
            <div className="relative">
              <Icon
                className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-sm text-outline"
                name="filter_alt"
              />
              <input
                className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 pl-10 font-mono text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary"
                onChange={(event) => onChangeSystemPrefix(event.target.value)}
                placeholder="_sys/something/"
                value={systemPrefix}
              />
            </div>
            <p className="text-[11px] text-secondary">
              Leave blank to list everything, or narrow to a prefix like{" "}
              <span className="font-mono text-on-surface">_sys/secrets/</span>.
            </p>
          </label>
          <button
            className="inline-flex items-center justify-center gap-2 rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 text-xs font-bold uppercase tracking-[0.16em] text-secondary transition-colors hover:bg-surface-container disabled:cursor-not-allowed disabled:opacity-50"
            disabled={!systemPrefix.trim()}
            onClick={() => onChangeSystemPrefix("")}
            type="button"
          >
            <Icon className="text-sm" name="filter_alt_off" />
            Clear Filter
          </button>
        </div>
      )}
    </div>
  );
}
