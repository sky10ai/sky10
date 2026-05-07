import type { DriveListResult } from "./rpc";
import {
  type RootAgentHooks,
  type RootAgentResult,
  recordTool,
  streamParagraphs,
  summarizeActivity,
  summarizeAgents,
  summarizeDevices,
  summarizeHealth,
  summarizeNetwork,
  summarizeSandboxes,
} from "./rootAgentShared";
import { rootAgentToolRunners } from "./rootAgentTools";

export async function runDaemonVersion(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Checking the daemon build and node health.");
  const health = await recordTool(
    hooks,
    "daemon.getVersion",
    "system.health",
    "Read daemon version",
    "Fetching the daemon build string and current health snapshot.",
    () => rootAgentToolRunners.daemon_getHealth(),
    (result) => `version ${result.version} · ${result.uptime} uptime`
  );

  const answer = await streamParagraphs(hooks, [
    `This node is running \`${health.version}\`.`,
    `The daemon has been up for ${health.uptime} and is currently serving ${health.drives_running} of ${health.drives} configured drives.`,
    health.http_addr
      ? `HTTP RPC is exposed at \`${health.http_addr}\`, so the assistant is reading live daemon state rather than a cached build label.`
      : "The assistant is reading the live health endpoint through the existing RPC surface.",
  ]);

  return {
    answer,
    followUps: ["Tell me what needs attention right now.", "Which drives or files need attention?", "Check whether my network is healthy."],
    status: "complete",
  };
}

export async function runDrives(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Inspecting drives and sync health.");
  const [drives, health] = await Promise.all([
    recordTool(
      hooks,
      "drives.list",
      "skyfs.driveList",
      "List drives",
      "Reading configured drive inventory.",
      () => rootAgentToolRunners.drives_list(),
      (result: DriveListResult) => `${result.drives.length} drive${result.drives.length === 1 ? "" : "s"} found`
    ),
    recordTool(
      hooks,
      "daemon.getHealth",
      "system.health",
      "Read storage health",
      "Checking drive runtime and sync counters.",
      () => rootAgentToolRunners.daemon_getHealth(),
      summarizeHealth
    ),
  ]);

  const lines: string[] = [
    drives.drives.length === 0
      ? "There are no configured drives on this node yet."
      : `I found ${drives.drives.length} drive${drives.drives.length === 1 ? "" : "s"} and ${health.drives_running} currently running daemon${health.drives_running === 1 ? "" : "s"}.`,
  ];

  if (drives.drives.length > 0) {
    lines.push(
      drives.drives
        .slice(0, 4)
        .map((drive) => {
          const flags: string[] = [];
          if (drive.sync_state && drive.sync_state !== "ok") flags.push(drive.sync_state);
          if ((drive.conflict_files ?? 0) > 0) flags.push(`${drive.conflict_files} conflict${drive.conflict_files === 1 ? "" : "s"}`);
          if ((drive.path_issue_count ?? 0) > 0) flags.push(`${drive.path_issue_count} path issue${drive.path_issue_count === 1 ? "" : "s"}`);
          return `• ${drive.name} — ${drive.snapshot_files} files, ${drive.outbox_pending} queued${flags.length > 0 ? `, ${flags.join(", ")}` : ""}`;
        })
        .join("\n")
    );
  }

  lines.push(`Overall storage snapshot: ${summarizeHealth(health)}.`);

  const answer = await streamParagraphs(hooks, lines);
  return {
    answer,
    followUps: ["Which drives or files need attention?", "Tell me what needs attention right now.", "Show me my agents and where they run."],
    status: "complete",
  };
}

export async function runDevices(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Checking device membership and last-seen data.");
  const devices = await recordTool(
    hooks,
    "devices.list",
    "identity.deviceList",
    "List devices",
    "Reading identity device membership and metadata.",
    () => rootAgentToolRunners.devices_list(),
    summarizeDevices
  );

  const current = devices.devices.find((device) => device.current);
  const lines: string[] = [
    `I found ${devices.devices.length} device${devices.devices.length === 1 ? "" : "s"} in this network.`,
  ];

  if (current) {
    lines.push(
      `This machine is \`${current.name}\`${current.platform ? ` on ${current.platform}` : ""}${current.version ? ` running ${current.version}` : ""}.`
    );
  }

  const peers = devices.devices
    .filter((device) => !device.current)
    .slice(0, 4)
    .map((device) => `• ${device.name}${device.platform ? ` — ${device.platform}` : ""}${device.version ? ` · ${device.version}` : ""}`)
    .join("\n");
  if (peers) {
    lines.push(peers);
  }

  const answer = await streamParagraphs(hooks, lines);
  return {
    answer,
    followUps: ["Tell me what needs attention right now.", "Check whether my network is healthy.", "Show me my agents and where they run."],
    status: "complete",
  };
}

