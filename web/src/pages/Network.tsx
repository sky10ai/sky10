import { Icon } from "../components/Icon";

const activityLog = [
  {
    time: "14:22:45.02",
    level: "INFO",
    levelColor: "text-green-600 bg-green-50",
    message: "Connected to",
    highlight: "MacBook Pro",
    detail: "(0x7a2...f92d)",
    meta: "latency: 0.2ms",
  },
  {
    time: "14:23:01.88",
    level: "SYNC",
    levelColor: "text-blue-600 bg-blue-50",
    message: "Vault synchronization notification sent to",
    highlight: "3 peers",
    meta: "protocol: sky-v2",
  },
  {
    time: "14:23:12.44",
    level: "WARN",
    levelColor: "text-amber-600 bg-amber-50",
    message: "Connection to",
    highlight: "S3-Storage",
    detail: "relayed through",
    highlight2: "Frankfurt",
    meta: "hop: 1",
  },
  {
    time: "14:23:15.10",
    level: "INFO",
    levelColor: "text-green-600 bg-green-50",
    message: "Agent",
    highlight: "Worker-Node-Alpha",
    detail: "registered capability: IMAGE_RESIZE_V1",
    meta: "status: ok",
  },
  {
    time: "14:23:45.33",
    level: "SYNC",
    levelColor: "text-blue-600 bg-blue-50",
    message: "P2P Heartbeat broadcast complete",
    meta: "peers: 4",
  },
];

const peers = [
  {
    name: "MacBook Pro (User-1)",
    icon: "laptop_mac",
    status: "LIVE",
    statusColor: "bg-green-500/10 text-green-700",
    caps: ["FS_SYNC", "RPC_CALL", "KV_STORE"],
  },
  {
    name: "Frankfurt Relay",
    icon: "dns",
    status: "RELAY",
    statusColor: "bg-blue-500/10 text-blue-700",
    caps: ["HOLEPUNCH", "METRICS"],
    dimmed: true,
  },
  {
    name: "iPad Air",
    icon: "tablet_mac",
    status: "LIVE",
    statusColor: "bg-green-500/10 text-green-700",
    caps: ["FS_SYNC"],
  },
];

