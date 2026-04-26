import { useState } from "react";
import { Link, NavLink, useLocation } from "react-router";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { isPinnablePagePath } from "../lib/pinnablePages";
import { usePinnedSidebarPages } from "../lib/usePinnedSidebarPages";
import { Icon } from "./Icon";
import { system } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";
import { VersionOverlay, parseVersionDetails } from "./VersionOverlay";

const UPDATE_REFRESH_EVENTS = [
  "update:available",
  "update:download:complete",
  "update:download:error",
  "update:install:complete",
  "update:install:error",
] as const;

const navItems = [
  {
    to: "/agents",
    icon: "smart_toy",
    label: "Agents",
    matchPrefixes: ["/agents"],
  },
  {
    to: "/drives",
    icon: "folder_open",
    label: "Drives",
    matchPrefixes: ["/drives", "/bucket"],
  },
  {
    to: "/settings",
    icon: "settings",
    label: "Settings",
    matchPrefixes: ["/settings"],
  },
];

export function Sidebar() {
  const location = useLocation();
  const [versionOverlayOpen, setVersionOverlayOpen] = useState(false);
  const { pinnedPages, unpinPage } = usePinnedSidebarPages();
  const { data: health } = useRPC(() => system.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: updateInfo } = useRPC(() => system.update.check(), [], {
    live: UPDATE_REFRESH_EVENTS,
  });
  const { data: stagedUpdate } = useRPC(() => system.update.status(), [], {
    live: UPDATE_REFRESH_EVENTS,
    refreshIntervalMs: 30_000,
  });
  const versionInfo = parseVersionDetails(health?.version ?? "");
  const versionLabel =
    versionInfo.version || health?.version?.split(" ")[0] || "...";
  const commitLabel = versionInfo.commit || "";
  const hasUpdateHighlight =
    Boolean(stagedUpdate?.ready) || Boolean(updateInfo?.available);
  const versionButtonClassName = hasUpdateHighlight
    ? "version-pill-attention mt-1 inline-flex items-center gap-2 rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1 text-[10px] text-emerald-900 shadow-[0_0_0_1px_rgba(16,185,129,0.08),0_10px_24px_-18px_rgba(16,185,129,0.9)] transition-colors hover:border-emerald-500/55 hover:bg-emerald-500/14 dark:text-emerald-100"
    : "mt-1 inline-flex items-center gap-2 rounded-full border border-outline-variant/20 bg-surface-container-lowest px-2.5 py-1 text-[10px] text-secondary transition-colors hover:border-primary/20 hover:text-on-surface";
  const activePinnedPage = pinnedPages.find((page) =>
    isPinnablePagePath(location.pathname, page),
  );

  return (
    <>
      <aside className="sticky top-0 z-40 flex h-screen w-64 shrink-0 flex-col border-r border-outline-variant/10 bg-surface-container-low font-body tracking-tight antialiased">
        <div className="flex h-full flex-col px-6 py-7">
          {/* Brand */}
          <div className="mb-8 flex items-center gap-2">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg text-white lithic-gradient">
              <Icon name="cloud" filled className="text-lg" />
            </div>
            <div>
              <h1 className="text-xl font-bold tracking-tighter text-on-surface">
                sky10
              </h1>
              <button
                aria-label="Open build details"
                className={`relative overflow-hidden ${versionButtonClassName}`}
                onClick={() => setVersionOverlayOpen(true)}
                title={
                  commitLabel
                    ? `${versionLabel} / ${commitLabel}`
                    : versionLabel
                }
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
                  <span
                    className={`relative font-mono text-[10px] ${hasUpdateHighlight ? "text-emerald-800/90 dark:text-emerald-100/90" : "text-outline"}`}
                  >
                    {commitLabel}
                  </span>
                )}
              </button>
            </div>
          </div>
          {/* Navigation */}
          <nav className="min-h-0 flex-1 space-y-6 overflow-y-auto pr-1">
            <div className="space-y-1.5">
              {navItems.map((item) => {
                const matchesItem = item.matchPrefixes.some(
                  (prefix) =>
                    location.pathname === prefix ||
                    location.pathname.startsWith(`${prefix}/`),
                );
                const isActive =
                  matchesItem &&
                  !(item.to === "/settings" && activePinnedPage);

                return (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    className={() =>
                      `flex items-center gap-3 rounded-xl px-4 py-2.5 text-sm font-medium transition-colors ${
                        isActive
                          ? "bg-surface-container-lowest text-primary shadow-sm ring-1 ring-outline-variant/10"
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
            </div>

            {pinnedPages.length > 0 && (
              <div className="space-y-1.5">
                <p className="px-4 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Pinned
                </p>
                {pinnedPages.map((page) => {
                  const isActive = isPinnablePagePath(
                    location.pathname,
                    page,
                  );

                  return (
                    <div
                      className={`group flex items-center overflow-hidden rounded-xl transition-colors ${
                        isActive
                          ? "bg-surface-container-lowest text-primary shadow-sm ring-1 ring-outline-variant/10"
                          : "text-secondary hover:bg-surface-container-high hover:text-on-surface"
                      }`}
                      key={page.id}
                    >
                      <Link
                        className="flex min-w-0 flex-1 items-center gap-3 px-4 py-2.5 text-sm font-medium"
                        to={page.to}
                      >
                        <Icon name={page.icon} filled={isActive} />
                        <span className="truncate">{page.label}</span>
                      </Link>
                      <button
                        aria-label={`Unpin ${page.label} from sidebar`}
                        className={`mr-2 flex h-7 w-7 shrink-0 items-center justify-center rounded-full transition-colors ${
                          isActive
                            ? "text-primary hover:bg-primary/10"
                            : "text-outline hover:bg-surface-container-lowest hover:text-on-surface"
                        }`}
                        onClick={() => unpinPage(page.id)}
                        title={`Unpin ${page.label}`}
                        type="button"
                      >
                        <Icon
                          className="text-base"
                          filled
                          name="push_pin"
                        />
                      </button>
                    </div>
                  );
                })}
              </div>
            )}
          </nav>
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
