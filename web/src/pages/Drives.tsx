import { Icon } from "../components/Icon";

interface DriveCard {
  name: string;
  icon: string;
  iconBg: string;
  iconColor: string;
  fileCount: string;
  fileIcon: string;
  size: string;
  status: "synced" | "syncing";
  syncProgress?: number;
  pendingFiles?: number;
  lastActive: string;
}

const drives: DriveCard[] = [
  {
    name: "Documents",
    icon: "description",
    iconBg: "bg-primary/5 group-hover:bg-primary",
    iconColor: "text-primary group-hover:text-white",
    fileCount: "1,248 files",
    fileIcon: "article",
    size: "4.2 GB",
    status: "synced",
    lastActive: "Last active 2m ago",
  },
  {
    name: "Photos",
    icon: "image",
    iconBg: "bg-tertiary/5 group-hover:bg-tertiary",
    iconColor: "text-tertiary group-hover:text-white",
    fileCount: "8,492 files",
    fileIcon: "photo_library",
    size: "128.5 GB",
    status: "syncing",
    syncProgress: 64,
    pendingFiles: 12,
    lastActive: "Updating now",
  },
  {
    name: "Projects",
    icon: "architecture",
    iconBg: "bg-on-surface/5 group-hover:bg-on-surface",
    iconColor: "text-on-surface group-hover:text-white",
    fileCount: "42 folders",
    fileIcon: "inventory_2",
    size: "1.1 TB",
    status: "synced",
    lastActive: "Last active 4h ago",
  },
];

