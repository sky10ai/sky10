import type { RefObject } from "react";
import { Icon } from "../Icon";

export function NewFolderForm({
  inputRef,
  onCancel,
  onCreate,
  onNameChange,
  value,
}: {
  inputRef: RefObject<HTMLInputElement | null>;
  onCancel: () => void;
  onCreate: () => void;
  onNameChange: (value: string) => void;
  value: string;
}) {
  return (
    <div className="flex items-center gap-3 rounded-2xl border border-primary/20 bg-surface-container-lowest p-4">
      <Icon className="text-2xl text-amber-400" filled name="folder" />
      <input
        className="flex-1 bg-transparent p-0 text-sm font-medium focus:ring-0"
        onChange={(event) => onNameChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") onCreate();
          if (event.key === "Escape") onCancel();
        }}
        placeholder="Folder name..."
        ref={inputRef}
        value={value}
      />
      <button
        className="rounded-full bg-primary px-4 py-1.5 text-xs font-semibold text-on-primary"
        onClick={onCreate}
        type="button"
      >
        Create
      </button>
      <button
        className="text-xs text-secondary transition-colors hover:text-on-surface"
        onClick={onCancel}
        type="button"
      >
        Cancel
      </button>
    </div>
  );
}
