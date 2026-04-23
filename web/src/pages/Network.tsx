import { useState } from "react";
import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { SettingsPage } from "../components/SettingsPage";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import {
  identity,
  skyfs,
  skylink,
  type Device,
  type LinkHealthEvent,
  type LinkLiveRelayHealth,
  type LinkMailboxHealth,
  type LinkNetworkHealth,
  type LinkRelayHealth,
  type LinkNostrSubscriptionHealth,
} from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

type NetworkStatusSummary = {
  tone: string;
  badge: string;
  title: string;
  detail: string;
};

function transportTone(health?: LinkNetworkHealth) {
  if (!health) return "bg-surface-container text-secondary";
  if (health.transport_degraded_reason) {
    return "bg-amber-500/15 text-amber-800 dark:text-amber-200";
  }
  return "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200";
}

function transportLabel(health?: LinkNetworkHealth) {
  if (!health?.preferred_transport) return "Unknown";
  const label = health.preferred_transport.toUpperCase();
  return health.transport_degraded_reason ? `${label} fallback` : label;
}

function fallbackTone(mailbox?: LinkMailboxHealth) {
  if (!mailbox) return "bg-surface-container text-secondary";
  if (mailbox.failed > 0) return "bg-error-container/30 text-error";
  if (mailbox.queued > 0 || mailbox.handed_off > 0) {
    return "bg-amber-500/15 text-amber-800 dark:text-amber-200";
  }
  return "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200";
}

function fallbackLabel(mailbox?: LinkMailboxHealth) {
  if (!mailbox) return "Unknown";
  if (mailbox.failed > 0) return "Needs attention";
  if (mailbox.handed_off > 0) return "Relay handoff";
  if (mailbox.queued > 0) return "Queued";
  return "Clear";
}

function coordinationTone(health?: LinkNetworkHealth) {
  if (!health) return "bg-surface-container text-secondary";
  if (health.coordination_degraded_reason) {
    return "bg-amber-500/15 text-amber-800 dark:text-amber-200";
  }
  return "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200";
}

function coordinationLabel(health?: LinkNetworkHealth) {
  if (!health?.nostr?.configured_relays) return "Not used";
  switch (health.coordination_degraded_reason) {
    case "nostr_subscription_down":
      return "Down";
    case "nostr_publish_quorum":
    case "nostr_subscription_quorum":
      return "Partial";
    default:
      return "Healthy";
  }
}

function describeTransportReason(reason?: string) {
  switch (reason) {
    case "udp_unreachable":
      return "UDP is blocked or unreachable, so direct QUIC is unavailable.";
    case "udp_mapping_varies":
      return "UDP NAT mapping varies by server, so direct QUIC is unreliable.";
    default:
      return reason
        ? `Transport issue: ${reason}`
        : "Live transport is healthy.";
  }
}

function describeDeliveryReason(reason?: string) {
  switch (reason) {
    case "mailbox_failures_pending":
      return "Durable mailbox delivery has pending failures that need attention.";
    case "mailbox_handoff_pending":
      return "Durable mailbox handoff is active while receipts catch up.";
    case "mailbox_queue_pending":
      return "Durable mailbox delivery is queued for later retry.";
    default:
      return reason
        ? `Delivery issue: ${reason}`
        : "Mailbox delivery is clear.";
  }
}

function describeCoordinationReason(reason?: string) {
  switch (reason) {
    case "nostr_publish_quorum":
      return "Nostr multi-relay publish is below quorum.";
    case "nostr_subscription_down":
      return "One or more live Nostr coordination subscriptions are down.";
    case "nostr_subscription_quorum":
      return "One or more live Nostr coordination subscriptions are only partially connected.";
    default:
      return reason
        ? `Coordination issue: ${reason}`
        : "Coordination is healthy.";
  }
}

function healthIssueLines(health?: LinkNetworkHealth) {
  if (!health) return [];
  const lines: string[] = [];
  if (health.transport_degraded_reason) {
    lines.push(
      `Transport: ${describeTransportReason(health.transport_degraded_reason)}`,
    );
  }
  if (health.delivery_degraded_reason) {
    lines.push(
      `Delivery: ${describeDeliveryReason(health.delivery_degraded_reason)}`,
    );
  }
  if (health.coordination_degraded_reason) {
    lines.push(
      `Coordination: ${describeCoordinationReason(health.coordination_degraded_reason)}`,
    );
  }
  return lines;
}

