import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES } from "../lib/events";
import { agent } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function Agents() {
  const { data, loading, error } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  const agents = data?.agents ?? [];

  if (!loading && agents.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center p-8">
        <div className="text-center space-y-4 max-w-md">
          <Icon name="smart_toy" className="text-5xl text-secondary" />
          <h1 className="text-2xl font-bold text-on-surface">No Agents</h1>
          <p className="text-secondary">
            Register an agent by calling{" "}
            <code className="bg-surface-container-high px-1.5 py-0.5 rounded text-xs font-mono">
              agent.register
            </code>{" "}
            on the daemon RPC.
          </p>
          <pre className="text-left bg-surface-container-lowest rounded-xl p-4 text-xs font-mono text-on-surface-variant overflow-x-auto">
{`curl -X POST localhost:9101/rpc \\
  -H "Content-Type: application/json" \\
  -d '{
    "jsonrpc": "2.0",
    "method": "agent.register",
    "params": {
      "name": "my-agent",
      "endpoint": "http://localhost:8200/rpc",
      "capabilities": ["code", "test"]
    },
    "id": 1
  }'`}
          </pre>
        </div>
      </div>
    );
  }

  // Count unique devices hosting agents.
  const deviceSet = new Set(agents.map((a) => a.device_id));

  return (
    <div className="p-12 max-w-7xl mx-auto">
      <div className="flex justify-between items-end mb-12">
        <div>
          <h1 className="text-4xl font-bold tracking-tight text-on-surface mb-2">
            Agents
          </h1>
          <p className="text-secondary font-medium">
            {agents.length} agent{agents.length !== 1 ? "s" : ""} across{" "}
            {deviceSet.size} device{deviceSet.size !== 1 ? "s" : ""}
          </p>
        </div>
      </div>

      {error && (
        <div className="mb-8 p-4 bg-error-container/20 text-error rounded-xl text-sm">
          {error}
        </div>
      )}

      {loading && agents.length === 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {[1, 2].map((i) => (
            <div
              key={i}
              className="bg-surface-container-lowest p-6 rounded-xl h-[280px] animate-pulse"
            />
          ))}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {agents.map((a) => (
          <div
            key={`${a.device_id}-${a.id}`}
            className="rounded-xl p-6 shadow-sm hover:shadow-xl transition-all duration-500 bg-surface-container-lowest ring-1 ring-outline-variant/10"
          >
            <div className="flex items-center justify-between mb-3 h-5">
              <StatusBadge pulse tone="live">
                Connected
              </StatusBadge>
            </div>

            <div className="flex items-start gap-4 mb-6">
              <div className="w-14 h-14 rounded-2xl flex items-center justify-center bg-tertiary-fixed/30 text-tertiary">
                <Icon name="smart_toy" className="text-3xl" />
              </div>
              <div className="flex-1 min-w-0">
                <h3 className="text-xl font-bold text-on-surface truncate">
                  {a.name}
                </h3>
                <p className="text-xs text-secondary flex items-center gap-1">
                  <Icon name="dns" className="text-xs" />
                  {a.device_name}
                  <span className="text-outline">({a.device_id})</span>
                </p>
              </div>
            </div>

            <div className="space-y-4">
              <div>
                <label className="text-[10px] font-bold text-secondary uppercase tracking-widest block mb-1">
                  Agent ID
                </label>
                <div className="bg-surface-container-low px-3 py-2 rounded-lg font-mono text-xs text-on-surface-variant">
                  {a.id}
                </div>
              </div>

              {a.capabilities && a.capabilities.length > 0 && (
                <div>
                  <label className="text-[10px] font-bold text-secondary uppercase tracking-widest block mb-1.5">
                    Capabilities
                  </label>
                  <div className="flex flex-wrap gap-1.5">
                    {a.capabilities.map((cap) => (
                      <span
                        key={cap}
                        className="bg-primary-fixed/20 text-primary text-[10px] font-semibold px-2 py-0.5 rounded-full"
                      >
                        {cap}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              <div className="flex items-center justify-between text-xs py-2 border-b border-surface-container-high">
                <span className="text-secondary font-medium">Methods</span>
                <span className="text-on-surface font-semibold">
                  {a.methods?.length ?? 0}
                </span>
              </div>

              <div className="flex items-center justify-between text-xs py-2">
                <span className="text-secondary font-medium">Connected</span>
                <RelativeTime
                  className="font-semibold text-on-surface"
                  value={a.connected_at}
                />
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
