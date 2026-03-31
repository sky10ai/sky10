import { NavLink } from "react-router";
import { Icon } from "./Icon";
import { skyfs, skylink } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

const navItems = [
  { to: "/drives", icon: "folder_open", label: "Drives" },
  { to: "/kv", icon: "database", label: "Key-Value" },
  { to: "/devices", icon: "devices", label: "Devices" },
  { to: "/network", icon: "hub", label: "Network" },
  { to: "/settings", icon: "settings", label: "Settings" },
];

export function Sidebar() {
  const { data: health } = useRPC(() => skyfs.health());
  const { data: linkStatus } = useRPC(() => skylink.status());

  return (
    <aside className="flex flex-col fixed left-0 top-0 h-screen w-64 z-40 bg-surface-container-low dark:bg-[#1a1c1d] font-body antialiased tracking-tight">
      <div className="px-6 py-8">
        {/* Brand */}
        <div className="flex items-center gap-2 mb-10">
          <div className="w-8 h-8 rounded-lg lithic-gradient flex items-center justify-center text-white">
            <Icon name="cloud" filled className="text-lg" />
          </div>
          <div>
            <h1 className="text-xl font-bold tracking-tighter text-on-surface dark:text-surface">
              sky10
            </h1>
            <p className="text-[10px] text-secondary tracking-widest uppercase opacity-60">
              {health?.version?.split(" ")[0] ?? "..."}
            </p>
          </div>
        </div>

        {/* Navigation */}
        <nav className="space-y-1.5">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-3 px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? "text-[#007AFF] bg-surface-container-lowest dark:bg-[#2a2c2e] shadow-sm"
                    : "text-[#71717a] dark:text-[#a1a1aa] hover:bg-surface-container-high dark:hover:bg-[#2a2c2e]"
                }`
              }
            >
              {({ isActive }) => (
                <>
                  <Icon name={item.icon} filled={isActive} />
                  <span>{item.label}</span>
                </>
              )}
            </NavLink>
          ))}
        </nav>
      </div>

      {/* Bottom section */}
      <div className="mt-auto p-6 space-y-4">
        <button className="w-full py-3 px-4 lithic-gradient text-white rounded-full font-semibold text-sm shadow-lg shadow-primary/20 flex items-center justify-center gap-2 hover:opacity-90 transition-all active:scale-95">
          <Icon name="add" className="text-sm" />
          New Agent
        </button>

        <div className="pt-4 border-t border-outline-variant/10">
          <div className="flex items-center justify-between text-[11px] font-mono text-secondary">
            <div className="flex items-center gap-2">
              <Icon name="content_copy" className="text-[14px]" />
              <span>
                {linkStatus?.address
                  ? truncAddr(linkStatus.address)
                  : "..."}
              </span>
            </div>
            <div className="flex items-center gap-1.5 text-emerald-500">
              <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              <span className="font-body font-semibold">
                {health ? "Connected" : "..."}
              </span>
            </div>
          </div>
        </div>
      </div>
    </aside>
  );
}