function networkStatusSummary(
  health?: LinkNetworkHealth,
): NetworkStatusSummary | null {
  if (!health) return null;
  if (health.transport_degraded_reason) {
    return {
      tone: "bg-amber-500/15 text-amber-900 border-amber-700/15 dark:text-amber-100 dark:border-amber-300/20",
      badge: "Transport",
      title: "Live transport is degraded.",
      detail: describeTransportReason(health.transport_degraded_reason),
    };
  }
  if (health.delivery_degraded_reason && health.coordination_degraded_reason) {
    return {
      tone: "bg-amber-500/15 text-amber-900 border-amber-700/15 dark:text-amber-100 dark:border-amber-300/20",
      badge: "Live OK",
      title: "Live transport is healthy.",
      detail: `${describeDeliveryReason(health.delivery_degraded_reason)} ${describeCoordinationReason(health.coordination_degraded_reason)}`,
    };
  }
  if (health.delivery_degraded_reason) {
    return {
      tone: "bg-amber-500/15 text-amber-900 border-amber-700/15 dark:text-amber-100 dark:border-amber-300/20",
      badge: "Live OK",
      title: "Live transport is healthy.",
      detail: describeDeliveryReason(health.delivery_degraded_reason),
    };
  }
  if (health.coordination_degraded_reason) {
    return {
      tone: "bg-amber-500/15 text-amber-900 border-amber-700/15 dark:text-amber-100 dark:border-amber-300/20",
      badge: "Live OK",
      title: "Live transport is healthy.",
      detail: describeCoordinationReason(health.coordination_degraded_reason),
    };
  }
  return {
    tone: "bg-emerald-500/15 text-emerald-900 border-emerald-700/15 dark:text-emerald-100 dark:border-emerald-300/20",
    badge: "Healthy",
    title: "Live transport and coordination are healthy.",
    detail:
      "Direct or relay-backed skylink delivery is available and coordination is in sync.",
  };
}

function liveRelayTone(liveRelay?: LinkLiveRelayHealth) {
  if (!liveRelay) return "bg-surface-container text-secondary";
  if (liveRelay.active_peers > 0)
    return "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200";
  if (liveRelay.configured_peers > 0 || liveRelay.cached_peers > 0) {
    return "bg-amber-500/15 text-amber-800 dark:text-amber-200";
  }
  return "bg-surface-container text-secondary";
}

function liveRelayLabel(liveRelay?: LinkLiveRelayHealth) {
  if (!liveRelay) return "Unknown";
  if (liveRelay.active_peers > 0) return "Active";
  if (liveRelay.configured_peers > 0 || liveRelay.cached_peers > 0)
    return "Configured";
  return "None";
}

function eventTone(status: string) {
  switch (status) {
    case "error":
      return "bg-error-container/30 text-error";
    case "warn":
      return "bg-amber-500/15 text-amber-800 dark:text-amber-200";
    default:
      return "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200";
  }
}

function eventLabel(event: LinkHealthEvent) {
  switch (event.type) {
    case "publish":
      return "Publish";
    case "connect":
      return "Connect";
    case "addresses":
      return "Address Change";
    case "reachability":
      return "Reachability";
    default:
      if (event.type.startsWith("coordination:")) {
        return `Coordination ${event.type.slice("coordination:".length)}`;
      }
      if (event.type.startsWith("mailbox:")) {
        return `Mailbox ${event.type.slice("mailbox:".length)}`;
      }
      return event.type;
  }
}

function relayTone(relay: LinkRelayHealth) {
  if (relay.failures > 0 && relay.successes === 0) {
    return "bg-error-container/30 text-error";
  }
  if (relay.failures > relay.successes) {
    return "bg-amber-500/15 text-amber-800 dark:text-amber-200";
  }
  return "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200";
}

function relayLabel(relay: LinkRelayHealth) {
  if (relay.failures > 0 && relay.successes === 0) return "Down";
  if (relay.failures > relay.successes) return "Partial";
  if (relay.successes > 0) return "Live";
  return "Idle";
}

