import type { MouseEvent } from "react";
import type { DirectoryEntry, FileEntry } from "../../lib/rpc";
import { formatBytes } from "../../lib/useRPC";
import { Icon } from "../Icon";
import { RelativeTime } from "../RelativeTime";

export interface BrowserFileRow {
  entry: FileEntry;
  kind: "file";
}

export interface BrowserDirectoryRow {
  entry: DirectoryEntry;
  kind: "dir";
}

export type BrowserRow = BrowserDirectoryRow | BrowserFileRow;

function getEntryName(path: string) {
  const segments = path.split("/").filter(Boolean);
  return segments.at(-1) ?? path;
}

function getFileIcon(path: string): [string, string] {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  const iconMap: Record<string, [string, string]> = {
    jpg: ["image", "text-sky-500"],
    jpeg: ["image", "text-sky-500"],
    json: ["data_object", "text-emerald-500"],
    md: ["description", "text-blue-500"],
    pdf: ["picture_as_pdf", "text-red-500"],
    png: ["image", "text-sky-500"],
    txt: ["description", "text-blue-500"],
    zip: ["folder_zip", "text-amber-500"],
  };

  return iconMap[ext] ?? ["draft", "text-on-surface-variant"];
}

export function BrowserTable({
  entries,
  onContextMenu,
  onOpenDirectory,
}: {
  entries: BrowserRow[];
  onContextMenu: (event: MouseEvent<HTMLDivElement>, row: BrowserRow) => void;
  onOpenDirectory: (path: string) => void;
}) {
  return (
    <div className="w-full rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-3 shadow-sm">
      <div className="grid grid-cols-[minmax(0,1.6fr)_120px_140px_80px_110px] px-4 py-3 text-[11px] font-bold uppercase tracking-[0.18em] text-on-surface-variant">
        <div>Name</div>
        <div>Size</div>
        <div>Modified</div>
        <div>Chunks</div>
        <div>Checksum</div>
      </div>

      <div className="space-y-1">
        {entries.map((row) => {
          const isDirectory = row.kind === "dir";
          const [icon, iconColor] = isDirectory
            ? ["folder", "text-amber-500"]
            : getFileIcon(row.entry.path);

          return (
            <div
              key={`${row.kind}:${row.entry.path}`}
              className={`grid grid-cols-[minmax(0,1.6fr)_120px_140px_80px_110px] items-center rounded-xl px-4 py-4 transition-colors ${
                isDirectory
                  ? "cursor-pointer hover:bg-primary/5"
                  : "hover:bg-surface-container-low"
              }`}
              onClick={() => {
                if (isDirectory) {
                  onOpenDirectory(row.entry.path);
                }
              }}
              onContextMenu={(event) => onContextMenu(event, row)}
            >
              <div className="flex min-w-0 items-center gap-4">
                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface-container-high">
                  <Icon
                    className={`text-xl ${iconColor}`}
                    filled
                    name={icon}
                  />
                </div>
                <div className="min-w-0">
                  <p className="truncate text-sm font-semibold text-on-surface">
                    {getEntryName(row.entry.path)}
                  </p>
                  <p className="truncate font-mono text-[10px] text-on-surface-variant">
                    {row.entry.path}
                  </p>
                </div>
              </div>

              <div className="text-sm font-mono text-on-surface-variant">
                {isDirectory ? "--" : formatBytes(row.entry.size)}
              </div>
              <div className="text-sm text-on-surface-variant">
                {isDirectory ? "--" : <RelativeTime value={row.entry.modified} />}
              </div>
              <div className="text-sm text-on-surface-variant">
                {isDirectory ? "--" : row.entry.chunks}
              </div>
              <div className="truncate font-mono text-xs text-outline">
                {isDirectory
                  ? row.entry.namespace
                  : `${row.entry.checksum.slice(0, 8)}...`}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
