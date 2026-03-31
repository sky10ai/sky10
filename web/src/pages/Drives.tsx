import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";
import { skyfs } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function Drives() {
  const navigate = useNavigate();
  const { data: driveList, loading, error } = useRPC(() => skyfs.driveList());
  const { data: health } = useRPC(() => skyfs.health());

  const drives = driveList?.drives ?? [];

  return (
    <section className="p-12 max-w-7xl w-full mx-auto">
      {/* Header */}
      <div className="flex justify-between items-end mb-12">
        <div className="space-y-1">
          <h2 className="text-[3.5rem] font-bold tracking-tight text-on-surface leading-none">
            Drives
          </h2>
          <p className="text-secondary text-lg">
            {drives.length === 0
              ? "No drives configured yet."
              : `Managing ${drives.length} encrypted volume${drives.length !== 1 ? "s" : ""}.`}
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

      {error && (
        <div className="mb-8 p-4 bg-error-container/20 text-error rounded-xl text-sm">
          {error}
        </div>
      )}

      {loading && drives.length === 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {[1, 2, 3].map((i) => (
            <div
              key={i}
              className="bg-surface-container-lowest p-6 rounded-xl h-[220px] animate-pulse"
            />
          ))}
        </div>
      )}

      {/* Drive grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {drives.map((drive) => {
          const isSyncing = drive.outbox_pending > 0;
          return (
            <div
              key={drive.id}
              onClick={() => navigate(`/drives/${drive.name}`)}
              className="group bg-surface-container-lowest p-6 rounded-xl transition-all duration-300 hover:bg-surface-container-low border border-transparent hover:border-outline-variant/20 relative overflow-hidden cursor-pointer"
            >
              <div className="flex justify-between items-start mb-6">
                <div className="p-3 bg-primary/5 group-hover:bg-primary rounded-lg transition-colors">
                  <Icon
                    name="folder_open"
                    className="text-3xl text-primary group-hover:text-white transition-colors"
                  />
                </div>
                {drive.running ? (
                  isSyncing ? (
                    <div className="flex items-center gap-1.5 text-[10px] font-bold text-primary uppercase tracking-tighter bg-primary/5 px-2 py-1 rounded-full animate-pulse">
                      <Icon name="sync" className="text-[12px] animate-spin" />
                      Syncing...
                    </div>
                  ) : (
                    <div className="flex items-center gap-1.5 text-[10px] font-bold text-emerald-500 uppercase tracking-tighter bg-emerald-50 px-2 py-1 rounded-full">
                      <Icon
                        name="check_circle"
                        filled
                        className="text-[12px]"
                      />
                      Synced
                    </div>
                  )
                ) : (
                  <div className="flex items-center gap-1.5 text-[10px] font-bold text-secondary uppercase tracking-tighter bg-surface-container-high px-2 py-1 rounded-full">
                    Stopped
                  </div>
                )}
              </div>
              <div>
                <h3 className="text-xl font-bold mb-1">{drive.name}</h3>
                <div className="flex items-center gap-3 text-secondary text-sm">
                  <span className="flex items-center gap-1">
                    <Icon name="article" className="text-sm" />
                    {drive.snapshot_files} file
                    {drive.snapshot_files !== 1 ? "s" : ""}
                  </span>
                  {drive.outbox_pending > 0 && (
                    <>
                      <span className="w-1 h-1 rounded-full bg-outline-variant" />
                      <span className="text-primary font-medium">
                        {drive.outbox_pending} pending
                      </span>
                    </>
                  )}
                </div>
              </div>
              <div className="mt-8 pt-4 border-t border-outline-variant/10 flex justify-between items-center">
                <span className="text-[11px] text-outline font-medium font-mono">
                  {drive.local_path}
                </span>
                <button className="opacity-0 group-hover:opacity-100 transition-opacity text-primary">
                  <Icon name="arrow_forward" />
                </button>
              </div>
            </div>
          );
        })}

        {/* Add new drive */}
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
      {health && (
        <div className="mt-24 grid grid-cols-3 gap-12 border-t border-outline-variant/10 pt-12">
          <div>
            <p className="text-[10px] uppercase font-bold text-outline mb-1">
              Version
            </p>
            <p className="text-lg font-bold font-mono">{health.version}</p>
          </div>
          <div className="text-right">
            <p className="text-[10px] uppercase font-bold text-outline mb-1">
              Uptime
            </p>
            <p className="text-lg font-bold">{health.uptime}</p>
          </div>
          <div className="text-right">
            <p className="text-[10px] uppercase font-bold text-outline mb-1">
              Drives Running
            </p>
            <p className="text-lg font-bold">
              {health.drives_running} / {health.drives}
            </p>
          </div>
        </div>
      )}
    </section>
  );
}
