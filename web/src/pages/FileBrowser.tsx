import { Icon } from "../components/Icon";

const files = [
  {
    name: "canyon_render_final.png",
    type: "PNG Image",
    size: "14.2 MB",
    modified: "2m ago",
    status: "synced" as const,
    device: "MacBook Pro 16",
    deviceIcon: "laptop_mac",
    thumb: true,
  },
  {
    name: "Project_Skyline_Spec.pdf",
    type: "PDF Document",
    size: "2.8 MB",
    modified: "1h ago",
    status: "pending" as const,
    device: "iPhone 15 Pro",
    deviceIcon: "smartphone",
    icon: "picture_as_pdf",
    iconColor: "text-red-500",
    iconBg: "bg-red-50",
  },
  {
    name: "source_assets_v2.zip",
    type: "ZIP Archive",
    size: "841 MB",
    modified: "Yesterday",
    status: "synced" as const,
    device: "Workstation-X",
    deviceIcon: "desktop_windows",
    icon: "folder_zip",
    iconColor: "text-on-surface-variant",
    iconBg: "bg-surface-container-highest",
  },
  {
    name: "workspace_inspo.jpg",
    type: "JPEG Image",
    size: "4.1 MB",
    modified: "3 days ago",
    status: "remote" as const,
    device: "MacBook Air",
    deviceIcon: "laptop_mac",
    thumb: true,
  },
];

const statusConfig = {
  synced: {
    icon: "check_circle",
    color: "text-primary",
    label: "SYNCED",
  },
  pending: {
    icon: "sync",
    color: "text-amber-500",
    label: "PENDING",
  },
  remote: {
    icon: "cloud_download",
    color: "text-primary",
    label: "REMOTE",
  },
};

