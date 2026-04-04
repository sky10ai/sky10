import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { EmptyState } from "../components/EmptyState";
import {
  BrowserContextMenu,
  type BrowserContextMenuState,
} from "../components/files/BrowserContextMenu";
import { BrowserTable, type BrowserRow } from "../components/files/BrowserTable";
import { NewFolderForm } from "../components/files/NewFolderForm";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { skyfs } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

async function uploadFiles(
  files: FileList,
  driveID: string,
  currentPath: string
) {
  const results: { name: string; ok: boolean; error?: string }[] = [];
  for (const file of Array.from(files)) {
    const filePath = currentPath ? `${currentPath}/${file.name}` : file.name;
    const form = new FormData();
    form.append("file", file);
    try {
      const res = await fetch(
        `/upload?drive=${encodeURIComponent(driveID)}&path=${encodeURIComponent(filePath)}`,
        { method: "POST", body: form }
      );
      if (!res.ok) {
        const text = await res.text();
        results.push({ name: file.name, ok: false, error: text });
      } else {
        results.push({ name: file.name, ok: true });
      }
    } catch (e: unknown) {
      results.push({
        name: file.name,
        ok: false,
        error: e instanceof Error ? e.message : "upload failed",
      });
    }
  }
  return results;
}

function isDirectChild(path: string, currentPath: string) {
  if (!currentPath) {
    return !path.includes("/");
  }

  if (!path.startsWith(`${currentPath}/`)) {
    return false;
  }

  const remainder = path.slice(currentPath.length + 1);
  return remainder !== "" && !remainder.includes("/");
}

function joinPath(base: string, name: string) {
  return base ? `${base}/${name}` : name;
}

function encodePathSegments(path: string) {
  return path
    .split("/")
    .filter(Boolean)
    .map((segment) => encodeURIComponent(segment))
    .join("/");
}

