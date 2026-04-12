import { useState } from "react";
import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import {
  identity,
  skyfs,
  skylink,
  type Device,
  type LinkHealthEvent,
  type LinkMailboxHealth,
  type LinkNetworkHealth,
  type LinkRelayHealth,
} from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

function transportTone(health?: LinkNetworkHealth) {
  if (!health) return "bg-surface-container text-secondary";
  if (health.transport_degraded_reason) {
    return "bg-amber-500/10 text-amber-700";
  }
  return "bg-emerald-500/10 text-emerald-700";
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
    return "bg-amber-500/10 text-amber-700";
  }
  return "bg-emerald-500/10 text-emerald-700";
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
    return "bg-amber-500/10 text-amber-700";
  }
  return "bg-emerald-500/10 text-emerald-700";
}

function coordinationLabel(health?: LinkNetworkHealth) {
  if (!health?.nostr?.configured_relays) return "Not configured";
  if (health.coordination_degraded_reason) return "Degraded";
  return "Healthy";
}

function eventTone(status: string) {
  switch (status) {
    case "error":
      return "bg-error-container/30 text-error";
    case "warn":
      return "bg-amber-500/10 text-amber-700";
    default:
      return "bg-emerald-500/10 text-emerald-700";
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
    return "bg-amber-500/10 text-amber-700";
  }
  return "bg-emerald-500/10 text-emerald-700";
}