export default function Network() {
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
          <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[160px]">
            <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
              Node ID
            </span>
            <span className="font-mono text-sm text-primary">
              sky10-7a2f...f92d
            </span>
          </div>
          <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[120px]">
            <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
              Mode
            </span>
            <div className="flex items-center gap-2">
              <span className="w-2 h-2 bg-primary rounded-full" />
              <span className="font-semibold text-sm">Network</span>
            </div>
          </div>
          <div className="px-6 py-4 bg-surface-container-low rounded-xl flex flex-col gap-1 min-w-[120px]">
            <span className="text-[10px] font-bold text-secondary uppercase tracking-widest">
              Uptime
            </span>
            <span className="font-semibold text-sm">14d 2h 12m</span>
          </div>
        </div>
      </section>

      <div className="grid grid-cols-12 gap-8">
        {/* Connection graph */}
        <div className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 min-h-[500px] relative overflow-hidden flex items-center justify-center border border-outline-variant/10 shadow-sm">
          <div className="absolute inset-0 bg-[radial-gradient(#007AFF10_1px,transparent_1px)] [background-size:24px_24px]" />
          <div className="relative w-full h-full flex items-center justify-center">
            {/* SVG lines */}
            <svg className="absolute inset-0 w-full h-full">
              <line
                x1="50%"
                y1="50%"
                x2="20%"
                y2="25%"
                stroke="#007AFF40"
                strokeWidth="2"
                strokeDasharray="4 4"
              />
              <line
                x1="50%"
                y1="50%"
                x2="80%"
                y2="30%"
                stroke="#10b981"
                strokeWidth="2"
              />
              <line
                x1="50%"
                y1="50%"
                x2="75%"
                y2="75%"
                stroke="#10b981"
                strokeWidth="2"
              />
              <line
                x1="50%"
                y1="50%"
                x2="25%"
                y2="80%"
                stroke="#f59e0b"
                strokeWidth="2"
                strokeDasharray="4 4"
              />
            </svg>
            {/* Central node */}
            <div className="relative z-10 w-24 h-24 rounded-full glass-effect border-4 border-primary/20 flex items-center justify-center shadow-2xl">
              <div className="flex flex-col items-center">
                <Icon name="token" className="text-primary text-3xl" />
                <span className="text-[10px] font-bold mt-1 uppercase text-primary">
                  Main Node
                </span>
              </div>
            </div>
            {/* Peer nodes */}
            <div className="absolute top-[15%] left-[15%] flex flex-col items-center gap-2">
              <div className="w-12 h-12 rounded-full bg-white shadow-lg border border-outline-variant/20 flex items-center justify-center">
                <Icon name="laptop_mac" className="text-secondary" />
              </div>
              <div className="px-2 py-1 bg-surface-container-high rounded-full text-[10px] font-medium whitespace-nowrap">
                MacBook Pro
              </div>
            </div>
            <div className="absolute top-[20%] right-[15%] flex flex-col items-center gap-2">
              <div className="w-14 h-14 rounded-full bg-white shadow-lg border border-green-500/30 flex items-center justify-center">
                <Icon name="dns" className="text-secondary" />
              </div>
              <div className="px-2 py-1 bg-surface-container-high rounded-full text-[10px] font-medium whitespace-nowrap">
                Frankfurt Relay
              </div>
            </div>
            <div className="absolute bottom-[15%] right-[20%] flex flex-col items-center gap-2">
              <div className="w-12 h-12 rounded-full bg-white shadow-lg border border-green-500/30 flex items-center justify-center">
                <Icon name="tablet_mac" className="text-secondary" />
              </div>
              <div className="px-2 py-1 bg-surface-container-high rounded-full text-[10px] font-medium whitespace-nowrap">
                iPad Air
              </div>
            </div>
            <div className="absolute bottom-[10%] left-[20%] flex flex-col items-center gap-2">
              <div className="w-12 h-12 rounded-full bg-white shadow-lg border border-amber-500/30 flex items-center justify-center">
                <Icon name="storage" className="text-secondary" />
              </div>
              <div className="px-2 py-1 bg-surface-container-high rounded-full text-[10px] font-medium whitespace-nowrap">
                S3-Storage (Relay)
              </div>
            </div>
          </div>
          {/* Legend */}
          <div className="absolute bottom-6 left-6 space-y-2 glass-effect p-4 rounded-xl border border-outline-variant/10">
            <div className="flex items-center gap-3">
              <span className="w-3 h-0.5 bg-[#10b981]" />
              <span className="text-xs text-secondary font-medium uppercase tracking-wider">
                Direct (0.4ms)
              </span>
            </div>
            <div className="flex items-center gap-3">
              <span className="w-3 h-0.5 bg-[#f59e0b]" />
              <span className="text-xs text-secondary font-medium uppercase tracking-wider">
                Relayed (48ms)
              </span>
            </div>
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
                <h3 className="font-bold text-2xl">4 Active</h3>
                <p className="text-xs text-secondary font-medium uppercase tracking-widest">
                  Connected Peers
                </p>
              </div>
            </div>
          </div>
          <div className="bg-surface-container-lowest rounded-xl p-6 border border-outline-variant/10 shadow-sm flex-1 space-y-6">
            <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest mb-4">
              Active Peer Details
            </h4>
            <div className="space-y-8">
              {peers.map((peer) => (
                <div
                  key={peer.name}
                  className={`flex items-start gap-4 ${peer.dimmed ? "opacity-80" : ""}`}
                >
                  <div className="w-8 h-8 rounded-lg bg-surface-container-high flex items-center justify-center shrink-0">
                    <Icon
                      name={peer.icon}
                      className="text-sm text-on-surface-variant"
                    />
                  </div>
                  <div className="flex-1 space-y-2">
                    <div className="flex justify-between items-center">
                      <span className="text-sm font-bold">{peer.name}</span>
                      <span
                        className={`text-[10px] px-1.5 py-0.5 rounded font-bold ${peer.statusColor}`}
                      >
                        {peer.status}
                      </span>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {peer.caps.map((cap) => (
                        <span
                          key={cap}
                          className="text-[9px] px-2 py-0.5 bg-surface-container-high rounded font-mono uppercase"
                        >
                          {cap}
                        </span>
                      ))}
                    </div>
                  </div>
                </div>
              ))}
            </div>
            <button className="w-full py-3 text-sm font-semibold text-primary border border-primary/20 rounded-lg hover:bg-primary/5 transition-colors mt-4">
              Scan for nearby peers
            </button>
          </div>
        </div>

        {/* Activity feed */}
        <div className="col-span-12">
          <div className="bg-surface-container-lowest rounded-xl p-8 border border-outline-variant/10 shadow-sm">
            <div className="flex justify-between items-center mb-8">
              <h4 className="text-sm font-bold text-on-surface uppercase tracking-widest">
                Network Activity Log
              </h4>
              <div className="flex items-center gap-2 px-3 py-1 bg-surface-container rounded-lg">
                <span className="w-1.5 h-1.5 bg-green-500 rounded-full animate-pulse" />
                <span className="text-[10px] font-mono text-secondary">
                  REAL-TIME MONITORING
                </span>
              </div>
            </div>
            <div className="space-y-4 font-mono text-[13px]">
              {activityLog.map((entry, i) => (
                <div
                  key={i}
                  className="flex items-center gap-4 py-2 border-b border-outline-variant/5"
                >
                  <span className="text-secondary/50 shrink-0">
                    {entry.time}
                  </span>
                  <span
                    className={`font-bold px-1.5 py-0.5 rounded uppercase text-[10px] ${entry.levelColor}`}
                  >
                    {entry.level}
                  </span>
                  <span className="text-on-surface">
                    {entry.message}{" "}
                    {entry.highlight && (
                      <strong className="text-primary">
                        {entry.highlight}
                      </strong>
                    )}{" "}
                    {entry.detail}{" "}
                    {entry.highlight2 && (
                      <strong className="text-on-surface-variant">
                        {entry.highlight2}
                      </strong>
                    )}
                  </span>
                  <span className="ml-auto text-secondary/40">
                    {entry.meta}
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
