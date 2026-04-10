import { useLocation } from "react-router";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { openCommandPalette } from "../lib/commandPalette";
import { skyfs } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";
import { Icon } from "./Icon";
import { StatusBadge } from "./StatusBadge";

function getRouteMeta(pathname: string) {
  if (pathname.startsWith("/drives/")) {
    const [, , driveName] = pathname.split("/");
    return {
      description: "Browse synced files and create new folders in this drive.",
      title: driveName ? decodeURIComponent(driveName) : "Drive Browser",
    };
  }

  if (pathname.startsWith("/drives")) {
    return {
      description: "Monitor drive health, pending sync work, and mounted storage.",
      title: "Drives",
    };
  }

  if (pathname.startsWith("/bucket")) {
    return {
      description: "Inspect the raw S3 prefix tree and object keys without mixing it into your drive list.",
      title: "Bucket",
    };
  }

  if (pathname.startsWith("/kv")) {
    return {
      description: "Inspect replicated key-value entries and edit them live.",
      title: "Key-Value Store",
    };
  }

  if (pathname.startsWith("/devices/invite")) {
    return {
      description: "Generate an invite and keep an eye on the device handshake.",
      title: "Invite Device",
    };
  }

  if (pathname.startsWith("/devices")) {
    return {
      description: "Track the devices currently participating in your vault.",
      title: "Devices",
    };
  }

  if (pathname.startsWith("/network")) {
    return {
      description: "Watch network topology, peers, and link status update in place.",
      title: "Network",
    };
  }

  if (pathname.startsWith("/agents")) {
    return {
      description: "Coordinate agents across the P2P network.",
      title: "Agents",
    };
  }

  if (pathname.startsWith("/settings/sandboxes")) {
    return {
      description: "Provision isolated runtimes, inspect sandbox status, and review live provisioning output.",
      title: "Sandboxes",
    };
  }

  if (pathname.startsWith("/settings/apps")) {
    return {
      description: "Manage the helper binaries sky10 installs and updates on your machine.",
      title: "Managed Apps",
    };
  }

  if (pathname.startsWith("/settings")) {
    return {
      description: "Review identity, runtime, and local node configuration details.",
      title: "Settings",
    };
  }

  if (pathname.startsWith("/getting-started")) {
    return {
      description: "Set up your sky10 node and connect devices.",
      title: "Getting Started",
    };
  }

  return {
    description: "Control your local sky10 node from one command center.",
    title: "sky10",
  };
}

export function Header() {
  const location = useLocation();
  const route = getRouteMeta(location.pathname);
  const { data: health, refreshing } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const pending = health?.outbox_pending ?? 0;
  const isSyncing = pending > 0;

  return (
    <header className="sticky top-0 z-30 flex min-h-16 w-full items-center justify-between gap-4 border-b border-outline-variant/10 px-6 py-3 glass-effect sm:px-8">
      <div className="min-w-0">
        <p className="text-[10px] font-bold uppercase tracking-[0.22em] text-outline">
          Command Center
        </p>
        <div className="min-w-0">
          <h2 className="truncate text-lg font-semibold text-on-surface">
            {route.title}
          </h2>
          <p className="hidden truncate text-xs text-secondary md:block">
            {route.description}
          </p>
        </div>
      </div>

      <div className="flex items-center gap-3">
        {isSyncing ? (
          <StatusBadge icon="sync" pulse tone="processing">
            {pending} queued
          </StatusBadge>
        ) : (
          <StatusBadge pulse tone="live">
            Live
          </StatusBadge>
        )}
        {refreshing && (
          <StatusBadge icon="sync" tone="neutral">
            Refreshing
          </StatusBadge>
        )}
        <button
          onClick={openCommandPalette}
          className="group hidden items-center gap-3 rounded-full border border-outline-variant/20 bg-surface-container-high px-3 py-2 text-left text-xs text-secondary transition-colors hover:border-primary/20 hover:text-on-surface sm:flex"
          type="button"
        >
          <Icon name="search" className="text-lg text-outline group-hover:text-primary" />
          <span>Search commands, routes, and actions</span>
          <kbd className="rounded bg-surface-container-lowest px-1.5 py-0.5 font-mono text-[10px] text-outline">
            ⌘K
          </kbd>
        </button>
        <div className="hidden items-center gap-3 text-secondary md:flex">
          <button className="cursor-pointer transition-colors hover:text-primary" type="button">
            <Icon name="notifications" />
          </button>
          <button className="cursor-pointer transition-colors hover:text-primary" type="button">
            <Icon name="terminal" />
          </button>
          <button className="cursor-pointer transition-colors hover:text-primary" type="button">
            <Icon name="account_circle" />
          </button>
        </div>
      </div>
    </header>
  );
}