function relayLabel(relay: LinkRelayHealth) {
  if (relay.failures > 0 && relay.successes === 0) return "Failing";
  if (relay.failures > relay.successes) return "Degraded";
  if (relay.successes > 0) return "Healthy";
  return "Idle";
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
    <div className="p-12 max-w-7xl mx-auto space-y-12">
      <section className="flex flex-col md:flex-row justify-between items-start gap-8">
        <div className="space-y-2">
          <h2 className="text-4xl font-bold tracking-tight text-on-surface">
            Network Dashboard
          </h2>
          <p className="text-secondary text-lg">
            Live transport health, fallback delivery state, and recent convergence events.
          </p>
        </div>
        <div className="flex flex-wrap gap-4">
          {linkStatus && (
            <>
              <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[160px]">
                <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
                  Peer ID
                </span>
                <span className="font-mono text-sm text-primary truncate max-w-[180px]">
                  {linkStatus.peer_id.slice(0, 16)}...
                </span>
              </div>
              <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[120px]">
                <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
                  Mode
                </span>
                <div className="flex items-center gap-2">
                  <span className="w-2 h-2 bg-primary rounded-full" />
                  <span className="font-semibold text-sm capitalize">
                    {linkStatus.mode}
                  </span>
                </div>
              </div>
              <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[140px]">
                <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
                  Transport
                </span>
                <span className={`inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${transportTone(networkHealth)}`}>
                  {transportLabel(networkHealth)}
                </span>
              </div>
              <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[150px]">
                <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
                  Fallback
                </span>
                <span className={`inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${fallbackTone(networkHealth?.mailbox)}`}>
                  {fallbackLabel(networkHealth?.mailbox)}
                </span>
              </div>
              <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[150px]">
                <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
                  Coordination
                </span>
                <span className={`inline-flex w-fit rounded-full px-2 py-1 text-xs font-bold ${coordinationTone(networkHealth)}`}>
                  {coordinationLabel(networkHealth)}
                </span>
              </div>
            </>
          )}
          {health && (
            <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[120px]">
              <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
                Uptime
              </span>
              <span className="font-semibold text-sm">{health.uptime}</span>
            </div>
          )}
        </div>
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
          className="rounded-full bg-primary px-5 py-2 text-sm font-semibold text-white shadow-lg shadow-primary/20 hover:bg-primary/90 disabled:opacity-50"
          disabled={connecting || !connectAddr.trim()}
          onClick={async () => {
            setConnecting(true);
            setConnectError(null);
            try {
              await skylink.connect({ address: connectAddr.trim() });
              setConnectAddr("");
            } catch (e: unknown) {
              setConnectError(e instanceof Error ? e.message : "Failed to connect");
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

      <div className="grid grid-cols-12 gap-8">
        <div className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 min-h-[400px] relative overflow-hidden flex items-center justify-center border border-outline-variant/10 shadow-sm">
          <div className="absolute inset-0 bg-[radial-gradient(#007AFF10_1px,transparent_1px)] [background-size:24px_24px]" />
          <div className="relative w-full h-full flex items-center justify-center">
            <svg className="absolute inset-0 w-full h-full">
              {peers.map((_, i) => {
                const angle = (i / Math.max(peers.length, 1)) * 2 * Math.PI - Math.PI / 2;
                const x2 = 50 + 30 * Math.cos(angle);
                const y2 = 50 + 30 * Math.sin(angle);
                return (
                  <line
                    key={i}
                    x1="50%"
                    y1="50%"
                    x2={`${x2}%`}
                    y2={`${y2}%`}
                    stroke="#10b981"
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
              const angle = (i / Math.max(peers.length, 1)) * 2 * Math.PI - Math.PI / 2;
              const x = 50 + 30 * Math.cos(angle);
              const y = 50 + 30 * Math.sin(angle);
              const device = deviceByPeerID.get(peer.peer_id);
              return (
                <div
                  key={peer.peer_id}
                  className="absolute flex flex-col items-center gap-2"
                  style={{ left: `${x}%`, top: `${y}%`, transform: "translate(-50%, -50%)" }}
                >
                  <div className="w-12 h-12 rounded-full bg-white shadow-lg border border-green-500/30 flex items-center justify-center">
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
                <Icon name="sensors" className="text-green-600" />
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
                    {networkHealth.netcheck.udp ? "Reachable" : "Blocked or unknown"}
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
                    {networkHealth.nostr.last_publish.successes || 0}/{networkHealth.nostr.last_publish.quorum || 0}
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
              {(networkHealth.transport_degraded_reason || networkHealth.delivery_degraded_reason || networkHealth.coordination_degraded_reason) && (
                <div className="rounded-lg bg-surface-container-low p-3 text-xs text-secondary">
                  {networkHealth.transport_degraded_reason && (
                    <div>Transport: {networkHealth.transport_degraded_reason}</div>
                  )}
                  {networkHealth.delivery_degraded_reason && (
                    <div>Delivery: {networkHealth.delivery_degraded_reason}</div>
                  )}
                  {networkHealth.coordination_degraded_reason && (
                    <div>Coordination: {networkHealth.coordination_degraded_reason}</div>
                  )}
                </div>
              )}
            </div>
          )}

          {relayHealth.length > 0 && (
            <div className="bg-surface-container-lowest rounded-xl p-6 border border-outline-variant/10 shadow-sm space-y-4">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest">
                Nostr Relays
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
                          {relay.average_latency_ms ? ` · avg ${relay.average_latency_ms}ms` : ""}
                        </div>
                      </div>
                      <span className={`rounded-full px-2 py-1 text-[10px] font-bold uppercase tracking-wider ${relayTone(relay)}`}>
                        {relayLabel(relay)}
                      </span>
                    </div>
                    {(relay.last_error || relay.last_success_at || relay.last_failure_at) && (
                      <div className="mt-3 space-y-1 text-[11px] text-secondary">
                        {relay.last_success_at && (
                          <div>
                            Last ok <RelativeTime value={relay.last_success_at} />
                          </div>
                        )}
                        {relay.last_failure_at && (
                          <div>
                            Last fail <RelativeTime value={relay.last_failure_at} />
                          </div>
                        )}
                        {relay.last_error && (
                          <div className="truncate">Error: {relay.last_error}</div>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
              {networkHealth?.nostr?.last_publish?.at && (
                <div className="rounded-lg bg-surface-container-low px-4 py-3 text-[11px] text-secondary">
                  Last multi-relay publish {networkHealth.nostr.last_publish.operation || "unknown"} hit{" "}
                  {networkHealth.nostr.last_publish.successes}/{networkHealth.nostr.last_publish.quorum || 0}
                  {" "}
                  relays <RelativeTime value={networkHealth.nostr.last_publish.at} />
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
                        <span className="text-[10px] px-1.5 py-0.5 bg-green-500/10 text-green-700 rounded font-bold shrink-0">
                          LIVE
                        </span>
                      </div>
                      <div className="text-[10px] font-mono text-secondary truncate">
                        {truncAddr(peer.peer_id)}
                      </div>
                      {device && (
                        <div className="text-[10px] text-secondary">
                          {device.platform} &middot; {device.location || device.ip} &middot; seen{" "}
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
                  Last publish <RelativeTime value={networkHealth.last_published_at} />
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
    </div>
  );
}
