import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";

export function NamespaceBar({
  keyCount,
  namespace,
  onCreate,
  refreshing,
}: {
  keyCount: number;
  namespace: string;
  onCreate: () => void;
  refreshing: boolean;
}) {
  return (
    <div className="flex items-center gap-3 border-b border-outline-variant/10 px-8 py-4">
      <button
        className="rounded-full border border-primary/10 bg-primary-fixed/40 px-4 py-2 text-xs font-semibold text-primary"
        type="button"
      >
        {namespace}
      </button>
      <div className="ml-auto flex items-center gap-3">
        <span className="font-mono text-[10px] text-secondary">
          {keyCount} keys
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
          className="flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-xs font-semibold text-white shadow-lg shadow-primary/15"
          onClick={onCreate}
          type="button"
        >
          <Icon className="text-sm" name="add" />
          New Key
        </button>
      </div>
    </div>
  );
}
