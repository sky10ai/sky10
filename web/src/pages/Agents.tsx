import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES } from "../lib/events";
import { agent } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function Agents() {
  const navigate = useNavigate();
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
      "skills": ["code", "test"]
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
      {error && (
        <div className="mb-8 p-4 bg-error-container/20 text-error rounded-xl text-sm">
          {error}
        </div>
      )}

      {/* My Agents */}
      <div className="mb-8">
        <h1 className="text-4xl font-bold tracking-tight text-on-surface mb-2">
          My Agents
        </h1>
        <p className="text-secondary font-medium">
          {agents.length} agent{agents.length !== 1 ? "s" : ""} across{" "}
          {deviceSet.size} device{deviceSet.size !== 1 ? "s" : ""}
        </p>
      </div>

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

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6 mb-16">
        {agents.map((a) => (
          <div
            key={`${a.device_id}-${a.id}`}
            onClick={() => navigate(`/agents/${a.id}`)}
            className="rounded-xl p-6 shadow-sm hover:shadow-xl transition-all duration-500 bg-surface-container-lowest ring-1 ring-outline-variant/10 cursor-pointer active:scale-[0.98]"
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
              {a.skills && a.skills.length > 0 && (
                <div>
                  <label className="text-[10px] font-bold text-secondary uppercase tracking-widest block mb-1.5">
                    Skills
                  </label>
                  <div className="flex flex-wrap gap-1.5">
                    {a.skills.map((skill) => (
                      <span
                        key={skill}
                        className="bg-primary-fixed/20 text-primary text-[10px] font-semibold px-2 py-0.5 rounded-full"
                      >
                        {skill}
                      </span>
                    ))}
                  </div>
                </div>
              )}

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

      {/* sky10 Network Agents — placeholder */}
      <div className="border-t border-outline-variant/10 pt-8">
        <h2 className="text-2xl font-bold tracking-tight text-on-surface mb-2">
          sky10 Network
        </h2>
        <p className="text-secondary text-sm">
          Browse agents on the sky10 network. Coming soon.
        </p>
      </div>
    </div>
  );
}