export default function FileBrowser() {
  return (
    <div className="flex flex-1 overflow-hidden">
      {/* Folder tree sidebar */}
      <div className="w-72 bg-surface border-r border-surface-container flex flex-col">
        <div className="p-6">
          <h3 className="text-xs font-bold uppercase tracking-widest text-on-surface-variant mb-4">
            Directories
          </h3>
          <div className="space-y-1">
            <div>
              <button className="w-full flex items-center justify-between px-2 py-1.5 rounded-lg hover:bg-surface-container-low transition-colors group">
                <div className="flex items-center gap-2">
                  <Icon
                    name="keyboard_arrow_down"
                    className="text-lg text-primary"
                  />
                  <Icon
                    name="folder"
                    filled
                    className="text-lg text-amber-400"
                  />
                  <span className="text-sm font-medium">Documents</span>
                </div>
              </button>
              <div className="ml-6 mt-1 space-y-1 border-l border-surface-container-high pl-4">
                <button className="w-full flex items-center gap-2 px-2 py-1.5 rounded-lg bg-surface-container-low text-primary font-medium">
                  <Icon name="folder_shared" filled className="text-lg" />
                  <span className="text-sm">Personal</span>
                </button>
                <button className="w-full flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-surface-container-low text-on-surface-variant">
                  <Icon name="folder" filled className="text-lg" />
                  <span className="text-sm">Work</span>
                </button>
                <button className="w-full flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-surface-container-low text-on-surface-variant">
                  <Icon name="folder" filled className="text-lg" />
                  <span className="text-sm">Taxes</span>
                </button>
              </div>
            </div>
            <button className="w-full flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-surface-container-low transition-colors text-on-surface-variant">
              <Icon name="keyboard_arrow_right" className="text-lg" />
              <Icon name="folder" filled className="text-lg text-blue-400" />
              <span className="text-sm font-medium">Projects</span>
            </button>
            <button className="w-full flex items-center gap-2 px-2 py-1.5 rounded-lg hover:bg-surface-container-low transition-colors text-on-surface-variant">
              <Icon name="keyboard_arrow_right" className="text-lg" />
              <Icon
                name="folder"
                filled
                className="text-lg text-emerald-400"
              />
              <span className="text-sm font-medium">Archives</span>
            </button>
          </div>
        </div>
        {/* Storage usage */}
        <div className="mt-auto p-6">
          <div className="bg-surface-container-low rounded-xl p-4">
            <div className="flex justify-between text-[11px] font-bold mb-2 uppercase tracking-tighter">
              <span className="text-on-surface-variant">Storage</span>
              <span className="text-primary">82%</span>
            </div>
            <div className="h-1.5 w-full bg-surface-container-highest rounded-full overflow-hidden">
              <div
                className="h-full bg-primary-container"
                style={{ width: "82%" }}
              />
            </div>
            <p className="text-[10px] text-on-surface-variant mt-2">
              1.8 TB of 2.2 TB used
            </p>
          </div>
        </div>
      </div>

      {/* Main file area */}
      <div className="flex-1 flex flex-col bg-surface overflow-y-auto relative">
        <div className="p-8">
          <div className="flex items-end justify-between mb-8">
            <div>
              <h2 className="text-3xl font-bold tracking-tight text-on-surface">
                Personal
              </h2>
              <p className="text-sm text-on-surface-variant">
                Managing 48 encrypted objects in this vault.
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

          {/* File table */}
          <div className="w-full">
            <div className="grid grid-cols-[1fr_100px_150px_100px_150px_40px] px-4 py-3 text-[11px] font-bold uppercase tracking-wider text-on-surface-variant border-b border-surface-container-high mb-2">
              <div>Name</div>
              <div>Size</div>
              <div>Modified</div>
              <div>Status</div>
              <div>Device</div>
              <div />
            </div>
            {files.map((file) => {
              const st = statusConfig[file.status];
              return (
                <div
                  key={file.name}
                  className="grid grid-cols-[1fr_100px_150px_100px_150px_40px] items-center px-4 py-4 hover:bg-surface-container-low rounded-xl transition-all group/item cursor-pointer"
                >
                  <div className="flex items-center gap-4">
                    <div
                      className={`w-12 h-12 rounded-lg flex-shrink-0 flex items-center justify-center ${file.iconBg ?? "bg-surface-container-highest overflow-hidden"}`}
                    >
                      {file.thumb ? (
                        <div className="w-full h-full bg-gradient-to-br from-primary/20 to-tertiary/20 rounded-lg" />
                      ) : (
                        <Icon
                          name={file.icon ?? "description"}
                          filled
                          className={`text-2xl ${file.iconColor ?? ""}`}
                        />
                      )}
                    </div>
                    <div>
                      <p className="text-sm font-semibold text-on-surface">
                        {file.name}
                      </p>
                      <p className="text-[10px] text-on-surface-variant">
                        {file.type}
                      </p>
                    </div>
                  </div>
                  <div className="text-sm text-on-surface-variant font-mono">
                    {file.size}
                  </div>
                  <div className="text-sm text-on-surface-variant">
                    {file.modified}
                  </div>
                  <div className="flex items-center">
                    <Icon
                      name={st.icon}
                      filled
                      className={`text-xl ${st.color}`}
                    />
                    <span
                      className={`text-[10px] ml-1 font-bold ${st.color}`}
                    >
                      {st.label}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <Icon
                      name={file.deviceIcon}
                      className="text-sm text-on-surface-variant"
                    />
                    <span className="text-xs font-medium">{file.device}</span>
                  </div>
                  <div>
                    <button className="p-1 rounded-full hover:bg-surface-container-highest opacity-0 group-hover/item:opacity-100 transition-opacity">
                      <Icon name="more_vert" />
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        {/* Upload progress toast */}
        <div className="absolute bottom-8 right-8 w-80 bg-surface-container-lowest shadow-2xl rounded-2xl border border-surface-container-high p-4 z-50">
          <div className="flex items-center justify-between mb-3">
            <h4 className="text-sm font-bold text-on-surface">
              Uploading 2 files
            </h4>
            <button className="p-1 rounded-full hover:bg-surface-container-high">
              <Icon name="close" className="text-lg" />
            </button>
          </div>
          <div className="space-y-4">
            <div>
              <div className="flex justify-between items-center mb-1">
                <span className="text-xs font-medium truncate w-40 text-on-surface">
                  backup_database_weekly.sql
                </span>
                <span className="text-[10px] font-bold text-primary">64%</span>
              </div>
              <div className="h-1 bg-surface-container-highest rounded-full overflow-hidden">
                <div
                  className="h-full bg-primary-container"
                  style={{ width: "64%" }}
                />
              </div>
            </div>
            <div>
              <div className="flex justify-between items-center mb-1">
                <span className="text-xs font-medium truncate w-40 text-on-surface">
                  high_res_avatar.png
                </span>
                <span className="text-[10px] font-bold text-secondary">
                  QUEUED
                </span>
              </div>
              <div className="h-1 bg-surface-container-highest rounded-full" />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
