import { Icon } from "../Icon";
import type { BrowserRow } from "./BrowserTable";

export interface BrowserContextMenuState {
  row: BrowserRow | null;
  x: number;
  y: number;
}

export function BrowserContextMenu({
  onDelete,
  onNewFolder,
  state,
}: {
  onDelete: (row: BrowserRow) => void;
  onNewFolder: () => void;
  state: BrowserContextMenuState;
}) {
  const deleteLabel =
    state.row?.kind === "dir" ? "Delete Folder" : "Delete File";

  return (
    <div
      className="fixed z-[200] min-w-[190px] rounded-xl border border-outline-variant/20 bg-surface-container-lowest py-1 shadow-2xl"
      onClick={(event) => event.stopPropagation()}
      style={{ left: state.x, top: state.y }}
    >
      <button
        className="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-on-surface transition-colors hover:bg-primary/5"
        onClick={onNewFolder}
        type="button"
      >
        <Icon className="text-lg text-primary" name="create_new_folder" />
        New Folder
      </button>
      {state.row && (
        <>
          <div className="my-1 border-t border-outline-variant/10" />
          <button
            className="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-error transition-colors hover:bg-error-container/20"
            onClick={() => {
              if (state.row) {
                onDelete(state.row);
              }
            }}
            type="button"
          >
            <Icon className="text-lg" name="delete" />
            {deleteLabel}
          </button>
        </>
      )}
    </div>
  );
}
