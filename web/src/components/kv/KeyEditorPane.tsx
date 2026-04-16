import { EmptyState } from "../EmptyState";
import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";

function isJSONValue(value: string) {
  try {
    JSON.parse(value);
    return true;
  } catch {
    return false;
  }
}

export function KeyEditorPane({
  actionError,
  editValue,
  isDirty,
  newKey,
  newValue,
  onCancelNew,
  onChangeEditValue,
  onChangeNewKey,
  onChangeNewValue,
  onCreate,
  onDelete,
  onSave,
  refreshing,
  selectedKey,
  showNew,
}: {
  actionError: string | null;
  editValue: string;
  isDirty: boolean;
  newKey: string;
  newValue: string;
  onCancelNew: () => void;
  onChangeEditValue: (value: string) => void;
  onChangeNewKey: (value: string) => void;
  onChangeNewValue: (value: string) => void;
  onCreate: () => void;
  onDelete: () => void;
  onSave: () => void;
  refreshing: boolean;
  selectedKey: string | null;
  showNew: boolean;
}) {
  if (showNew) {
    return (
      <div className="flex-1 overflow-y-auto p-8">
        <div className="mx-auto max-w-2xl space-y-6">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              New Entry
            </p>
            <h2 className="mt-2 text-2xl font-semibold text-on-surface">
              Create a replicated key
            </h2>
          </div>

          {actionError && (
            <div className="rounded-xl bg-error-container/40 p-4 text-sm text-error">
              {actionError}
            </div>
          )}

          <div className="space-y-2">
            <label className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Key
            </label>
            <input
              className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container-lowest p-3 font-mono text-sm focus:border-primary focus:ring-1 focus:ring-primary"
              onChange={(event) => onChangeNewKey(event.target.value)}
              placeholder="my-key"
              value={newKey}
            />
          </div>

          <div className="space-y-2">
            <label className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Value
            </label>
            <textarea
              className="h-48 w-full resize-none rounded-2xl border border-outline-variant/20 bg-surface-container-lowest p-4 font-mono text-sm focus:border-primary focus:ring-1 focus:ring-primary"
              onChange={(event) => onChangeNewValue(event.target.value)}
              placeholder="value..."
              value={newValue}
            />
          </div>

          <div className="flex gap-2">
            <button
              className="rounded-full bg-gradient-to-br from-primary to-primary-container px-6 py-2 text-sm font-medium text-on-primary shadow-lg shadow-primary/10"
              onClick={onCreate}
              type="button"
            >
              Create
            </button>
            <button
              className="px-6 py-2 text-sm text-secondary transition-colors hover:text-on-surface"
              onClick={onCancelNew}
              type="button"
            >
              Cancel
            </button>
          </div>
        </div>
      </div>
    );
  }

  if (!selectedKey) {
    return (
      <div className="flex flex-1 items-center justify-center p-8">
        <EmptyState
          description="Select an existing key to inspect it, or create a new one from the top bar."
          icon="database"
          title="Select a key"
        />
      </div>
    );
  }

  return (
    <div className="flex flex-1 flex-col overflow-hidden bg-surface">
      <div className="border-b border-outline-variant/10 p-6">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h2 className="text-xl font-semibold tracking-tight text-on-surface">
              {selectedKey}
            </h2>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <span className="font-mono text-xs text-secondary">
                {editValue.length} bytes
              </span>
              {isDirty && <StatusBadge tone="processing">Unsaved</StatusBadge>}
              {refreshing && (
                <StatusBadge icon="sync" tone="neutral">
                  Refreshing
                </StatusBadge>
              )}
            </div>
          </div>
          <div className="flex gap-2">
            <button
              className="flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium text-error transition-colors hover:bg-error-container/20"
              onClick={onDelete}
              type="button"
            >
              <Icon className="text-sm" name="delete" />
              Delete
            </button>
            <button
              className="flex items-center gap-2 rounded-full bg-gradient-to-br from-primary to-primary-container px-6 py-2 text-sm font-medium text-on-primary shadow-lg shadow-primary/10 transition-opacity disabled:cursor-not-allowed disabled:opacity-50"
              disabled={!isDirty}
              onClick={onSave}
              type="button"
            >
              <Icon className="text-sm" name="save" />
              Save Changes
            </button>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-8 py-8">
        {actionError && (
          <div className="mb-6 rounded-xl bg-error-container/40 p-4 text-sm text-error">
            {actionError}
          </div>
        )}
        <div className="flex h-full flex-col gap-6">
          <div className="flex-1 space-y-2">
            <label className="px-1 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Value
            </label>
            <div className="flex h-full min-h-[28rem] flex-col overflow-hidden rounded-2xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
              <div className="flex items-center justify-between bg-surface-container-high px-4 py-2">
                <div className="flex gap-1.5">
                  <div className="h-2.5 w-2.5 rounded-full bg-error/20" />
                  <div className="h-2.5 w-2.5 rounded-full bg-tertiary/20" />
                  <div className="h-2.5 w-2.5 rounded-full bg-primary/20" />
                </div>
                <span className="font-mono text-[10px] uppercase tracking-tight text-secondary">
                  {isJSONValue(editValue) ? "application/json" : "text/plain"}
                </span>
              </div>
              <textarea
                className="flex-1 resize-none bg-transparent p-6 font-mono text-sm leading-relaxed focus:ring-0"
                onChange={(event) => onChangeEditValue(event.target.value)}
                placeholder="Enter value..."
                value={editValue}
              />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
