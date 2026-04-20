import { agent, identity, sandbox, skyfs, skylink } from "./rpc";
import {
  detectIntent,
  type AgentAudience,
  type RootAssistantHooks,
  type RootAssistantResult,
  recordTool,
  streamParagraphs,
  summarizeAgents,
  summarizeDevices,
  summarizeHealth,
  summarizeNetwork,
  summarizeSandboxes,
} from "./rootAssistantShared";
import {
  runAgents,
  runDaemonVersion,
  runDevices,
  runDrives,
  runNetwork,
  runSandboxes,
  runSyncActivity,
} from "./rootAssistantQueries";

export type {
  AgentAudience,
  RootAssistantHooks,
  RootAssistantResult,
  RootAssistantStatus,
  RootAssistantToolTrace,
} from "./rootAssistantShared";

interface ExecuteOptions {
  audience?: AgentAudience;
}

function soundsCommercial(prompt: string) {
  const value = prompt.toLowerCase();
  return (
    value.includes("for others") ||
    value.includes("for clients") ||
    value.includes("for customers") ||
    value.includes("sell") ||
    value.includes("charge") ||
    value.includes("paid") ||
    value.includes("offer")
  );
}

async function runNodeDiagnosis(hooks: RootAssistantHooks): Promise<RootAssistantResult> {
  hooks.onStatus?.("Scanning storage, network, agents, sandboxes, and activity.");
  const health = await recordTool(
    hooks,
    "daemon.getHealth",
    "skyfs.health",
    "Read daemon health",
    "Inspecting storage health, queue depth, and runtime counters.",
    () => skyfs.health(),
    summarizeHealth
  );
  const network = await recordTool(
    hooks,
    "network.getStatus",
    "skylink.status",
    "Read network status",
    "Checking peer connectivity and delivery health.",
    () => skylink.status(),
    summarizeNetwork
  );
  const activity = await recordTool(
    hooks,
    "activity.list",
    "skyfs.syncActivity",
    "Read sync activity",
    "Inspecting pending transfers, conflicts, and path issues.",
    () => skyfs.syncActivity(),
    (result) => `${result.pending.length} pending · ${result.conflicts.length} conflicts · ${result.path_issues.length} path issues`
  );
  const sandboxes = await recordTool(
    hooks,
    "sandboxes.list",
    "sandbox.list",
    "List sandboxes",
    "Checking managed runtimes for provisioning errors or busy state.",
    () => sandbox.list(),
    summarizeSandboxes
  );
  const agents = await recordTool(
    hooks,
    "agents.list",
    "agent.list",
    "List agents",
    "Reviewing registered agent presence.",
    () => agent.list(),
    summarizeAgents
  );

  const issues: string[] = [];
  if (health.sync_error_drives > 0) {
    issues.push(`${health.sync_error_drives} drive${health.sync_error_drives === 1 ? "" : "s"} are degraded`);
  }
  if (health.path_issue_drives > 0) {
    issues.push(`${health.path_issue_drives} drive${health.path_issue_drives === 1 ? "" : "s"} have path issues`);
  }
  if (activity.conflicts.length > 0) {
    issues.push(`${activity.conflicts.length} active conflict${activity.conflicts.length === 1 ? "" : "s"} need review`);
  }
  if (network.peers === 0) {
    issues.push("the node has no connected peers");
  }
  if (network.health.transport_degraded_reason) {
    issues.push(`transport is degraded (${network.health.transport_degraded_reason.replaceAll("_", " ")})`);
  }
  if (network.health.delivery_degraded_reason) {
    issues.push(`delivery is degraded (${network.health.delivery_degraded_reason.replaceAll("_", " ")})`);
  }
  const failingSandboxes = sandboxes.sandboxes.filter((item) =>
    item.status.includes("error") || item.status.includes("failed")
  );
  if (failingSandboxes.length > 0) {
    issues.push(`${failingSandboxes.length} sandbox${failingSandboxes.length === 1 ? "" : "es"} need attention`);
  }

  const healthySummary = [
    `${health.drives_running}/${health.drives} drives live`,
    `${network.peers} peer${network.peers === 1 ? "" : "s"} connected`,
    `${agents.count} agent${agents.count === 1 ? "" : "s"} registered`,
  ].join(" · ");

  const lines: string[] = [
    issues.length > 0
      ? `The main issues I found are: ${issues.join("; ")}.`
      : `I do not see a major fault right now. The node looks steady: ${healthySummary}.`,
    `Storage snapshot: ${summarizeHealth(health)}.`,
    `Network snapshot: ${summarizeNetwork(network)}.`,
  ];

  if (activity.conflicts.length > 0 || activity.path_issues.length > 0) {
    const activityLines: string[] = [];
    if (activity.conflicts.length > 0) {
      activityLines.push(
        ...activity.conflicts.slice(0, 3).map((item) => `• Conflict in ${item.drive_name}: ${item.path}`)
      );
    }
    if (activity.path_issues.length > 0) {
      activityLines.push(
        ...activity.path_issues.slice(0, 3).map((item) => `• Path issue in ${item.drive_name}: ${item.reason}`)
      );
    }
    lines.push(activityLines.join("\n"));
  }

  if (failingSandboxes.length > 0) {
    lines.push(
      failingSandboxes
        .slice(0, 3)
        .map((item) => `• Sandbox ${item.name} is ${item.status}${item.last_error ? ` — ${item.last_error}` : ""}`)
        .join("\n")
    );
  }

  const answer = await streamParagraphs(hooks, lines);
  return {
    answer,
    followUps: ["Which drives or files need attention?", "Check whether my network is healthy.", "Show me my agents and where they run."],
    status: "complete",
  };
}