export async function runAgents(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Inspecting registered agents.");
  const agents = await recordTool(
    hooks,
    "agents.list",
    "agent.list",
    "List agents",
    "Reading the live agent registry.",
    () => rootAgentToolRunners.agents_list(),
    summarizeAgents
  );

  const lines: string[] = [
    agents.count === 0
      ? "There are no registered agents yet."
      : `I found ${agents.count} registered agent${agents.count === 1 ? "" : "s"} across your current node set.`,
  ];

  if (agents.count > 0) {
    lines.push(
      agents.agents
        .slice(0, 5)
        .map((item) => `• ${item.name} — ${item.device_name} (${item.device_id})`)
        .join("\n")
    );
  }

  lines.push("Milestone 2 is still read-only, so I can inspect this registry but not provision a new managed agent yet.");

  const answer = await streamParagraphs(hooks, lines);
  return {
    answer,
    followUps: ["Create me an agent that watches a folder and processes new media.", "Tell me what needs attention right now.", "Check whether my network is healthy."],
    status: "complete",
  };
}

export async function runSandboxes(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Reading sandbox inventory.");
  const sandboxes = await recordTool(
    hooks,
    "sandboxes.list",
    "sandbox.list",
    "List sandboxes",
    "Inspecting managed runtime inventory.",
    () => rootAgentToolRunners.sandboxes_list(),
    summarizeSandboxes
  );

  const lines: string[] = [
    sandboxes.sandboxes.length === 0
      ? "There are no managed sandboxes provisioned on this machine."
      : `I found ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"}.`,
  ];

  if (sandboxes.sandboxes.length > 0) {
    lines.push(
      sandboxes.sandboxes
        .slice(0, 5)
        .map((item) => `• ${item.name} — ${item.status}${item.progress?.summary ? ` · ${item.progress.summary}` : ""}`)
        .join("\n")
    );
  }

  const answer = await streamParagraphs(hooks, lines);
  return {
    answer,
    followUps: ["Show me my agents and where they run.", "Tell me what needs attention right now.", "Create me an agent that watches a folder and processes new media."],
    status: "complete",
  };
}

export async function runNetwork(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Inspecting peer connectivity and delivery health.");
  const network = await recordTool(
    hooks,
    "network.getStatus",
    "skylink.status",
    "Read network status",
    "Inspecting peer counts, delivery health, and published addresses.",
    () => rootAgentToolRunners.network_getStatus(),
    summarizeNetwork
  );

  const answer = await streamParagraphs(hooks, [
    `The node is in \`${network.mode}\` mode with ${network.peers} connected peer${network.peers === 1 ? "" : "s"}.`,
    network.health.transport_degraded_reason
      ? `Transport is degraded because \`${network.health.transport_degraded_reason}\`.`
      : "Transport health looks normal right now.",
    network.health.delivery_degraded_reason
      ? `Delivery is degraded because \`${network.health.delivery_degraded_reason}\`.`
      : "Delivery looks normal right now.",
    `Primary address: \`${network.address}\`.`,
  ]);

  return {
    answer,
    followUps: ["Check whether my network is healthy.", "Tell me what needs attention right now.", "Which drives or files need attention?"],
    status: "complete",
  };
}

export async function runSyncActivity(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Reviewing sync activity, conflicts, and path issues.");
  const activity = await recordTool(
    hooks,
    "activity.list",
    "skyfs.syncActivity",
    "Read sync activity",
    "Inspecting pending transfers, conflicts, and path normalization issues.",
    () => rootAgentToolRunners.sync_activity(),
    summarizeActivity
  );

  const answer = await streamParagraphs(hooks, [
    `I found ${activity.pending.length} pending transfer${activity.pending.length === 1 ? "" : "s"}, ${activity.conflicts.length} conflict${activity.conflicts.length === 1 ? "" : "s"}, and ${activity.path_issues.length} path issue${activity.path_issues.length === 1 ? "" : "s"}.`,
    activity.conflicts.length > 0
      ? activity.conflicts.slice(0, 4).map((item) => `• Conflict in ${item.drive_name}: ${item.path}`).join("\n")
      : "There are no active file conflicts in the current activity snapshot.",
    activity.path_issues.length > 0
      ? activity.path_issues
          .slice(0, 3)
          .map((item) => `• ${item.drive_name}: ${item.reason}`)
          .join("\n")
      : "There are no active path normalization problems in the current snapshot.",
  ]);

  return {
    answer,
    followUps: ["Which drives or files need attention?", "Tell me what needs attention right now.", "Check whether my network is healthy."],
    status: "complete",
  };
}
