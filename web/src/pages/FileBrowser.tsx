import { useCallback, useRef, useState } from "react";
import { useParams } from "react-router";
import { Icon } from "../components/Icon";
import { skyfs } from "../lib/rpc";
import { useRPC, formatBytes, timeAgo } from "../lib/useRPC";

interface ContextMenu {
  x: number;
  y: number;
  filePath: string | null; // null = background right-click
}

export default function FileBrowser() {
  const { name } = useParams();
  const driveName = name ?? "default";

  const { data, loading, error, refetch } = useRPC(
    () => skyfs.list({ drive: driveName }),
    [driveName]
  );

  const [ctx, setCtx] = useState<ContextMenu | null>(null);
  const [newFolderName, setNewFolderName] = useState("");
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const files = data?.files ?? [];

  const onContextMenu = useCallback(
    (e: React.MouseEvent, filePath: string | null) => {
      e.preventDefault();
      setCtx({ x: e.clientX, y: e.clientY, filePath });
    },
    []
  );

  const closeMenu = useCallback(() => setCtx(null), []);

  const handleDelete = useCallback(
    async (path: string) => {
      setCtx(null);
      setActionError(null);
      try {
        await skyfs.remove({ drive: driveName, path });
        refetch();
      } catch (e: unknown) {
        setActionError(
          e instanceof Error ? e.message : "Failed to delete"
        );
      }
    },
    [driveName, refetch]
  );

  const handleNewFolder = useCallback(() => {
    setCtx(null);
    setShowNewFolder(true);
    setNewFolderName("");
    setTimeout(() => inputRef.current?.focus(), 50);
  }, []);

  const submitNewFolder = useCallback(async () => {
    if (!newFolderName.trim()) return;
    setActionError(null);
    try {
      await skyfs.mkdir({ drive: driveName, path: newFolderName.trim() });
      setShowNewFolder(false);
      setNewFolderName("");
      refetch();
    } catch (e: unknown) {
      setActionError(
        e instanceof Error ? e.message : "Failed to create folder"
      );
    }
  }, [driveName, newFolderName, refetch]);

  return (
    // Close context menu on click anywhere
    <div
      className="flex flex-1 overflow-hidden"
      onClick={closeMenu}
    >
      <div
        className="flex-1 flex flex-col bg-surface overflow-y-auto relative"
        onContextMenu={(e) => onContextMenu(e, null)}
      >
        <div className="p-8">
          <div className="flex items-end justify-between mb-8">
            <div>
              <h2 className="text-3xl font-bold tracking-tight text-on-surface">
                {driveName}
              </h2>
              <p className="text-sm text-on-surface-variant">
                {files.length} encrypted object
                {files.length !== 1 ? "s" : ""} in this vault.
              </p>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={handleNewFolder}
                className="flex items-center gap-2 px-4 py-2 bg-surface-container-high rounded-lg text-sm font-medium hover:bg-surface-container-highest transition-colors"
              >
                <Icon name="create_new_folder" className="text-lg" />
                New Folder
              </button>
            </div>
          </div>

          {(error || actionError) && (
            <div className="mb-4 p-4 bg-error-container/20 text-error rounded-xl text-sm flex justify-between items-center">
              <span>{actionError ?? error}</span>
              {actionError && (
                <button
                  onClick={() => setActionError(null)}
                  className="text-error hover:underline text-xs"
                >
                  dismiss
                </button>
              )}
            </div>
          )}

          {/* New folder inline input */}
          {showNewFolder && (
            <div className="mb-4 flex items-center gap-3 bg-surface-container-lowest p-4 rounded-xl border border-primary/20">
              <Icon
                name="folder"
                filled
                className="text-2xl text-amber-400"
              />
              <input
                ref={inputRef}
                value={newFolderName}
                onChange={(e) => setNewFolderName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") submitNewFolder();
                  if (e.key === "Escape") setShowNewFolder(false);
                }}
                className="flex-1 bg-transparent border-none focus:ring-0 text-sm font-medium p-0"
                placeholder="Folder name..."
              />
              <button
                onClick={submitNewFolder}
                className="px-4 py-1.5 bg-primary text-white rounded-full text-xs font-semibold"
              >
                Create
              </button>
              <button
                onClick={() => setShowNewFolder(false)}
                className="text-secondary text-xs hover:text-on-surface"
              >
                Cancel
              </button>
            </div>
          )}

          {loading && files.length === 0 && (
            <div className="space-y-2">
              {[1, 2, 3].map((i) => (
                <div
                  key={i}
                  className="h-16 bg-surface-container-low rounded-xl animate-pulse"
                />
              ))}
            </div>
          )}

          {!loading && files.length === 0 && !error && (
            <div className="flex flex-col items-center justify-center py-24 text-center">
              <Icon
                name="folder_open"
                className="text-6xl text-outline mb-4"
              />
              <h3 className="text-lg font-bold text-secondary mb-1">
                No files yet
              </h3>
              <p className="text-sm text-outline">
                Right-click to create a folder, or drop files into the local
                sync directory.
              </p>
            </div>
          )}

          {/* File table */}
          {files.length > 0 && (
            <div className="w-full">
              <div className="grid grid-cols-[1fr_100px_150px_80px_100px] px-4 py-3 text-[11px] font-bold uppercase tracking-wider text-on-surface-variant border-b border-surface-container-high mb-2">
                <div>Name</div>
                <div>Size</div>
                <div>Modified</div>
                <div>Chunks</div>
                <div>Checksum</div>
              </div>
              {files.map((file) => {
                const ext =
                  file.path.split(".").pop()?.toLowerCase() ?? "";
                const iconMap: Record<string, [string, string]> = {
                  txt: ["description", "text-blue-500"],
                  md: ["description", "text-blue-500"],
                  pdf: ["picture_as_pdf", "text-red-500"],
                  png: ["image", "text-sky-500"],
                  jpg: ["image", "text-sky-500"],
                  jpeg: ["image", "text-sky-500"],
                  zip: ["folder_zip", "text-amber-500"],
                  json: ["data_object", "text-emerald-500"],
                };
                const [icon, iconColor] = iconMap[ext] ?? [
                  "draft",
                  "text-on-surface-variant",
                ];

                return (
                  <div
                    key={file.path}
                    onContextMenu={(e) => onContextMenu(e, file.path)}
                    className="grid grid-cols-[1fr_100px_150px_80px_100px] items-center px-4 py-4 hover:bg-surface-container-low rounded-xl transition-all group/item cursor-pointer"
                  >
                    <div className="flex items-center gap-4">
                      <div className="w-10 h-10 rounded-lg bg-surface-container-high flex items-center justify-center flex-shrink-0">
                        <Icon
                          name={icon}
                          filled
                          className={`text-xl ${iconColor}`}
                        />
                      </div>
                      <div>
                        <p className="text-sm font-semibold text-on-surface">
                          {file.path}
                        </p>
                        <p className="text-[10px] text-on-surface-variant font-mono">
                          {file.namespace}
                        </p>
                      </div>
                    </div>
                    <div className="text-sm text-on-surface-variant font-mono">
                      {formatBytes(file.size)}
                    </div>
                    <div className="text-sm text-on-surface-variant">
                      {timeAgo(file.modified)}
                    </div>
                    <div className="text-sm text-on-surface-variant">
                      {file.chunks}
                    </div>
                    <div className="text-xs font-mono text-outline truncate">
                      {file.checksum.slice(0, 8)}...
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {/* Context menu */}
      {ctx && (
        <div
          className="fixed z-[200] bg-surface-container-lowest rounded-xl shadow-2xl border border-outline-variant/20 py-1 min-w-[180px]"
          style={{ left: ctx.x, top: ctx.y }}
          onClick={(e) => e.stopPropagation()}
        >
          <button
            onClick={handleNewFolder}
            className="w-full flex items-center gap-3 px-4 py-2.5 text-sm text-on-surface hover:bg-primary/5 transition-colors"
          >
            <Icon name="create_new_folder" className="text-lg text-primary" />
            New Folder
          </button>
          {ctx.filePath && (
            <>
              <div className="border-t border-outline-variant/10 my-1" />
              <button
                onClick={() => handleDelete(ctx.filePath!)}
                className="w-full flex items-center gap-3 px-4 py-2.5 text-sm text-error hover:bg-error-container/10 transition-colors"
              >
                <Icon name="delete" className="text-lg" />
                Delete
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
