import { useParams } from "react-router";
import { Icon } from "../components/Icon";
import { skyfs } from "../lib/rpc";
import { useRPC, formatBytes, timeAgo } from "../lib/useRPC";

export default function FileBrowser() {
  const { name } = useParams();
  const driveName = name ?? "default";

  const { data, loading, error } = useRPC(
    () => skyfs.list({ drive: driveName }),
    [driveName]
  );

  const files = data?.files ?? [];

  return (
    <div className="flex flex-1 overflow-hidden">
      {/* Main file area */}
      <div className="flex-1 flex flex-col bg-surface overflow-y-auto relative">
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
              <button className="p-2 rounded-lg hover:bg-surface-container-high transition-colors">
                <Icon name="grid_view" />
              </button>
              <button className="p-2 rounded-lg bg-surface-container-high transition-colors">
                <Icon name="list" />
              </button>
            </div>
          </div>

          {error && (
            <div className="mb-4 p-4 bg-error-container/20 text-error rounded-xl text-sm">
              {error}
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
                Drop files into{" "}
                <code className="font-mono bg-surface-container-high px-1 rounded">
                  {driveName}
                </code>{" "}
                to start syncing.
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
                const ext = file.path.split(".").pop()?.toLowerCase() ?? "";
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
    </div>
  );
}
