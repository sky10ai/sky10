import {
  detectIntent,
  type AgentAudience,
  type AssistantIntent,
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
  disabledRootAssistantRPCs,
  rootAssistantApprovalRequiredToolNames,
  rootAssistantReadOnlyToolNames,
  rootAssistantToolMetadata,
  rootAssistantToolRunners,
  type RootAssistantToolName,
} from "./rootAssistantTools";
import { codex } from "./rpc";
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
  intent?: AssistantIntent;
}

const PLANNABLE_INTENTS: AssistantIntent[] = [
  "agent_create",
  "agents",
  "configuration",
  "daemon_version",
  "devices",
  "drives",
  "fallback",
  "network",
  "node_diagnosis",
  "sandboxes",
  "sync_activity",
];

function plannerToolSummary(names: readonly RootAssistantToolName[]) {
  return names
    .map((name) => {
      const metadata = rootAssistantToolMetadata[name];
      return `- ${name}: ${metadata.title} (${metadata.rpcMethods.join(", ")})`;
    })
    .join("\n");
}

function parsePlannerIntent(text: string): AssistantIntent | null {
  const trimmed = text.trim();
  const match = trimmed.match(/\{[\s\S]*\}/);
  if (!match) return null;

  try {
    const parsed = JSON.parse(match[0]) as { intent?: unknown };
    const intent = typeof parsed.intent === "string" ? parsed.intent : "";
    return PLANNABLE_INTENTS.includes(intent as AssistantIntent)
      ? (intent as AssistantIntent)
      : null;
  } catch {
    return null;
  }
}

