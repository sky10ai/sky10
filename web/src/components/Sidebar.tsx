import { useState } from "react";
import { NavLink, useLocation } from "react-router";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { Icon } from "./Icon";
import { skyfs, skylink, system } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";
import { StatusBadge } from "./StatusBadge";
import { VersionOverlay, parseVersionDetails } from "./VersionOverlay";

const UPDATE_REFRESH_EVENTS = [
  "update:available",
  "update:download:complete",
  "update:download:error",
  "update:install:complete",
  "update:install:error",
] as const;

const navItems = [
  { to: "/agents", icon: "smart_toy", label: "Agents", matchPrefixes: ["/agents"] },
  {
    to: "/drives",
    icon: "folder_open",
    label: "Drives",
    matchPrefixes: ["/drives", "/bucket"],
  },
  { to: "/devices", icon: "devices", label: "Devices", matchPrefixes: ["/devices"] },
  { to: "/settings", icon: "settings", label: "Settings", matchPrefixes: ["/settings"] },
];

export function Sidebar() {
  const location = useLocation();
  const [versionOverlayOpen, setVersionOverlayOpen] = useState(false);
  const { data: health, refreshing } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: updateInfo } = useRPC(() => system.update.check(), [], {
    live: UPDATE_REFRESH_EVENTS,
  });
  const { data: stagedUpdate } = useRPC(() => system.update.status(), [], {
    live: UPDATE_REFRESH_EVENTS,
    refreshIntervalMs: 30_000,
  });
  const pending = (health?.outbox_pending ?? 0) + (health?.transfer_pending ?? 0);
  const syncing = pending > 0;
  const versionInfo = parseVersionDetails(health?.version ?? "");
  const versionLabel = versionInfo.version || health?.version?.split(" ")[0] || "...";
  const commitLabel = versionInfo.commit || "";
  const hasUpdateHighlight = Boolean(stagedUpdate?.ready) || Boolean(updateInfo?.available);
  const versionButtonClassName = hasUpdateHighlight
    ? "version-pill-attention mt-1 inline-flex items-center gap-2 rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1 text-[10px] text-emerald-900 shadow-[0_0_0_1px_rgba(16,185,129,0.08),0_10px_24px_-18px_rgba(16,185,129,0.9)] transition-colors hover:border-emerald-500/55 hover:bg-emerald-500/14 dark:text-emerald-100"
    : "mt-1 inline-flex items-center gap-2 rounded-full border border-outline-variant/20 bg-surface-container-lowest px-2.5 py-1 text-[10px] text-secondary transition-colors hover:border-primary/20 hover:text-on-surface";

  return (
    <>
      <aside className="fixed left-0 top-0 z-40 flex h-screen w-64 flex-col bg-surface-container-low font-body tracking-tight antialiased">
        <div className="px-6 py-8">
          {/* Brand */}
          <div className="mb-10 flex items-center gap-2">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg text-white lithic-gradient">
              <Icon name="cloud" filled className="text-lg" />
            </div>
            <div>
              <h1 className="text-xl font-bold tracking-tighter text-on-surface">sky10</h1>
              <button
                aria-label="Open build details"
                className={`relative overflow-hidden ${versionButtonClassName}`}
                onClick={() => setVersionOverlayOpen(true)}
                title={commitLabel ? `${versionLabel} / ${commitLabel}` : versionLabel}
                type="button"
              >
                {hasUpdateHighlight && (
                  <span
                    aria-hidden="true"
                    className="version-pill-core-glow pointer-events-none absolute left-1/2 top-1/2 h-5 w-16 -translate-x-1/2 -translate-y-1/2 rounded-full bg-emerald-400/20 blur-md"
                  />
                )}
                <span className="relative font-semibold tracking-[0.18em]">
                  {versionLabel}
                </span>
                {commitLabel && (
                  <span className={`relative font-mono text-[10px] ${hasUpdateHighlight ? "text-emerald-800/90 dark:text-emerald-100/90" : "text-outline"}`}>
                    {commitLabel}
                  </span>
                )}
              </button>
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
                    `flex items-center gap-3 rounded-lg px-4 py-2 text-sm font-medium transition-colors ${
                      isActive
                        ? "bg-surface-container-lowest text-primary shadow-sm"
                        : "text-secondary hover:bg-surface-container-high hover:text-on-surface"
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
        <div className="mt-auto space-y-4 p-6">
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
          <div className="border-t border-outline-variant/10 pt-4">
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
      <VersionOverlay
        health={health}
        onClose={() => setVersionOverlayOpen(false)}
        open={versionOverlayOpen}
      />
    </>
  );
}
