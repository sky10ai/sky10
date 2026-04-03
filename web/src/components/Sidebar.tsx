import { NavLink, useLocation } from "react-router";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { Icon } from "./Icon";
import { skyfs, skylink } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";
import { StatusBadge } from "./StatusBadge";

const navItems = [
  { to: "/kv", icon: "database", label: "Key-Value", matchPrefixes: ["/kv"] },
  { to: "/devices", icon: "devices", label: "Devices", matchPrefixes: ["/devices"] },
  { to: "/network", icon: "hub", label: "Network", matchPrefixes: ["/network"] },
  {
    to: "/drives",
    icon: "folder_open",
    label: "Drives",
    matchPrefixes: ["/drives", "/bucket"],
  },
  { to: "/settings", icon: "settings", label: "Settings", matchPrefixes: ["/settings"] },
];

export function Sidebar() {
  const location = useLocation();
  const { data: health, refreshing } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });
  const pending = health?.outbox_pending ?? 0;
  const syncing = pending > 0;

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
          {navItems.map((item) => {
            const isActive = item.matchPrefixes.some(
              (prefix) =>
                location.pathname === prefix ||
                location.pathname.startsWith(`${prefix}/`)
            );

            return (
              <NavLink
                key={item.to}
                to={item.to}
                className={() =>
                  `flex items-center gap-3 px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
                    isActive
                      ? "text-[#007AFF] bg-surface-container-lowest dark:bg-[#2a2c2e] shadow-sm"
                      : "text-[#71717a] dark:text-[#a1a1aa] hover:bg-surface-container-high dark:hover:bg-[#2a2c2e]"
                  }`
                }
              >
                <>
                  <Icon name={item.icon} filled={isActive} />
                  <span>{item.label}</span>
                </>
              </NavLink>
            );
          })}
        </nav>
      </div>

      {/* Bottom section */}
      <div className="mt-auto p-6 space-y-4">
        <div className="rounded-xl border border-outline-variant/10 bg-surface-container-lowest px-4 py-3">
          <div className="mb-3 flex items-center justify-between">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Node Status
            </p>
            {syncing ? (
              <StatusBadge icon="sync" pulse tone="processing">
                Syncing
              </StatusBadge>
            ) : (
              <StatusBadge pulse tone="live">
                Ready
              </StatusBadge>
            )}
          </div>
          <div className="grid grid-cols-2 gap-3 text-xs">
            <div>
              <p className="text-outline">Drives</p>
              <p className="mt-1 font-semibold text-on-surface">
                {health?.drives_running ?? 0}/{health?.drives ?? 0}
              </p>
            </div>
            <div>
              <p className="text-outline">Queue</p>
              <p className="mt-1 font-semibold text-on-surface">
                {pending}
                {refreshing ? " ..." : ""}
              </p>
            </div>
          </div>
        </div>
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
            <span className="font-body font-semibold text-emerald-500">
              {health ? "Connected" : "..."}
            </span>
          </div>
        </div>
      </div>
    </aside>
  );
}
