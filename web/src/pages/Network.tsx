import { useState } from "react";
import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { skylink, skyfs, type Device } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

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
  const { data: deviceData } = useRPC(() => skyfs.deviceList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const peers = peersData?.peers ?? [];

  // Build a device lookup by peer ID extracted from multiaddrs
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
      {/* Overview panel */}
      <section className="flex flex-col md:flex-row justify-between items-start gap-8">
        <div className="space-y-2">
          <h2 className="text-4xl font-bold tracking-tight text-on-surface">
            Network Dashboard
          </h2>
          <p className="text-secondary text-lg">
            P2P topology and node synchronization status.
          </p>
        </div>
        <div className="flex gap-4">
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

      {/* Connect to peer */}
      <div className="flex items-center gap-3">
        <input
          className="flex-1 rounded-lg border border-outline-variant/20 bg-surface-container px-4 py-2 font-mono text-sm text-on-surface outline-none focus:border-primary"
          onChange={(e) => { setConnectAddr(e.target.value); setConnectError(null); }}
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
        {/* Connection graph */}
        <div className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 min-h-[400px] relative overflow-hidden flex items-center justify-center border border-outline-variant/10 shadow-sm">
          <div className="absolute inset-0 bg-[radial-gradient(#007AFF10_1px,transparent_1px)] [background-size:24px_24px]" />
          <div className="relative w-full h-full flex items-center justify-center">
            {/* SVG lines to peers */}
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
            {/* Central node */}
            <div className="relative z-10 w-24 h-24 rounded-full glass-effect border-4 border-primary/20 flex items-center justify-center shadow-2xl">
              <div className="flex flex-col items-center">
                <Icon name="token" className="text-primary text-3xl" />
                <span className="text-[10px] font-bold mt-1 uppercase text-primary">
                  This Node
                </span>
              </div>
            </div>
            {/* Peer nodes */}
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

        {/* Peer list */}
        <div className="col-span-12 lg:col-span-4 flex flex-col gap-8">
          <div className="bg-surface-container-low rounded-xl p-8 relative overflow-hidden">
            <div className="flex items-center gap-4 mb-4">
              <div className="w-10 h-10 rounded-full bg-green-500/10 flex items-center justify-center">
                <Icon name="sensors" className="text-green-600" />
              </div>
              <div>
                <h3 className="font-bold text-2xl">
                  {peersData?.count ?? 0} Active
                </h3>
                <p className="text-xs text-secondary font-medium uppercase tracking-widest">
                  Connected Peers
                </p>
              </div>
            </div>
          </div>
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

        {/* Listen addresses */}
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
      </div>
    </div>
  );
}