async function planIntentWithModel(
  prompt: string,
  hooks: RootAssistantHooks,
): Promise<AssistantIntent | null> {
  hooks.onStatus?.("Asking the model to choose a sky10 tool path.");

  try {
    const result = await codex.chat({
      model: "gpt-5.5",
      reasoning_effort: "low",
      system_prompt: [
        "You are the sky10 assistant planner.",
        "Classify the user's request into exactly one intent.",
        "Use daemon_version for any request about the sky10, app, CLI, binary, build, or daemon version, even if the user has typos.",
        "Use configuration for setup, install, create, delete, update, secret, wallet, device invite/join/approve/remove, sandbox lifecycle, drive lifecycle, or file mutation requests.",
        "Use node_diagnosis for health, status summary, degraded, broken, or needs-attention requests.",
        "Return only compact JSON in this shape: {\"intent\":\"daemon_version\"}.",
      ].join("\n"),
      messages: [
        {
          role: "user",
          content: [
            `Request: ${prompt}`,
            "",
            "Read-only AI SDK tools:",
            plannerToolSummary(rootAssistantReadOnlyToolNames),
            "",
            "Approval-gated AI SDK tools:",
            plannerToolSummary(rootAssistantApprovalRequiredToolNames),
            "",
            `Disabled RPCs: ${disabledRootAssistantRPCs.join(", ")}`,
          ].join("\n"),
        },
      ],
    });
    return parsePlannerIntent(result.text);
  } catch {
    return null;
  }
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
    "system.health",
    "Read daemon health",
    "Inspecting storage health, queue depth, and runtime counters.",
    () => rootAssistantToolRunners.daemon_getHealth(),
    summarizeHealth
  );
  const network = await recordTool(
    hooks,
    "network.getStatus",
    "skylink.status",
    "Read network status",
    "Checking peer connectivity and delivery health.",
    () => rootAssistantToolRunners.network_getStatus(),
    summarizeNetwork
  );
  const activity = await recordTool(
    hooks,
    "activity.list",
    "skyfs.syncActivity",
    "Read sync activity",
    "Inspecting pending transfers, conflicts, and path issues.",
    () => rootAssistantToolRunners.sync_activity(),
    (result) => `${result.pending.length} pending · ${result.conflicts.length} conflicts · ${result.path_issues.length} path issues`
  );
  const sandboxes = await recordTool(
    hooks,
    "sandboxes.list",
    "sandbox.list",
    "List sandboxes",
    "Checking managed runtimes for provisioning errors or busy state.",
    () => rootAssistantToolRunners.sandboxes_list(),
    summarizeSandboxes
  );
  const agents = await recordTool(
    hooks,
    "agents.list",
    "agent.list",
    "List agents",
    "Reviewing registered agent presence.",
    () => rootAssistantToolRunners.agents_list(),
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
  hooks.onStatus?.("Reading...");
  const [agents, sandboxes] = await Promise.all([
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Checking what agents already exist.",
      () => rootAssistantToolRunners.agents_list(),
      summarizeAgents
    ),
    recordTool(
      hooks,
      "sandboxes.list",
      "sandbox.list",
      "List sandboxes",
      "Checking available managed runtimes.",
      () => rootAssistantToolRunners.sandboxes_list(),
      summarizeSandboxes
    ),
  ]);

  const serviceAgent = audience === "for_others" || soundsCommercial(prompt);
  const answer = await streamParagraphs(
    hooks,
    serviceAgent
      ? [
          `Agent draft: ${prompt}.`,
          "Define the offer, inputs, runtime, billing, data boundaries, and isolation before serving outside users.",
          `Inventory: ${agents.count} registered agent${agents.count === 1 ? "" : "s"} · ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"}.`,
        ]
      : [
          `Agent draft: ${prompt}.`,
          "Define the trigger, inputs, outputs, data access, runtime, and provider keys before provisioning.",
          `Inventory: ${agents.count} registered agent${agents.count === 1 ? "" : "s"} · ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"}.`,
        ]
  );

  return {
    answer,
    followUps:
      serviceAgent
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

async function runConfigurationPrompt(
  prompt: string,
  hooks: RootAssistantHooks
): Promise<RootAssistantResult> {
  hooks.onStatus?.("Checking live RPC state and assistant policy boundaries.");
  const [health, drives, devices, sandboxes, agents] = await Promise.all([
    recordTool(
      hooks,
      "daemon.getHealth",
      "system.health",
      "Read daemon health",
      "Checking current daemon and storage state.",
      () => rootAssistantToolRunners.daemon_getHealth(),
      summarizeHealth
    ),
    recordTool(
      hooks,
      "drives.list",
      "skyfs.driveList",
      "List drives",
      "Reading configured drive inventory before planning changes.",
      () => rootAssistantToolRunners.drives_list(),
      (result) => `${result.drives.length} drive${result.drives.length === 1 ? "" : "s"} configured`
    ),
    recordTool(
      hooks,
      "devices.list",
      "identity.deviceList",
      "List devices",
      "Reading current device membership.",
      () => rootAssistantToolRunners.devices_list(),
      summarizeDevices
    ),
    recordTool(
      hooks,
      "sandboxes.list",
      "sandbox.list",
      "List sandboxes",
      "Reading managed runtime inventory.",
      () => rootAssistantToolRunners.sandboxes_list(),
      summarizeSandboxes
    ),
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Reading registered agent inventory.",
      () => rootAssistantToolRunners.agents_list(),
      summarizeAgents
    ),
  ]);

  const answer = await streamParagraphs(hooks, [
    `I would treat \`${prompt}\` as an approval-gated RPC configuration request, not as a separate settings-page workflow.`,
    `Current context: ${summarizeHealth(health)} · ${drives.drives.length} drive${drives.drives.length === 1 ? "" : "s"} · ${devices.devices.length} device${devices.devices.length === 1 ? "" : "s"} · ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"} · ${agents.count} agent${agents.count === 1 ? "" : "s"}.`,
    "The AI-first contract is that user-configurable RPC surfaces should be model-addressable through curated tools. Mutating operations need a visible plan, exact parameters, and approval before execution.",
    `This build has ${rootAssistantApprovalRequiredToolNames.length} approval-gated AI SDK tool wrappers for RPC writes such as drives, secrets, sandboxes, apps, updates, device invites, and wallet setup. The next step is wiring approval cards to execute those tools after user confirmation.`,
  ]);

  return {
    answer,
    followUps: [
      "Create a drive for agent outputs.",
      "Store a provider API key for trusted devices.",
      "Create a sandbox for a local coding agent.",
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
      "system.health",
      "Read daemon health",
      "Checking the daemon health snapshot.",
      () => rootAssistantToolRunners.daemon_getHealth(),
      summarizeHealth
    ),
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Reviewing the current agent registry.",
      () => rootAssistantToolRunners.agents_list(),
      summarizeAgents
    ),
    recordTool(
      hooks,
      "devices.list",
      "identity.deviceList",
      "List devices",
      "Reviewing current device membership.",
      () => rootAssistantToolRunners.devices_list(),
      summarizeDevices
    ),
    recordTool(
      hooks,
      "network.getStatus",
      "skylink.status",
      "Read network status",
      "Inspecting peer connectivity.",
      () => rootAssistantToolRunners.network_getStatus(),
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
  const intent =
    options.intent ??
    (await planIntentWithModel(prompt, hooks)) ??
    detectIntent(prompt);
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
    case "configuration":
      return runConfigurationPrompt(prompt, hooks);
    default:
      return runFallback(prompt, hooks);
  }
}
