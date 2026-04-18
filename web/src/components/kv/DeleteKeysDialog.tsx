import { useEffect } from "react";
import type { KVDeleteMatchingResult } from "../../lib/rpc";
import { Icon } from "../Icon";

type DeleteDialogMode = "single" | "pattern";

export function DeleteKeysDialog({
  busy,
  confirmationText,
  dialogError,
  includeInternal,
  mode,
  onChangeConfirmationText,
  onChangeIncludeInternal,
  onChangePattern,
  onClose,
  onConfirm,
  onPreview,
  open,
  pattern,
  preview,
  previewing,
  targetKey,
}: {
  busy: boolean;
  confirmationText: string;
  dialogError: string | null;
  includeInternal: boolean;
  mode: DeleteDialogMode | null;
  onChangeConfirmationText: (value: string) => void;
  onChangeIncludeInternal: (value: boolean) => void;
  onChangePattern: (value: string) => void;
  onClose: () => void;
  onConfirm: () => void;
  onPreview: () => void;
  open: boolean;
  pattern: string;
  preview: KVDeleteMatchingResult | null;
  previewing: boolean;
  targetKey: string | null;
}) {
  useEffect(() => {
    if (!open) {
      return;
    }

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy && !previewing) {
        onClose();
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [busy, onClose, open, previewing]);

  if (!open || !mode) {
    return null;
  }

  const trimmedPattern = pattern.trim();
  const confirmationTarget = mode === "single" ? targetKey ?? "" : trimmedPattern;
  const confirmationReady =
    confirmationTarget.length > 0 && confirmationText === confirmationTarget;
  const previewKeys = mode === "single"
    ? targetKey ? [targetKey] : []
    : preview?.keys ?? [];
  const previewCount = mode === "single" ? previewKeys.length : preview?.count ?? 0;
  const canDelete = mode === "single"
    ? confirmationReady && previewCount === 1 && !busy
    : confirmationReady && previewCount > 0 && !busy && !previewing;
  const deleteLabel = mode === "single"
    ? "Delete Key"
    : previewCount === 1
      ? "Delete 1 Key"
      : `Delete ${previewCount} Keys`;
  const warningTitle = mode === "single"
    ? "Delete this replicated key"
    : "Delete every key matching this pattern";
  const warningBody = mode === "single"
    ? "This writes a delete tombstone into the live KV namespace. Connected devices will converge on the removal."
    : "This writes delete tombstones across the live KV namespace. Bulk deletes replicate to connected devices and do not have a one-click undo.";

  return (
    <div className="fixed inset-0 z-[90]" role="presentation">
      <div
        className="absolute inset-0 bg-on-surface/40 backdrop-blur-sm"
        onClick={busy || previewing ? undefined : onClose}
      />

      <div className="absolute inset-x-4 top-5 mx-auto max-w-5xl sm:top-8">
        <div
          aria-labelledby="kv-delete-dialog-title"
          aria-modal="true"
          className="overflow-hidden rounded-[32px] border border-outline-variant/20 bg-surface-container-high shadow-2xl"
          role="dialog"
        >
          <div className="flex items-start justify-between gap-4 border-b border-outline-variant/10 px-6 py-5">
            <div className="space-y-1">
              <p className="text-[10px] font-black uppercase tracking-[0.26em] text-error">
                Danger Zone
              </p>
              <h2
                className="text-2xl font-semibold tracking-tight text-on-surface"
                id="kv-delete-dialog-title"
              >
                {warningTitle}
              </h2>
              <p className="max-w-2xl text-sm text-secondary">
                Review the target carefully before you arm this action.
              </p>
            </div>
            <button
              aria-label="Close delete dialog"
              className="inline-flex h-11 w-11 items-center justify-center rounded-full border border-outline-variant/20 text-secondary transition-colors hover:border-error/25 hover:text-error disabled:cursor-not-allowed disabled:opacity-40"
              disabled={busy || previewing}
              onClick={onClose}
              type="button"
            >
              <Icon name="close" />
            </button>
          </div>

          <div className="grid gap-6 p-6 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)]">
            <section className="space-y-5">
              <div className="overflow-hidden rounded-[28px] border border-error/30 bg-[radial-gradient(circle_at_top_left,rgba(239,68,68,0.18),rgba(239,68,68,0.05)_42%,transparent_78%)] p-6">
                <div className="flex items-start gap-4">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl bg-error/15 text-error">
                    <Icon className="text-[30px]" name="warning" />
                  </div>
                  <div className="space-y-3">
                    <p className="text-[11px] font-black uppercase tracking-[0.24em] text-error">
                      Live replicated delete
                    </p>
                    <h3 className="text-2xl font-semibold leading-tight text-on-surface">
                      {warningTitle}
                    </h3>
                    <p className="max-w-2xl text-sm leading-6 text-on-surface/80">
                      {warningBody}
                    </p>
                    <div className="grid gap-3 text-xs text-on-surface/75 sm:grid-cols-2">
                      <div className="rounded-2xl border border-error/20 bg-surface-container-lowest/70 p-4">
                        Matching is literal plus globs.
                        {" "}
                        <span className="font-mono text-on-surface">*</span>
                        {" "}
                        matches any run, including
                        {" "}
                        <span className="font-mono text-on-surface">/</span>
                        .
                      </div>
                      <div className="rounded-2xl border border-error/20 bg-surface-container-lowest/70 p-4">
                        There is no bulk undo flow in the KV UI. Recovering means writing keys back manually.
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <div className="rounded-[28px] border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
                <div className="flex flex-wrap items-start justify-between gap-4">
                  <div className="space-y-1">
                    <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                      Match Review
                    </p>
                    <h3 className="text-lg font-semibold text-on-surface">
                      {mode === "single" ? "1 key is queued for deletion" : `${previewCount} keys matched`}
                    </h3>
                    <p className="text-sm text-secondary">
                      {mode === "single"
                        ? "The delete button stays locked until you type the exact key name."
                        : preview
                          ? "Preview results are frozen until you edit the pattern again."
                          : "Run a preview before the delete button becomes available."}
                    </p>
                  </div>
                  {mode === "pattern" && (
                    <div className={`rounded-full px-3 py-1 text-[10px] font-bold uppercase tracking-[0.18em] ${
                      includeInternal
                        ? "bg-error/10 text-error"
                        : "bg-surface-container-high text-secondary"
                    }`}>
                      {includeInternal ? "Including _sys keys" : "Visible keys only"}
                    </div>
                  )}
                </div>

                <div className="mt-4 max-h-72 space-y-2 overflow-y-auto pr-1">
                  {previewKeys.length === 0 ? (
                    <div className="rounded-2xl border border-dashed border-outline-variant/20 bg-surface-container p-4 text-sm text-secondary">
                      {mode === "single"
                        ? "No key is selected."
                        : preview
                          ? "No keys match this pattern right now."
                          : "No preview yet. Enter a pattern and inspect the matches before deleting anything."}
                    </div>
                  ) : (
                    previewKeys.slice(0, 12).map((key) => (
                      <div
                        className="flex items-center justify-between gap-3 rounded-2xl border border-outline-variant/10 bg-surface-container px-4 py-3"
                        key={key}
                      >
                        <span className="truncate font-mono text-xs font-semibold text-on-surface">
                          {key}
                        </span>
                        <span className="shrink-0 rounded-full bg-error/10 px-2 py-1 text-[10px] font-bold uppercase tracking-[0.16em] text-error">
                          Delete
                        </span>
                      </div>
                    ))
                  )}
                </div>

                {previewKeys.length > 12 && (
                  <p className="mt-3 text-xs text-secondary">
                    Showing the first 12 keys. The delete applies to all
                    {" "}
                    {previewCount}
                    {" "}
                    matches.
                  </p>
                )}
              </div>
            </section>

            <section className="space-y-4">
              {mode === "pattern" && (
                <div className="rounded-[28px] border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
                  <div className="space-y-4">
                    <div className="space-y-2">
                      <label
                        className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline"
                        htmlFor="kv-delete-pattern"
                      >
                        Pattern
                      </label>
                      <input
                        className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container px-4 py-3 font-mono text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-error/40"
                        id="kv-delete-pattern"
                        onChange={(event) => onChangePattern(event.target.value)}
                        placeholder="session/*"
                        value={pattern}
                      />
                      <p className="text-xs text-secondary">
                        Use
                        {" "}
                        <span className="font-mono text-on-surface">*</span>
                        {" "}
                        for any sequence and
                        {" "}
                        <span className="font-mono text-on-surface">?</span>
                        {" "}
                        for a single character.
                      </p>
                    </div>

                    <label className="flex items-start gap-3 rounded-2xl border border-outline-variant/15 bg-surface-container px-4 py-3">
                      <input
                        checked={includeInternal}
                        className="mt-1 h-4 w-4 rounded border-outline-variant/30 text-error focus:ring-error"
                        onChange={(event) => onChangeIncludeInternal(event.target.checked)}
                        type="checkbox"
                      />
                      <span className="space-y-1">
                        <span className="block text-sm font-medium text-on-surface">
                          Include reserved
                          {" "}
                          <span className="font-mono">_sys/*</span>
                          {" "}
                          keys
                        </span>
                        <span className="block text-xs leading-5 text-secondary">
                          Leave this off unless you intentionally want to delete internal bookkeeping keys.
                        </span>
                      </span>
                    </label>

                    <button
                      className="inline-flex w-full items-center justify-center gap-2 rounded-full border border-error/25 bg-error/5 px-4 py-3 text-sm font-semibold text-error transition-colors hover:bg-error/10 disabled:cursor-not-allowed disabled:opacity-50"
                      disabled={busy || previewing || trimmedPattern.length === 0}
                      onClick={onPreview}
                      type="button"
                    >
                      <Icon className={previewing ? "animate-spin" : ""} name={previewing ? "sync" : "preview"} />
                      {previewing ? "Previewing..." : "Preview Matches"}
                    </button>
                  </div>
                </div>
              )}

              <div className="rounded-[28px] border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
                <div className="space-y-2">
                  <label
                    className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline"
                    htmlFor="kv-delete-confirmation"
                  >
                    Confirmation
                  </label>
                  <p className="text-sm text-secondary">
                    Type
                    {" "}
                    <span className="rounded bg-surface-container-high px-1.5 py-0.5 font-mono text-on-surface">
                      {confirmationTarget || (mode === "pattern" ? "the pattern above" : "the selected key")}
                    </span>
                    {" "}
                    to arm this delete.
                  </p>
                  <input
                    className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container px-4 py-3 font-mono text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-error/40"
                    id="kv-delete-confirmation"
                    onChange={(event) => onChangeConfirmationText(event.target.value)}
                    placeholder={confirmationTarget || "Type to confirm"}
                    value={confirmationText}
                  />
                </div>

                {dialogError && (
                  <div className="mt-4 rounded-2xl bg-error-container/30 p-4 text-sm text-error">
                    {dialogError}
                  </div>
                )}

                <div className="mt-5 flex flex-col gap-3">
                  <button
                    className="inline-flex items-center justify-center gap-2 rounded-full bg-error px-5 py-3 text-sm font-semibold text-white shadow-lg shadow-error/20 transition-all hover:bg-error/90 disabled:cursor-not-allowed disabled:opacity-45"
                    disabled={!canDelete}
                    onClick={onConfirm}
                    type="button"
                  >
                    <Icon className={busy ? "animate-spin" : ""} name={busy ? "sync" : "delete_forever"} />
                    {busy ? "Deleting..." : deleteLabel}
                  </button>
                  <button
                    className="inline-flex items-center justify-center gap-2 rounded-full border border-outline-variant/20 px-5 py-3 text-sm font-semibold text-secondary transition-colors hover:text-on-surface disabled:cursor-not-allowed disabled:opacity-45"
                    disabled={busy || previewing}
                    onClick={onClose}
                    type="button"
                  >
                    <Icon name="arrow_back" />
                    Cancel
                  </button>
                </div>
              </div>
            </section>
          </div>
        </div>
      </div>
    </div>
  );
}