export default function FileBrowser() {
  const navigate = useNavigate();
  const { name, "*": splat } = useParams();
  const driveName = name ?? "default";
  const currentPath = splat?.replace(/^\/+|\/+$/g, "") ?? "";
  const listPrefix = currentPath ? `${currentPath}/` : "";

  const { data: driveList, loading: drivesLoading } = useRPC(
    () => skyfs.driveList(),
    [],
    {
      live: STORAGE_EVENT_TYPES,
      refreshIntervalMs: 10_000,
    }
  );
  const { data, loading, error, mutate, refreshing, refetch } = useRPC(
    () => skyfs.list(listPrefix ? { prefix: listPrefix } : undefined),
    [listPrefix],
    {
      live: [...STORAGE_EVENT_TYPES, "file.changed"],
      refreshIntervalMs: 10_000,
    }
  );

  const [ctx, setCtx] = useState<BrowserContextMenuState | null>(null);
  const [newFolderName, setNewFolderName] = useState("");
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const currentDrive = (driveList?.drives ?? []).find(
    (drive) => drive.name === driveName
  );

  useEffect(() => {
    if (!showNewFolder) return;

    const timer = window.setTimeout(() => {
      inputRef.current?.focus();
    }, 50);

    return () => window.clearTimeout(timer);
  }, [showNewFolder]);

  const rows = useMemo(() => {
    if (!data) return [];

    const namespace = currentDrive?.namespace;
    const dirs = (data.dirs ?? [])
      .filter((dir) => !namespace || dir.namespace === namespace)
      .filter((dir) => isDirectChild(dir.path, currentPath))
      .map((entry) => ({ entry, kind: "dir" as const }));

    const files = data.files
      .filter((file) => !namespace || file.namespace === namespace)
      .filter((file) => isDirectChild(file.path, currentPath))
      .map((entry) => ({ entry, kind: "file" as const }));

    return [...dirs, ...files].sort((left, right) => {
      if (left.kind !== right.kind) {
        return left.kind === "dir" ? -1 : 1;
      }
      return left.entry.path.localeCompare(right.entry.path);
    });
  }, [currentDrive?.namespace, currentPath, data]);

  const breadcrumbParts = currentPath.split("/").filter(Boolean);
  const folderCount = rows.filter((row) => row.kind === "dir").length;
  const fileCount = rows.filter((row) => row.kind === "file").length;

  const onContextMenu = useCallback(
    (event: React.MouseEvent, row: BrowserRow | null) => {
      event.preventDefault();
      setCtx({ x: event.clientX, y: event.clientY, row });
    },
    []
  );

  const closeMenu = useCallback(() => setCtx(null), []);

  const navigateToPath = useCallback(
    (path: string) => {
      navigate(
        path
          ? `/drives/${encodeURIComponent(driveName)}/${encodePathSegments(path)}`
          : `/drives/${encodeURIComponent(driveName)}`
      );
    },
    [driveName, navigate]
  );

  const handleDelete = useCallback(
    async (row: BrowserRow) => {
      setCtx(null);
      setActionError(null);
      const path = row.entry.path;

      mutate((previous) => {
        if (!previous) return previous;

        return {
          ...previous,
          dirs: (previous.dirs ?? []).filter(
            (dir) => dir.path !== path && !dir.path.startsWith(`${path}/`)
          ),
          files: previous.files.filter(
            (file) => file.path !== path && !file.path.startsWith(`${path}/`)
          ),
        };
      });

      try {
        await skyfs.remove({ drive: currentDrive?.id ?? driveName, path });
        refetch({ background: true });
      } catch (e: unknown) {
        setActionError(e instanceof Error ? e.message : "Failed to delete");
        refetch();
      }
    },
    [currentDrive?.id, driveName, mutate, refetch]
  );

  const handleNewFolder = useCallback(() => {
    setCtx(null);
    setShowNewFolder(true);
    setNewFolderName("");
  }, []);

  const submitNewFolder = useCallback(async () => {
    if (!newFolderName.trim()) return;
    setActionError(null);
    const targetPath = joinPath(currentPath, newFolderName.trim());

    mutate((previous) => {
      if (!previous) return previous;
      if ((previous.dirs ?? []).some((dir) => dir.path === targetPath)) {
        return previous;
      }

      return {
        ...previous,
        dirs: [
          ...(previous.dirs ?? []),
          {
            namespace: currentDrive?.namespace ?? "",
            path: targetPath,
          },
        ],
      };
    });

    try {
      await skyfs.mkdir({
        drive: currentDrive?.id ?? driveName,
        path: targetPath,
      });
      setShowNewFolder(false);
      setNewFolderName("");
      refetch({ background: true });
    } catch (e: unknown) {
      setActionError(
        e instanceof Error ? e.message : "Failed to create folder"
      );
      refetch();
    }
  }, [
    currentDrive?.id,
    currentDrive?.namespace,
    currentPath,
    driveName,
    mutate,
    newFolderName,
    refetch,
  ]);

  const handleUpload = useCallback(
    async (event: React.ChangeEvent<HTMLInputElement>) => {
      const files = event.target.files;
      if (!files || files.length === 0) return;
      setActionError(null);
      setUploading(true);
      try {
        const results = await uploadFiles(
          files,
          currentDrive?.id ?? driveName,
          currentPath
        );
        const failed = results.filter((r) => !r.ok);
        if (failed.length > 0) {
          setActionError(
            `Failed to upload: ${failed.map((f) => `${f.name} (${f.error})`).join(", ")}`
          );
        }
        refetch({ background: true });
      } finally {
        setUploading(false);
        if (fileInputRef.current) fileInputRef.current.value = "";
      }
    },
    [currentDrive?.id, currentPath, driveName, refetch]
  );

  const handleDownload = useCallback(
    (row: BrowserRow) => {
      setCtx(null);
      if (row.kind !== "file") return;
      const driveID = currentDrive?.id ?? driveName;
      const url = `/download?drive=${encodeURIComponent(driveID)}&path=${encodeURIComponent(row.entry.path)}`;
      window.open(url, "_blank");
    },
    [currentDrive?.id, driveName]
  );

  return (
    <div className="flex flex-1 overflow-hidden" onClick={closeMenu}>
      <div
        className="relative flex-1 overflow-y-auto bg-surface"
        onContextMenu={(event) => onContextMenu(event, null)}
      >
        <div className="mx-auto max-w-7xl space-y-6 p-8">
          <PageHeader
            actions={
              <>
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
                  className="flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-white shadow-lg shadow-primary/20 transition-colors hover:bg-primary/90 disabled:opacity-50"
                  disabled={uploading}
                  onClick={() => fileInputRef.current?.click()}
                  type="button"
                >
                  <Icon className="text-lg" name="upload" />
                  {uploading ? "Uploading..." : "Upload"}
                </button>
                <input
                  ref={fileInputRef}
                  className="hidden"
                  multiple
                  onChange={handleUpload}
                  type="file"
                />
                <button
                  className="flex items-center gap-2 rounded-full bg-surface-container-high px-4 py-2 text-sm font-medium text-on-surface transition-colors hover:bg-surface-container-highest"
                  onClick={handleNewFolder}
                  type="button"
                >
                  <Icon className="text-lg" name="create_new_folder" />
                  New Folder
                </button>
              </>
            }
            description={
              currentDrive
                ? `${folderCount} folder${folderCount === 1 ? "" : "s"} and ${fileCount} file${fileCount === 1 ? "" : "s"} in ${currentPath || "the drive root"}.`
                : "Loading drive details..."
            }
            eyebrow={currentDrive?.namespace ?? "Drive Browser"}
            title={driveName}
          />

          <div className="flex flex-wrap items-center gap-2 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-4 py-3 text-sm text-secondary shadow-sm">
            <Icon className="text-sm text-primary" name="folder" />
            <button
              className="font-medium text-on-surface transition-colors hover:text-primary"
              onClick={() => navigateToPath("")}
              type="button"
            >
              /
            </button>
            {breadcrumbParts.map((part, index) => {
              const path = breadcrumbParts.slice(0, index + 1).join("/");
              return (
                <button
                  className="flex items-center gap-2 transition-colors hover:text-primary"
                  key={path}
                  onClick={() => navigateToPath(path)}
                  type="button"
                >
                  <span className="text-outline">/</span>
                  <span className="font-medium text-on-surface">{part}</span>
                </button>
              );
            })}
            {currentDrive && (
              <span className="ml-auto truncate font-mono text-xs text-outline">
                {currentDrive.local_path}
              </span>
            )}
          </div>

          {(error || actionError) && (
            <div className="flex items-center justify-between rounded-xl bg-error-container/20 p-4 text-sm text-error">
              <span>{actionError ?? error}</span>
              {actionError && (
                <button
                  className="text-xs text-error transition-colors hover:underline"
                  onClick={() => setActionError(null)}
                  type="button"
                >
                  dismiss
                </button>
              )}
            </div>
          )}

          {showNewFolder && (
            <NewFolderForm
              inputRef={inputRef}
              onCancel={() => setShowNewFolder(false)}
              onCreate={submitNewFolder}
              onNameChange={setNewFolderName}
              value={newFolderName}
            />
          )}

          {drivesLoading && !currentDrive && (
            <div className="space-y-2">
              {[1, 2, 3].map((index) => (
                <div
                  className="h-16 animate-pulse rounded-xl bg-surface-container-low"
                  key={index}
                />
              ))}
            </div>
          )}

          {!drivesLoading && !currentDrive && (
            <EmptyState
              description="This drive no longer exists or its metadata has not loaded yet."
              icon="folder_off"
              title="Drive not found"
            />
          )}

          {loading && rows.length === 0 && currentDrive && (
            <div className="space-y-2">
              {[1, 2, 3].map((index) => (
                <div
                  className="h-16 animate-pulse rounded-xl bg-surface-container-low"
                  key={index}
                />
              ))}
            </div>
          )}

          {!loading && rows.length === 0 && !error && currentDrive && (
            <EmptyState
              description="Create a folder here or drop files into the local sync directory and they’ll appear as the daemon picks them up."
              icon="folder_open"
              title="This folder is empty"
            />
          )}

          {rows.length > 0 && currentDrive && (
            <BrowserTable
              entries={rows}
              onContextMenu={onContextMenu}
              onOpenDirectory={navigateToPath}
            />
          )}
        </div>
      </div>

      {ctx && (
        <BrowserContextMenu
          onDelete={handleDelete}
          onDownload={handleDownload}
          onNewFolder={handleNewFolder}
          state={ctx}
        />
      )}
    </div>
  );
}
