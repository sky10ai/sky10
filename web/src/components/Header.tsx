import { STORAGE_EVENT_TYPES } from "../lib/events";
import { openCommandPalette } from "../lib/commandPalette";
import { skyfs } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";
import { Icon } from "./Icon";
import { StatusBadge } from "./StatusBadge";

export function Header() {
  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const queued = health?.outbox_pending ?? 0;
  const transfers = health?.transfer_pending ?? 0;
  const pending = queued + transfers;
  const isSyncing = pending > 0;

  return (
    <div className="inline-flex items-center gap-2.5">
      {isSyncing ? (
        <StatusBadge icon="sync" pulse tone="processing">
          {transfers > 0 ? `${pending} active` : `${queued} queued`}
        </StatusBadge>
      ) : (
        <StatusBadge pulse tone="live">
          Live
        </StatusBadge>
      )}
      <button
        onClick={openCommandPalette}
        className="group hidden w-[16rem] items-center justify-between gap-3 rounded-full border border-outline-variant/20 bg-surface/82 px-3 py-2 text-left text-xs text-secondary shadow-sm backdrop-blur-md transition-colors hover:border-primary/20 hover:text-on-surface xl:flex xl:w-[20rem]"
        type="button"
      >
        <span className="flex min-w-0 items-center gap-3">
          <Icon
            name="search"
            className="text-lg text-outline group-hover:text-primary"
          />
          <span className="truncate xl:hidden">Search</span>
          <span className="hidden truncate xl:inline">
            Search commands and routes
          </span>
        </span>
        <kbd className="rounded bg-surface-container-lowest px-1.5 py-0.5 font-mono text-[10px] text-outline">
          ⌘K
        </kbd>
      </button>
    </div>
  );
}