async function runAgentCreatePrompt(
  prompt: string,
  hooks: RootAssistantHooks,
  audience: AgentAudience
): Promise<RootAssistantResult> {
  hooks.onStatus?.("Reading current agent and sandbox inventory before drafting the next step.");
  const [agents, sandboxes] = await Promise.all([
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Checking what agents already exist.",
      () => agent.list(),
      summarizeAgents
    ),
    recordTool(
      hooks,
      "sandboxes.list",
      "sandbox.list",
      "List sandboxes",
      "Checking available managed runtimes.",
      () => sandbox.list(),
      summarizeSandboxes
    ),
  ]);

  const mode = audience === "for_others" || soundsCommercial(prompt) ? "for_others" : "for_me";
  const answer = await streamParagraphs(
    hooks,
    mode === "for_others"
      ? [
          `This reads like a \`For others\` agent: \`${prompt}\`.`,
          "The next step should be a service-agent draft covering what the agent offers, who can use it, what it costs to run, how it gets paid, what data may leave the machine, and what isolation level it needs before serving outside users.",
          `This Milestone 2 slice is still read-only, so I cannot publish or provision that agent yet. I can only show the live registry and prove the planning surface. Current inventory: ${agents.count} registered agent${agents.count === 1 ? "" : "s"} and ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"} available for future runtime placement.`,
        ]
      : [
          `This reads like a \`For me\` agent: \`${prompt}\`.`,
          "The next step should be a private-agent draft covering what folders or data it can access, what outputs it should create, whether it runs once or watches continuously, and what tools or providers it needs.",
          `This Milestone 2 slice is still read-only, so I cannot provision that agent yet. I can only show the live registry and prove the planning surface. Current inventory: ${agents.count} registered agent${agents.count === 1 ? "" : "s"} and ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"} available for future runtime placement.`,
        ]
  );

  return {
    answer,
    followUps:
      mode === "for_others"
        ? [
            "Create an agent that transcribes podcasts and charges per upload.",
            "Show me my agents and where they run.",
            "Tell me what needs attention right now.",
          ]
        : [
            "Create an agent that watches a folder and summarizes meeting recordings.",
            "Show me my agents and where they run.",
            "Tell me what needs attention right now.",
          ],
    status: "complete",
  };
}

async function runFallback(prompt: string, hooks: RootAssistantHooks): Promise<RootAssistantResult> {
  hooks.onStatus?.("Building a quick read-only overview of the node.");
  const [health, agents, devices, network] = await Promise.all([
    recordTool(
      hooks,
      "daemon.getHealth",
      "skyfs.health",
      "Read daemon health",
      "Checking the daemon health snapshot.",
      () => skyfs.health(),
      summarizeHealth
    ),
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Reviewing the current agent registry.",
      () => agent.list(),
      summarizeAgents
    ),
    recordTool(
      hooks,
      "devices.list",
      "identity.deviceList",
      "List devices",
      "Reviewing current device membership.",
      () => identity.deviceList(),
      summarizeDevices
    ),
    recordTool(
      hooks,
      "network.getStatus",
      "skylink.status",
      "Read network status",
      "Inspecting peer connectivity.",
      () => skylink.status(),
      summarizeNetwork
    ),
  ]);

  const answer = await streamParagraphs(hooks, [
    `I do not have a specialized read-only tool flow for \`${prompt}\` yet, so I pulled a quick overview instead.`,
    `Current snapshot: ${summarizeHealth(health)} · ${agents.count} agent${agents.count === 1 ? "" : "s"} · ${devices.devices.length} device${devices.devices.length === 1 ? "" : "s"} · ${network.peers} connected peer${network.peers === 1 ? "" : "s"}.`,
    "This MVP currently handles version checks, node diagnosis, drives, devices, agents, network, sandboxes, and sync activity. Provisioning and richer planning come next.",
  ]);

  return {
    answer,
    followUps: ["Tell me what needs attention right now.", "Which drives or files need attention?", "Check whether my network is healthy."],
    status: "complete",
  };
}

export async function executeRootAssistantPrompt(
  prompt: string,
  hooks: RootAssistantHooks = {},
  options: ExecuteOptions = {}
): Promise<RootAssistantResult> {
  const intent = detectIntent(prompt);
  const audience = options.audience ?? "for_me";

  switch (intent) {
    case "daemon_version":
      return runDaemonVersion(hooks);
    case "drives":
      return runDrives(hooks);
    case "devices":
      return runDevices(hooks);
    case "agents":
      return runAgents(hooks);
    case "sandboxes":
      return runSandboxes(hooks);
    case "network":
      return runNetwork(hooks);
    case "sync_activity":
      return runSyncActivity(hooks);
    case "node_diagnosis":
      return runNodeDiagnosis(hooks);
    case "agent_create":
      return runAgentCreatePrompt(prompt, hooks, audience);
    default:
      return runFallback(prompt, hooks);
  }
}