export default function Drives() {
  return (
    <section className="p-12 max-w-7xl w-full mx-auto">
      {/* Header */}
      <div className="flex justify-between items-end mb-12">
        <div className="space-y-1">
          <h2 className="text-[3.5rem] font-bold tracking-tight text-on-surface leading-none">
            Drives
          </h2>
          <p className="text-secondary text-lg">
            Securely managing 14.2 TB across 3 encrypted volumes.
          </p>
        </div>
        <div className="flex items-center gap-4">
          <div className="text-right hidden sm:block">
            <p className="text-[10px] uppercase tracking-widest font-bold text-outline">
              Keyboard Shortcut
            </p>
            <p className="text-sm font-mono text-secondary">
              <kbd className="bg-surface-container-high px-1.5 py-0.5 rounded">
                &#x2318;
              </kbd>{" "}
              <kbd className="bg-surface-container-high px-1.5 py-0.5 rounded">
                N
              </kbd>
            </p>
          </div>
          <button className="lithic-gradient text-white px-8 py-4 rounded-full font-bold flex items-center gap-3 shadow-xl shadow-primary/30 transition-transform active:scale-95">
            <Icon name="add_circle" />
            Create Drive
          </button>
        </div>
      </div>

      {/* Drive grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {drives.map((drive) => (
          <div
            key={drive.name}
            className="group bg-surface-container-lowest p-6 rounded-xl transition-all duration-300 hover:bg-surface-container-low border border-transparent hover:border-outline-variant/20 relative overflow-hidden cursor-pointer"
          >
            <div className="flex justify-between items-start mb-6">
              <div
                className={`p-3 rounded-lg transition-colors ${drive.iconBg}`}
              >
                <Icon
                  name={drive.icon}
                  className={`text-3xl ${drive.iconColor} transition-colors`}
                />
              </div>
              {drive.status === "synced" ? (
                <div className="flex items-center gap-1.5 text-[10px] font-bold text-emerald-500 uppercase tracking-tighter bg-emerald-50 px-2 py-1 rounded-full">
                  <Icon name="check_circle" filled className="text-[12px]" />
                  Synced
                </div>
              ) : (
                <div className="flex items-center gap-1.5 text-[10px] font-bold text-primary uppercase tracking-tighter bg-primary/5 px-2 py-1 rounded-full animate-pulse">
                  <Icon name="sync" className="text-[12px] animate-spin" />
                  Syncing...
                </div>
              )}
            </div>
            <div>
              <h3 className="text-xl font-bold mb-1">{drive.name}</h3>
              <div className="flex items-center gap-3 text-secondary text-sm">
                <span className="flex items-center gap-1">
                  <Icon name={drive.fileIcon} className="text-sm" />{" "}
                  {drive.fileCount}
                </span>
                <span className="w-1 h-1 rounded-full bg-outline-variant" />
                <span className="font-mono">{drive.size}</span>
              </div>
              {drive.status === "syncing" &&
                drive.syncProgress !== undefined && (
                  <div className="mt-4 space-y-1.5">
                    <div className="flex justify-between text-[10px] font-bold text-outline uppercase tracking-wider">
                      <span>{drive.pendingFiles} files pending</span>
                      <span>{drive.syncProgress}%</span>
                    </div>
                    <div className="w-full bg-surface-container-high h-1 rounded-full overflow-hidden">
                      <div
                        className="bg-primary h-full rounded-full transition-all"
                        style={{ width: `${drive.syncProgress}%` }}
                      />
                    </div>
                  </div>
                )}
            </div>
            <div className="mt-8 pt-4 border-t border-outline-variant/10 flex justify-between items-center">
              <span className="text-[11px] text-outline font-medium">
                {drive.lastActive}
              </span>
              <button className="opacity-0 group-hover:opacity-100 transition-opacity text-primary">
                <Icon name="arrow_forward" />
              </button>
            </div>
          </div>
        ))}

        {/* Add new drive ghost card */}
        <div className="group border-2 border-dashed border-outline-variant/30 p-6 rounded-xl flex flex-col items-center justify-center text-center space-y-4 hover:border-primary/50 hover:bg-primary/[0.02] transition-all cursor-pointer min-h-[220px]">
          <div className="w-12 h-12 rounded-full bg-surface-container-high flex items-center justify-center text-outline group-hover:bg-primary/10 group-hover:text-primary transition-colors">
            <Icon name="add" className="text-2xl" />
          </div>
          <div className="space-y-1">
            <h4 className="font-bold text-secondary group-hover:text-primary transition-colors">
              Mount New Drive
            </h4>
            <p className="text-xs text-outline leading-relaxed px-4">
              Create a virtual encrypted volume to store sensitive assets.
            </p>
          </div>
        </div>
      </div>

      {/* Footer stats */}
      <div className="mt-24 grid grid-cols-4 gap-12 border-t border-outline-variant/10 pt-12">
        <div className="col-span-2">
          <h4 className="text-sm font-bold uppercase tracking-widest text-outline mb-4">
            Total Space Allocation
          </h4>
          <div className="w-full bg-surface-container-high h-2 rounded-full flex overflow-hidden">
            <div className="bg-primary h-full" style={{ width: "30%" }} />
            <div className="bg-tertiary h-full" style={{ width: "45%" }} />
            <div className="bg-on-surface h-full" style={{ width: "15%" }} />
          </div>
          <div className="flex gap-6 mt-4">
            <div className="flex items-center gap-2 text-xs font-medium">
              <span className="w-2 h-2 rounded-full bg-primary" /> Documents
              (4.2 GB)
            </div>
            <div className="flex items-center gap-2 text-xs font-medium">
              <span className="w-2 h-2 rounded-full bg-tertiary" /> Photos
              (128.5 GB)
            </div>
            <div className="flex items-center gap-2 text-xs font-medium">
              <span className="w-2 h-2 rounded-full bg-on-surface" /> Projects
              (1.1 TB)
            </div>
          </div>
        </div>
        <div className="text-right">
          <p className="text-[10px] uppercase font-bold text-outline mb-1">
            Global Health
          </p>
          <p className="text-2xl font-bold text-emerald-500">OPTIMAL</p>
        </div>
        <div className="text-right">
          <p className="text-[10px] uppercase font-bold text-outline mb-1">
            Nodes Active
          </p>
          <p className="text-2xl font-bold">12 / 14</p>
        </div>
      </div>
    </section>
  );
}
