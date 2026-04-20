import { Link } from "react-router";
import { StatusBadge } from "../StatusBadge";
import type { AgentInfo, DeviceListResult, DriveListResult, HealthResult, LinkStatus, SandboxListResult } from "../../lib/rpc";
import { audienceLabel, type AgentAudience } from "./workspaceTypes";

interface WorkspaceSidebarProps {
  audience: AgentAudience;
  devices: DeviceListResult | null;
  drives: DriveListResult | null;
  health: HealthResult | null;
  linkStatus: LinkStatus | null;
  onPromptSelect: (value: string) => void;
  recentAgents: AgentInfo[];
  sandboxNeedsAttention: number;
  sandboxes: SandboxListResult | null;
  suggestions: readonly string[];
}

export function WorkspaceSidebar({
  audience,
  devices,
  drives,
  health,
  linkStatus,
  onPromptSelect,
  recentAgents,
  sandboxNeedsAttention,
  sandboxes,
  suggestions,
}: WorkspaceSidebarProps) {
  const pending = (health?.outbox_pending ?? 0) + (health?.transfer_pending ?? 0);

  return (
    <aside className="space-y-6">
      <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="flex items-center justify-between gap-3">
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Quick Context
          </p>
          <StatusBadge tone="neutral">Live</StatusBadge>
        </div>
        <div className="mt-4 space-y-3 text-sm text-secondary">
          <p>{health ? `${health.drives_running}/${health.drives} drives live · ${pending} pending operations` : "Waiting for daemon health"}</p>
          <p>{linkStatus ? `${linkStatus.peers} peers · ${linkStatus.mode} mode` : "Waiting for network status"}</p>
          <p>{devices ? `${devices.devices.length} total device${devices.devices.length === 1 ? "" : "s"}` : "Waiting for device inventory"}</p>
          <p>{drives ? `${drives.drives.length} configured drive${drives.drives.length === 1 ? "" : "s"}` : "Waiting for drive inventory"}</p>
          <p>{sandboxes ? `${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"} · ${sandboxNeedsAttention} need attention` : "Waiting for sandbox inventory"}</p>
          {recentAgents.length > 0 && (
            <div className="rounded-2xl border border-outline-variant/10 bg-surface px-4 py-3">
              <p className="font-semibold text-on-surface">Recent agent</p>
              <p className="mt-1">
                <Link
                  className="transition-colors hover:text-on-surface"
                  to={`/agents/${encodeURIComponent(recentAgents[0]!.id)}`}
                >
                  {recentAgents[0]!.name}
                </Link>
                {" "}
                on {recentAgents[0]!.device_name}
              </p>
            </div>
          )}
        </div>
      </section>

      <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="flex items-center justify-between gap-3">
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Starter Ideas
          </p>
          <StatusBadge tone="neutral">
            {audienceLabel(audience)}
          </StatusBadge>
        </div>
        <div className="mt-4 space-y-2">
          {suggestions.map((item) => (
            <button
              key={item}
              className="flex w-full items-start rounded-2xl border border-outline-variant/10 bg-surface px-4 py-3 text-left text-sm text-secondary transition-colors hover:border-primary/20 hover:text-on-surface"
              onClick={() => onPromptSelect(item)}
              type="button"
            >
              {item}
            </button>
          ))}
        </div>
      </section>
    </aside>
  );
}