function subscriptionLabel(subscription: LinkNostrSubscriptionHealth) {
  if (subscription.active_relays === 0) return "Down";
  if (
    subscription.required_relays > 0 &&
    subscription.active_relays < subscription.required_relays
  ) {
    return "Partial";
  }
  return "Live";
}

export default function Network() {
  const [connectAddr, setConnectAddr] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 5_000,
  });
  const { data: peersData } = useRPC(() => skylink.peers(), [], {
    refreshIntervalMs: 5_000,
  });
  const { data: deviceData } = useRPC(() => identity.deviceList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const peers = peersData?.peers ?? [];
  const networkHealth = linkStatus?.health;
  const recentEvents = networkHealth?.events ?? [];
  const relayHealth = networkHealth?.relays ?? [];
  const coordinationSubscriptions = networkHealth?.nostr?.subscriptions ?? [];
  const statusSummary = networkStatusSummary(networkHealth);
  const issueLines = healthIssueLines(networkHealth);

  const deviceByPeerID = new Map<string, Device>();
  for (const d of deviceData?.devices ?? []) {
    for (const ma of d.multiaddrs ?? []) {
      const match = ma.match(/\/p2p\/(.+)$/);
      if (match?.[1]) {
        deviceByPeerID.set(match[1], d);
      }
    }
  }

  return (
    <SettingsPage
      backHref="/settings"
      description="Track peers, relays, and delivery health."
      title="Network"
      width="wide"
    >
      <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4 2xl:grid-cols-7">
        {linkStatus && (
          <>
            <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
              <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
                Peer ID
              </span>
              <span className="mt-2 block truncate font-mono text-sm text-primary">
                {linkStatus.peer_id.slice(0, 16)}...
              </span>
            </div>
            <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
              <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
                Mode
              </span>
              <div className="mt-2 flex items-center gap-2">
                <span className="h-2 w-2 rounded-full bg-primary" />
                <span className="text-sm font-semibold capitalize text-on-surface">
                  {linkStatus.mode}
                </span>
              </div>
            </div>
            <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
              <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
                Transport
              </span>
              <span
                className={`mt-2 inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${transportTone(networkHealth)}`}
              >
                {transportLabel(networkHealth)}
              </span>
            </div>
            <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
              <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
                Fallback
              </span>
              <span
                className={`mt-2 inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${fallbackTone(networkHealth?.mailbox)}`}
              >
                {fallbackLabel(networkHealth?.mailbox)}
              </span>
            </div>
            <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
              <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
                Coordination
              </span>
              <span
                className={`mt-2 inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${coordinationTone(networkHealth)}`}
              >
                {coordinationLabel(networkHealth)}
              </span>
            </div>
            <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
              <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
                Live Relay
              </span>
              <span
                className={`mt-2 inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${liveRelayTone(networkHealth?.live_relay)}`}
              >
                {liveRelayLabel(networkHealth?.live_relay)}
              </span>
            </div>
          </>
        )}
        {health && (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
            <span className="text-[10px] font-bold uppercase tracking-widest text-secondary">
              Uptime
            </span>
            <span className="mt-2 block text-sm font-semibold text-on-surface">
              {health.uptime}
            </span>
          </div>
        )}
      </section>

      <div className="flex items-center gap-3">
        <input
          className="flex-1 rounded-lg border border-outline-variant/20 bg-surface-container px-4 py-2 font-mono text-sm text-on-surface outline-none focus:border-primary"
          onChange={(e) => {
            setConnectAddr(e.target.value);
            setConnectError(null);
          }}
          placeholder="/ip4/1.2.3.4/tcp/9100/p2p/12D3..."
          value={connectAddr}
        />
        <button
          className="rounded-full bg-primary px-5 py-2 text-sm font-semibold text-on-primary shadow-lg shadow-primary/20 hover:bg-primary/90 disabled:opacity-50"
          disabled={connecting || !connectAddr.trim()}
          onClick={async () => {
            setConnecting(true);
            setConnectError(null);
            try {
              await skylink.connect({ address: connectAddr.trim() });
              setConnectAddr("");
            } catch (e: unknown) {
              setConnectError(
                e instanceof Error ? e.message : "Failed to connect",
              );
            } finally {
              setConnecting(false);
            }
          }}
          type="button"
        >
          {connecting ? "Connecting..." : "Connect"}
        </button>
      </div>
      {connectError && (
        <div className="rounded-lg bg-error-container/20 p-3 text-sm text-error">
          {connectError}
        </div>
      )}
      {statusSummary && (
        <div className={`rounded-xl border px-5 py-4 ${statusSummary.tone}`}>
          <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
            <div>
              <div className="text-sm font-semibold">{statusSummary.title}</div>
              <div className="mt-1 text-sm opacity-90">
                {statusSummary.detail}
              </div>
            </div>
            <span className="inline-flex w-fit rounded-full bg-surface-container-high/80 px-2 py-1 text-[10px] font-bold uppercase tracking-widest text-on-surface">
              {statusSummary.badge}
            </span>
          </div>
        </div>
      )}

      <div className="grid grid-cols-12 gap-8">
        <div className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 min-h-[400px] relative overflow-hidden flex items-center justify-center border border-outline-variant/10 shadow-sm">
          <div className="network-grid absolute inset-0" />
          <div className="relative w-full h-full flex items-center justify-center">
            <svg className="absolute inset-0 h-full w-full text-emerald-500/40 dark:text-emerald-300/30">
              {peers.map((_, i) => {
                const angle =
                  (i / Math.max(peers.length, 1)) * 2 * Math.PI - Math.PI / 2;
                const x2 = 50 + 30 * Math.cos(angle);
                const y2 = 50 + 30 * Math.sin(angle);
                return (
                  <line
                    key={i}
                    x1="50%"
                    y1="50%"
                    x2={`${x2}%`}
                    y2={`${y2}%`}
                    stroke="currentColor"
                    strokeWidth="2"
                  />
                );
              })}
            </svg>
            <div className="relative z-10 w-24 h-24 rounded-full glass-effect border-4 border-primary/20 flex items-center justify-center shadow-2xl">
              <div className="flex flex-col items-center">
                <Icon name="token" className="text-primary text-3xl" />
                <span className="text-[10px] font-bold mt-1 uppercase text-primary">
                  This Node
                </span>
              </div>
            </div>
            {peers.map((peer, i) => {
              const angle =
                (i / Math.max(peers.length, 1)) * 2 * Math.PI - Math.PI / 2;
              const x = 50 + 30 * Math.cos(angle);
              const y = 50 + 30 * Math.sin(angle);
              const device = deviceByPeerID.get(peer.peer_id);
              return (
                <div
                  key={peer.peer_id}
                  className="absolute flex flex-col items-center gap-2"
                  style={{
                    left: `${x}%`,
                    top: `${y}%`,
                    transform: "translate(-50%, -50%)",
                  }}
                >
                  <div className="flex h-12 w-12 items-center justify-center rounded-full border border-emerald-500/20 bg-surface-container-lowest shadow-lg">
                    <Icon name="laptop_mac" className="text-secondary" />
                  </div>
                  <div className="px-2 py-1 bg-surface-container-high rounded-full text-[10px] font-medium whitespace-nowrap">
                    {device?.name ?? truncAddr(peer.peer_id)}
                  </div>
                </div>
              );
            })}
            {peers.length === 0 && (
              <div className="absolute bottom-8 text-sm text-outline">
                No peers connected
              </div>
            )}
          </div>
        </div>

        <div className="col-span-12 lg:col-span-4 flex flex-col gap-8">
          <div className="bg-surface-container-low rounded-xl p-8 relative overflow-hidden">
            <div className="flex items-center gap-4 mb-4">
              <div className="w-10 h-10 rounded-full bg-green-500/10 flex items-center justify-center">
                <Icon
                  name="sensors"
                  className="text-emerald-700 dark:text-emerald-300"
                />
              </div>
              <div>
                <h3 className="font-bold text-2xl">
                  {linkStatus?.private_peers ?? peersData?.count ?? 0} Active
                </h3>
                <p className="text-xs text-secondary font-medium uppercase tracking-widest">
                  Private Peers
                </p>
              </div>
            </div>
            {networkHealth && (
              <div className="grid grid-cols-2 gap-3 text-xs text-secondary">
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Public Addr
                  </div>
                  <div className="mt-1 font-mono text-on-surface">
                    {networkHealth.public_addr ?? "Unknown"}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Reachability
                  </div>
                  <div className="mt-1 font-semibold capitalize text-on-surface">
                    {networkHealth.reachability || "Unknown"}
                  </div>
                </div>
              </div>
            )}
          </div>

          {networkHealth && (
            <div className="bg-surface-container-lowest rounded-xl p-6 border border-outline-variant/10 shadow-sm space-y-4">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest">
                Health Summary
              </h4>
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Preferred Path
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {transportLabel(networkHealth)}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    UDP
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.netcheck.udp
                      ? "Reachable"
                      : "Blocked or unknown"}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Private Mailbox
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.mailbox.pending_private}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    sky10 Mailbox
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.mailbox.pending_sky10_network}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Handed Off
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.mailbox.handed_off}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Nostr Publish
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.nostr.last_publish.successes || 0}/
                    {networkHealth.nostr.last_publish.quorum || 0}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Live Subs
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {coordinationSubscriptions.length}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Live Relay
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.live_relay.active_peers}/
                    {networkHealth.live_relay.configured_peers ||
                      networkHealth.live_relay.cached_peers ||
                      0}
                  </div>
                </div>
                <div>
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Failed
                  </div>
                  <div className="mt-1 font-semibold text-on-surface">
                    {networkHealth.mailbox.failed}
                  </div>
                </div>
              </div>
              {issueLines.length > 0 && (
                <div className="rounded-lg bg-surface-container-low p-3 text-xs text-secondary">
                  {issueLines.map((line) => (
                    <div key={line}>{line}</div>
                  ))}
                </div>
              )}
            </div>
          )}

          {networkHealth?.live_relay?.configured_peers ||
          networkHealth?.live_relay?.cached_peers ||
          networkHealth?.live_relay?.active_peers ? (
            <div className="bg-surface-container-lowest rounded-xl p-6 border border-outline-variant/10 shadow-sm space-y-4">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest">
                Live Relay
              </h4>
              <div className="rounded-lg bg-surface-container-low px-4 py-3 text-[11px] text-secondary space-y-1">
                <div>
                  Configured peers:{" "}
                  {networkHealth?.live_relay?.configured_peers ?? 0}
                </div>
                <div>
                  Cached peers: {networkHealth?.live_relay?.cached_peers ?? 0}
                </div>
                <div>
                  Active peers: {networkHealth?.live_relay?.active_peers ?? 0}
                </div>
                {networkHealth?.live_relay?.last_bootstrap_at && (
                  <div>
                    Cache updated{" "}
                    <RelativeTime
                      value={networkHealth.live_relay.last_bootstrap_at}
                    />
                  </div>
                )}
              </div>
              {networkHealth?.live_relay?.current_peer_id && (
                <div className="rounded-lg bg-surface-container-low px-4 py-3">
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Current Relay
                  </div>
                  <div className="mt-1 font-mono text-xs text-on-surface">
                    {networkHealth.live_relay.current_peer_id}
                  </div>
                </div>
              )}
              {networkHealth?.live_relay?.preferred_peer_id && (
                <div className="rounded-lg bg-surface-container-low px-4 py-3">
                  <div className="text-[10px] font-bold uppercase tracking-widest text-outline">
                    Home Relay
                  </div>
                  <div className="mt-1 font-mono text-xs text-on-surface">
                    {networkHealth.live_relay.preferred_peer_id}
                  </div>
                  <div className="mt-2 space-y-1 text-[11px] text-secondary">
                    {networkHealth.live_relay.preferred_at && (
                      <div>
                        Preferred{" "}
                        <RelativeTime
                          value={networkHealth.live_relay.preferred_at}
                        />
                      </div>
                    )}
                    {networkHealth.live_relay.last_switch_at && (
                      <div>
                        Last switch{" "}
                        <RelativeTime
                          value={networkHealth.live_relay.last_switch_at}
                        />
                      </div>
                    )}
                  </div>
                </div>
              )}
              {(networkHealth?.live_relay?.active_addrs ?? []).length > 0 && (
                <div className="space-y-2">
                  {(networkHealth?.live_relay?.active_addrs ?? []).map(
                    (addr) => (
                      <div
                        key={addr}
                        className="rounded-lg bg-surface-container-low px-4 py-3 font-mono text-[11px] text-on-surface break-all"
                      >
                        {addr}
                      </div>
                    ),
                  )}
                </div>
              )}
            </div>
          ) : null}

          {relayHealth.length > 0 && (
            <div className="bg-surface-container-lowest rounded-xl p-6 border border-outline-variant/10 shadow-sm space-y-4">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest">
                Nostr Coordination
              </h4>
              <div className="space-y-3">
                {relayHealth.map((relay) => (
                  <div
                    key={relay.url}
                    className="rounded-lg bg-surface-container-low px-4 py-3"
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <div className="truncate font-mono text-xs text-on-surface">
                          {relay.url}
                        </div>
                        <div className="mt-1 text-[11px] text-secondary">
                          ok {relay.successes} · fail {relay.failures}
                          {relay.average_latency_ms
                            ? ` · avg ${relay.average_latency_ms}ms`
                            : ""}
                          {relay.active_subscriptions
                            ? ` · subs ${relay.active_subscriptions}`
                            : ""}
                        </div>
                      </div>
                      <span
                        className={`rounded-full px-2 py-1 text-[10px] font-bold uppercase tracking-wider ${relayTone(relay)}`}
                      >
                        {relayLabel(relay)}
                      </span>
                    </div>
                    {(relay.last_error ||
                      relay.last_success_at ||
                      relay.last_failure_at ||
                      relay.last_subscription_at ||
                      relay.last_subscription_error) && (
                      <div className="mt-3 space-y-1 text-[11px] text-secondary">
                        {relay.last_success_at && (
                          <div>
                            Last ok{" "}
                            <RelativeTime value={relay.last_success_at} />
                          </div>
                        )}
                        {relay.last_failure_at && (
                          <div>
                            Last fail{" "}
                            <RelativeTime value={relay.last_failure_at} />
                          </div>
                        )}
                        {relay.last_error && (
                          <div className="truncate">
                            Error: {relay.last_error}
                          </div>
                        )}
                        {relay.last_subscription_at && (
                          <div>
                            Last sub{" "}
                            <RelativeTime value={relay.last_subscription_at} />
                          </div>
                        )}
                        {relay.last_subscription_error && (
                          <div className="truncate">
                            Sub error: {relay.last_subscription_error}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
              {networkHealth?.nostr?.last_publish?.at && (
                <div className="rounded-lg bg-surface-container-low px-4 py-3 text-[11px] text-secondary">
                  Last multi-relay publish{" "}
                  {networkHealth.nostr.last_publish.operation || "unknown"} hit{" "}
                  {networkHealth.nostr.last_publish.successes}/
                  {networkHealth.nostr.last_publish.quorum || 0} relays{" "}
                  <RelativeTime value={networkHealth.nostr.last_publish.at} />
                </div>
              )}
              {coordinationSubscriptions.length > 0 && (
                <div className="space-y-3">
                  {coordinationSubscriptions.map((subscription) => (
                    <div
                      key={subscription.label}
                      className="rounded-lg bg-surface-container-low px-4 py-3"
                    >
                      <div className="flex items-center justify-between gap-3">
                        <div className="min-w-0">
                          <div className="truncate font-mono text-xs text-on-surface">
                            {subscription.label}
                          </div>
                          <div className="mt-1 text-[11px] text-secondary">
                            active {subscription.active_relays}/
                            {subscription.required_relays ||
                              networkHealth?.nostr?.configured_relays ||
                              0}
                          </div>
                        </div>
                        <span
                          className={`rounded-full px-2 py-1 text-[10px] font-bold uppercase tracking-wider ${
                            subscription.active_relays === 0
                              ? "bg-error-container/30 text-error"
                              : subscription.required_relays > 0 &&
                                  subscription.active_relays <
                                    subscription.required_relays
                                ? "bg-amber-500/15 text-amber-800 dark:text-amber-200"
                                : "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200"
                          }`}
                        >
                          {subscriptionLabel(subscription)}
                        </span>
                      </div>
                      {(subscription.last_event_at ||
                        subscription.last_disconnect_at ||
                        subscription.last_error) && (
                        <div className="mt-3 space-y-1 text-[11px] text-secondary">
                          {subscription.last_event_at && (
                            <div>
                              Last event{" "}
                              <RelativeTime
                                value={subscription.last_event_at}
                              />
                            </div>
                          )}
                          {subscription.last_disconnect_at && (
                            <div>
                              Last disconnect{" "}
                              <RelativeTime
                                value={subscription.last_disconnect_at}
                              />
                            </div>
                          )}
                          {subscription.last_error && (
                            <div className="truncate">
                              Error: {subscription.last_error}
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          <div className="bg-surface-container-lowest rounded-xl p-6 border border-outline-variant/10 shadow-sm flex-1 space-y-6">
            <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest mb-4">
              Peer Details
            </h4>
            <div className="space-y-6">
              {peers.map((peer) => {
                const device = deviceByPeerID.get(peer.peer_id);
                return (
                  <div key={peer.peer_id} className="flex items-start gap-4">
                    <div className="w-8 h-8 rounded-lg bg-surface-container-high flex items-center justify-center shrink-0">
                      <Icon
                        name="laptop_mac"
                        className="text-sm text-on-surface-variant"
                      />
                    </div>
                    <div className="flex-1 space-y-2 min-w-0">
                      <div className="flex justify-between items-center">
                        <span className="text-sm font-bold truncate">
                          {device?.name ?? "Unknown"}
                        </span>
                        <span className="shrink-0 rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-bold text-emerald-800 dark:text-emerald-200">
                          LIVE
                        </span>
                      </div>
                      <div className="text-[10px] font-mono text-secondary truncate">
                        {truncAddr(peer.peer_id)}
                      </div>
                      {device && (
                        <div className="text-[10px] text-secondary">
                          {device.platform} &middot;{" "}
                          {device.location || device.ip} &middot; seen{" "}
                          <RelativeTime value={device.last_seen} />
                        </div>
                      )}
                    </div>
                  </div>
                );
              })}
              {peers.length === 0 && (
                <p className="text-sm text-outline text-center py-4">
                  No peers connected
                </p>
              )}
            </div>
          </div>
        </div>

        {linkStatus && linkStatus.addrs.length > 0 && (
          <div className="col-span-12">
            <div className="bg-surface-container-lowest rounded-xl p-8 border border-outline-variant/10 shadow-sm">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest mb-4">
                Listen Addresses
              </h4>
              <div className="space-y-2 font-mono text-sm">
                {linkStatus.addrs.map((addr) => (
                  <div
                    key={addr}
                    className="py-2 px-4 bg-surface-container-low rounded-lg text-on-surface-variant"
                  >
                    {addr}
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}

        <div className="col-span-12">
          <div className="bg-surface-container-lowest rounded-xl p-8 border border-outline-variant/10 shadow-sm">
            <div className="flex items-center justify-between gap-4 mb-4">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest">
                Recent Network Events
              </h4>
              {networkHealth?.last_published_at && (
                <div className="text-xs text-secondary">
                  Last publish{" "}
                  <RelativeTime value={networkHealth.last_published_at} />
                </div>
              )}
            </div>
            {recentEvents.length === 0 ? (
              <p className="text-sm text-outline">No recent network events.</p>
            ) : (
              <div className="space-y-3">
                {recentEvents.map((event) => (
                  <div
                    key={`${event.type}-${event.at}-${event.detail ?? ""}`}
                    className="flex flex-col gap-2 rounded-lg bg-surface-container-low px-4 py-3 md:flex-row md:items-center md:justify-between"
                  >
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-on-surface">
                          {eventLabel(event)}
                        </span>
                        <span
                          className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${eventTone(event.status)}`}
                        >
                          {event.status}
                        </span>
                      </div>
                      {event.detail && (
                        <div className="mt-1 truncate text-xs text-secondary">
                          {event.detail}
                        </div>
                      )}
                    </div>
                    <div className="text-xs text-secondary">
                      <RelativeTime value={event.at} />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>
    </SettingsPage>
  );
}
