import {
  detectIntent,
  type AgentAudience,
  type AssistantIntent,
  type RootAgentHooks,
  type RootAgentResult,
  recordTool,
  streamParagraphs,
  summarizeAgents,
  summarizeDevices,
  summarizeHealth,
  summarizeNetwork,
  summarizeSandboxes,
} from "./rootAgentShared";
import {
  disabledRootAgentRPCs,
  rootAgentApprovalRequiredToolNames,
  rootAgentReadOnlyToolNames,
  rootAgentToolMetadata,
  rootAgentToolRunners,
  type RootAgentToolName,
} from "./rootAgentTools";
import { codex } from "./rpc";
import {
  runAgents,
  runDaemonVersion,
  runDevices,
  runDrives,
  runNetwork,
  runSandboxes,
  runSyncActivity,
} from "./rootAgentQueries";
import type {
  RootAgentPageContext,
  RootAgentScreenshotContext,
} from "./rootAgentContext";
import { formatRootAgentContextForPrompt } from "./rootAgentContext";

export type {
  AgentAudience,
  RootAgentHooks,
  RootAgentResult,
  RootAgentStatus,
  RootAgentToolTrace,
} from "./rootAgentShared";

interface ExecuteOptions {
  audience?: AgentAudience;
  pageContext?: RootAgentPageContext;
  intent?: AssistantIntent;
  screenshot?: RootAgentScreenshotContext | null;
}

const ROOT_AGENT_LIGHT_MODEL = "gpt-5.4-mini";

const PLANNABLE_INTENTS: AssistantIntent[] = [
  "agent_create",
  "agents",
  "configuration",
  "daemon_version",
  "devices",
  "drives",
  "fallback",
  "greeting",
  "network",
  "node_diagnosis",
  "page_context",
  "sandboxes",
  "sync_activity",
];

function plannerToolSummary(names: readonly RootAgentToolName[]) {
  return names
    .map((name) => {
      const metadata = rootAgentToolMetadata[name];
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

function parseAgentNameResponse(text: string) {
  const trimmed = text.trim();
  const match = trimmed.match(/\{[\s\S]*\}/);
  if (match) {
    try {
      const parsed = JSON.parse(match[0]) as { name?: unknown };
      if (typeof parsed.name === "string") {
        return normalizeGeneratedAgentName(parsed.name);
      }
    } catch {
      // Fall through to plain-text parsing.
    }
  }
  return normalizeGeneratedAgentName(trimmed.split(/\r?\n/)[0] ?? "");
}

function normalizeGeneratedAgentName(value: string) {
  const parts = value
    .trim()
    .toLowerCase()
    .match(/[a-z0-9]+/g)
    ?.slice(0, 5);
  if (!parts || parts.length === 0) return null;
  const slug = parts.join("-").slice(0, 64).replace(/-+$/g, "");
  return slug || null;
}

export async function generateAgentNameFromPrompt(
  prompt: string,
  existingNames: readonly string[] = [],
  hooks: Pick<RootAgentHooks, "onStatus"> = {},
): Promise<string | null> {
  const trimmed = prompt.trim();
  if (!trimmed) return null;

  hooks.onStatus?.("Naming agent...");

  let generated: string | null = null;
  try {
    const result = await codex.chat({
      model: ROOT_AGENT_LIGHT_MODEL,
      reasoning_effort: "minimal",
      system_prompt: [
        "You name sky10 agents.",
        "Return only compact JSON in this shape: {\"name\":\"short-kebab-case-name\"}.",
        "The name must be lowercase kebab-case, 2 to 5 words, memorable, and specific to the requested work.",
        "Do not include punctuation outside the JSON object.",
      ].join("\n"),
      messages: [
        {
          role: "user",
          content: [
            `Agent prompt: ${trimmed}`,
            "",
            existingNames.length > 0
              ? `Avoid existing names: ${existingNames.join(", ")}`
              : "No existing names.",
          ].join("\n"),
        },
      ],
    });
    generated = parseAgentNameResponse(result.text);
  } catch {
    generated = fallbackAgentName(trimmed);
  }

  return uniqueAgentName(generated ?? fallbackAgentName(trimmed), existingNames);
}

function fallbackAgentName(prompt: string) {
  const ignored = new Set([
    "a",
    "ai",
    "an",
    "and",
    "agent",
    "for",
    "make",
    "me",
    "my",
    "that",
    "the",
    "to",
    "with",
  ]);
  const parts = prompt
    .toLowerCase()
    .match(/[a-z0-9]+/g)
    ?.filter((part) => !ignored.has(part))
    .slice(0, 4);
  if (!parts || parts.length === 0) return "custom-agent";
  return `${parts.join("-")}-agent`.slice(0, 64).replace(/-+$/g, "");
}

function uniqueAgentName(name: string, existingNames: readonly string[]) {
  const base = normalizeGeneratedAgentName(name) ?? "custom-agent";
  const existing = new Set(
    existingNames
      .map((item) => normalizeGeneratedAgentName(item))
      .filter((item): item is string => Boolean(item)),
  );
  if (!existing.has(base)) return base;
  for (let i = 2; i < 100; i += 1) {
    const candidate = `${base}-${i}`;
    if (!existing.has(candidate)) return candidate;
  }
  return `${base}-${Math.random().toString(36).slice(2, 6)}`;
}

async function planIntentWithModel(
  prompt: string,
  hooks: RootAgentHooks,
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
        "Use page_context when the request asks about the current page, current screen, selected text, a screenshot, a troubleshooting note, or a context document.",
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
            plannerToolSummary(rootAgentReadOnlyToolNames),
            "",
            "Approval-gated AI SDK tools:",
            plannerToolSummary(rootAgentApprovalRequiredToolNames),
            "",
            `Disabled RPCs: ${disabledRootAgentRPCs.join(", ")}`,
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

function asksAboutCurrentView(prompt: string) {
  const value = prompt.trim().toLowerCase();
  return (
    value.includes("this page") ||
    value.includes("this view") ||
    value.includes("this screen") ||
    value.includes("current page") ||
    value.includes("current view") ||
    value.includes("on screen") ||
    value.includes("what am i looking at") ||
    value.includes("what is happening here") ||
    value.includes("what's happening here") ||
    value.includes("whats happening here") ||
    value.includes("explain here") ||
    value.includes("troubleshoot here") ||
    value.includes("needs attention here")
  );
}

function isGreetingPrompt(prompt: string) {
  return /^(hi|hello|hey|yo|sup|howdy)[.!?\s]*$/i.test(prompt.trim());
}

function splitAnswerParagraphs(text: string) {
  const paragraphs = text
    .split(/\n{2,}/)
    .map((line) => line.trim())
    .filter(Boolean);
  return paragraphs.length > 0 ? paragraphs : [text.trim()];
}

async function runGreetingPrompt(
  hooks: RootAgentHooks,
  pageContext?: RootAgentPageContext,
): Promise<RootAgentResult> {
  hooks.onStatus?.("Ready.");
  const pageLabel = pageContext?.pageLabel ?? "this page";
  const answer = await streamParagraphs(hooks, [
    `Hey. I can help with ${pageLabel}.`,
    "Ask what looks wrong, attach a screenshot, or tell me what you are trying to do.",
  ]);

  return {
    answer,
    followUps: [
      "Tell me what needs attention here.",
      "Make a troubleshooting note for this view.",
      "Check whether my network is healthy.",
    ],
    status: "complete",
  };
}

async function runModelContextPrompt(
  prompt: string,
  hooks: RootAgentHooks,
  pageContext?: RootAgentPageContext,
  screenshot?: RootAgentScreenshotContext | null,
): Promise<RootAgentResult | null> {
  hooks.onStatus?.("Thinking with page context.");
  const context = pageContext
    ? formatRootAgentContextForPrompt(pageContext, screenshot)
    : "No browser page context was captured.";

  try {
    const result = await codex.chat({
      model: "gpt-5.5",
      reasoning_effort: "low",
      system_prompt: [
        "You are RootAgent inside the sky10 web UI.",
        "Answer like a pragmatic in-product assistant, not a demo script.",
        "Use the current page context when it helps, but do not dump it back at the user.",
        "Keep responses concise and concrete. Avoid implementation chatter, roadmap language, and phrases like MVP.",
        "If the user asks you to change state, explain the next safe action instead of pretending the change happened.",
        "Screenshot context only contains metadata unless the user describes what is in the image.",
      ].join("\n"),
      messages: [
        {
          role: "user",
          content: [
            `User message: ${prompt}`,
            "",
            context,
          ].join("\n"),
        },
      ],
    });
    const text = result.text.trim();
    if (!text) return null;
    const answer = await streamParagraphs(hooks, splitAnswerParagraphs(text));
    return {
      answer,
      followUps: [
        "Tell me what needs attention here.",
        "Make a troubleshooting note for this view.",
        "Attach a screenshot and look at it with me.",
      ],
      status: "complete",
    };
  } catch {
    return null;
  }
}

async function runNodeDiagnosis(hooks: RootAgentHooks): Promise<RootAgentResult> {
  hooks.onStatus?.("Scanning storage, network, agents, sandboxes, and activity.");
  const health = await recordTool(
    hooks,
    "daemon.getHealth",
    "system.health",
    "Read daemon health",
    "Inspecting storage health, queue depth, and runtime counters.",
    () => rootAgentToolRunners.daemon_getHealth(),
    summarizeHealth
  );
  const network = await recordTool(
    hooks,
    "network.getStatus",
    "skylink.status",
    "Read network status",
    "Checking peer connectivity and delivery health.",
    () => rootAgentToolRunners.network_getStatus(),
    summarizeNetwork
  );
  const activity = await recordTool(
    hooks,
    "activity.list",
    "skyfs.syncActivity",
    "Read sync activity",
    "Inspecting pending transfers, conflicts, and path issues.",
    () => rootAgentToolRunners.sync_activity(),
    (result) => `${result.pending.length} pending · ${result.conflicts.length} conflicts · ${result.path_issues.length} path issues`
  );
  const sandboxes = await recordTool(
    hooks,
    "sandboxes.list",
    "sandbox.list",
    "List sandboxes",
    "Checking managed runtimes for provisioning errors or busy state.",
    () => rootAgentToolRunners.sandboxes_list(),
    summarizeSandboxes
  );
  const agents = await recordTool(
    hooks,
    "agents.list",
    "agent.list",
    "List agents",
    "Reviewing registered agent presence.",
    () => rootAgentToolRunners.agents_list(),
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

async function runPageContextPrompt(
  hooks: RootAgentHooks,
  pageContext?: RootAgentPageContext,
  screenshot?: RootAgentScreenshotContext | null,
): Promise<RootAgentResult> {
  hooks.onStatus?.("Reading page context and live daemon state.");
  const health = await recordTool(
    hooks,
    "daemon.getHealth",
    "system.health",
    "Read daemon health",
    "Checking the daemon snapshot behind the current view.",
    () => rootAgentToolRunners.daemon_getHealth(),
    summarizeHealth
  );

  const routeTrace: string[] = [];
  switch (pageContext?.area) {
    case "agents": {
      const agents = await recordTool(
        hooks,
        "agents.list",
        "agent.list",
        "List agents",
        "Reading the live agent registry for this page.",
        () => rootAgentToolRunners.agents_list(),
        summarizeAgents
      );
      routeTrace.push(`${agents.count} registered agent${agents.count === 1 ? "" : "s"}.`);
      break;
    }
    case "drives":
    case "files": {
      const drives = await recordTool(
        hooks,
        "drives.list",
        "skyfs.driveList",
        "List drives",
        "Reading configured drives for this storage view.",
        () => rootAgentToolRunners.drives_list(),
        (result) => `${result.drives.length} drive${result.drives.length === 1 ? "" : "s"} configured`
      );
      routeTrace.push(`${drives.drives.length} configured drive${drives.drives.length === 1 ? "" : "s"}.`);
      break;
    }
    case "network": {
      const network = await recordTool(
        hooks,
        "network.getStatus",
        "skylink.status",
        "Read network status",
        "Reading peer and delivery state for this network view.",
        () => rootAgentToolRunners.network_getStatus(),
        summarizeNetwork
      );
      routeTrace.push(`${network.peers} connected peer${network.peers === 1 ? "" : "s"}.`);
      break;
    }
    case "sandbox": {
      const sandboxes = await recordTool(
        hooks,
        "sandboxes.list",
        "sandbox.list",
        "List sandboxes",
        "Reading managed runtime state for this sandbox view.",
        () => rootAgentToolRunners.sandboxes_list(),
        summarizeSandboxes
      );
      routeTrace.push(`${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"} in inventory.`);
      break;
    }
    case "devices": {
      const devices = await recordTool(
        hooks,
        "devices.list",
        "identity.deviceList",
        "List devices",
        "Reading device membership for this view.",
        () => rootAgentToolRunners.devices_list(),
        summarizeDevices
      );
      routeTrace.push(`${devices.devices.length} known device${devices.devices.length === 1 ? "" : "s"}.`);
      break;
    }
  }

  const lines: string[] = [
    pageContext
      ? `You are looking at ${pageContext.pageLabel} at \`${pageContext.route}\`. The main heading is "${pageContext.heading}".`
      : "I could not read the current page context from the browser.",
    `Live daemon snapshot: ${summarizeHealth(health)}.`,
  ];

  if (routeTrace.length > 0) {
    lines.push(`Route-specific check: ${routeTrace.join(" ")}`);
  }
  if (pageContext?.selection) {
    lines.push(`Selected text: ${pageContext.selection}`);
  } else if (pageContext?.visibleText) {
    lines.push(`Visible page context: ${pageContext.visibleText.slice(0, 700)}${pageContext.visibleText.length > 700 ? "..." : ""}`);
  }
  if (pageContext?.controls.length) {
    lines.push(`Visible actions include: ${pageContext.controls.slice(0, 8).join(", ")}.`);
  }
  if (screenshot) {
    lines.push(`A browser screenshot was captured as \`${screenshot.filename}\` (${screenshot.width}x${screenshot.height}).`);
  }

  const answer = await streamParagraphs(hooks, lines);
  return {
    answer,
    followUps: [
      "Make a troubleshooting note for this view.",
      "Tell me what needs attention right now.",
      "Which drives or files need attention?",
    ],
    status: "complete",
  };
}

async function runAgentCreatePrompt(
  prompt: string,
  hooks: RootAgentHooks,
  audience: AgentAudience
): Promise<RootAgentResult> {
  hooks.onStatus?.("Reading...");
  const [agents, sandboxes] = await Promise.all([
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Checking what agents already exist.",
      () => rootAgentToolRunners.agents_list(),
      summarizeAgents
    ),
    recordTool(
      hooks,
      "sandboxes.list",
      "sandbox.list",
      "List sandboxes",
      "Checking available managed runtimes.",
      () => rootAgentToolRunners.sandboxes_list(),
      summarizeSandboxes
    ),
  ]);

  const serviceAgent = audience === "for_others" || soundsCommercial(prompt);
  const answer = await streamParagraphs(
    hooks,
    serviceAgent
      ? [
          `Agent spec: ${prompt}.`,
          "Define the offer, inputs, runtime, billing, data boundaries, and isolation before serving outside users.",
          `Inventory: ${agents.count} registered agent${agents.count === 1 ? "" : "s"} · ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"}.`,
        ]
      : [
          `Agent spec: ${prompt}.`,
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
  hooks: RootAgentHooks
): Promise<RootAgentResult> {
  hooks.onStatus?.("Checking live RPC state and assistant policy boundaries.");
  const [health, drives, devices, sandboxes, agents] = await Promise.all([
    recordTool(
      hooks,
      "daemon.getHealth",
      "system.health",
      "Read daemon health",
      "Checking current daemon and storage state.",
      () => rootAgentToolRunners.daemon_getHealth(),
      summarizeHealth
    ),
    recordTool(
      hooks,
      "drives.list",
      "skyfs.driveList",
      "List drives",
      "Reading configured drive inventory before planning changes.",
      () => rootAgentToolRunners.drives_list(),
      (result) => `${result.drives.length} drive${result.drives.length === 1 ? "" : "s"} configured`
    ),
    recordTool(
      hooks,
      "devices.list",
      "identity.deviceList",
      "List devices",
      "Reading current device membership.",
      () => rootAgentToolRunners.devices_list(),
      summarizeDevices
    ),
    recordTool(
      hooks,
      "sandboxes.list",
      "sandbox.list",
      "List sandboxes",
      "Reading managed runtime inventory.",
      () => rootAgentToolRunners.sandboxes_list(),
      summarizeSandboxes
    ),
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Reading registered agent inventory.",
      () => rootAgentToolRunners.agents_list(),
      summarizeAgents
    ),
  ]);

  const answer = await streamParagraphs(hooks, [
    `I would treat \`${prompt}\` as an approval-gated RPC configuration request, not as a separate settings-page workflow.`,
    `Current context: ${summarizeHealth(health)} · ${drives.drives.length} drive${drives.drives.length === 1 ? "" : "s"} · ${devices.devices.length} device${devices.devices.length === 1 ? "" : "s"} · ${sandboxes.sandboxes.length} sandbox${sandboxes.sandboxes.length === 1 ? "" : "es"} · ${agents.count} agent${agents.count === 1 ? "" : "s"}.`,
    "The AI-first contract is that user-configurable RPC surfaces should be model-addressable through curated tools. Mutating operations need a visible plan, exact parameters, and approval before execution.",
    `This build has ${rootAgentApprovalRequiredToolNames.length} approval-gated AI SDK tool wrappers for RPC writes such as drives, secrets, sandboxes, apps, updates, device invites, and wallet setup. The next step is wiring approval cards to execute those tools after user confirmation.`,
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

async function runFallback(
  prompt: string,
  hooks: RootAgentHooks,
  pageContext?: RootAgentPageContext,
  screenshot?: RootAgentScreenshotContext | null,
): Promise<RootAgentResult> {
  const modelResult = await runModelContextPrompt(prompt, hooks, pageContext, screenshot);
  if (modelResult) return modelResult;

  hooks.onStatus?.("Building a quick read-only overview of the node.");
  const [health, agents, devices, network] = await Promise.all([
    recordTool(
      hooks,
      "daemon.getHealth",
      "system.health",
      "Read daemon health",
      "Checking the daemon health snapshot.",
      () => rootAgentToolRunners.daemon_getHealth(),
      summarizeHealth
    ),
    recordTool(
      hooks,
      "agents.list",
      "agent.list",
      "List agents",
      "Reviewing the current agent registry.",
      () => rootAgentToolRunners.agents_list(),
      summarizeAgents
    ),
    recordTool(
      hooks,
      "devices.list",
      "identity.deviceList",
      "List devices",
      "Reviewing current device membership.",
      () => rootAgentToolRunners.devices_list(),
      summarizeDevices
    ),
    recordTool(
      hooks,
      "network.getStatus",
      "skylink.status",
      "Read network status",
      "Inspecting peer connectivity.",
      () => rootAgentToolRunners.network_getStatus(),
      summarizeNetwork
    ),
  ]);

  const answer = await streamParagraphs(hooks, [
    `I could not reach model-backed help for \`${prompt}\`, so I checked the live node state I can read safely.`,
    `Current snapshot: ${summarizeHealth(health)} · ${agents.count} agent${agents.count === 1 ? "" : "s"} · ${devices.devices.length} device${devices.devices.length === 1 ? "" : "s"} · ${network.peers} connected peer${network.peers === 1 ? "" : "s"}.`,
    "Ask about a specific page, drive, agent, network issue, or attach a screenshot if you want me to narrow it down.",
  ]);

  return {
    answer,
    followUps: ["Tell me what needs attention right now.", "Which drives or files need attention?", "Check whether my network is healthy."],
    status: "complete",
  };
}

export async function executeRootAgentPrompt(
  prompt: string,
  hooks: RootAgentHooks = {},
  options: ExecuteOptions = {}
): Promise<RootAgentResult> {
  const contextPrompt = options.pageContext
    ? formatRootAgentContextForPrompt(options.pageContext, options.screenshot)
    : "";
  const planningPrompt = contextPrompt ? `${prompt}\n\n${contextPrompt}` : prompt;
  const contextIntent =
    options.pageContext && asksAboutCurrentView(prompt) ? "page_context" : undefined;
  const greetingIntent = isGreetingPrompt(prompt) ? "greeting" : undefined;
  const intent =
    options.intent ??
    contextIntent ??
    greetingIntent ??
    (await planIntentWithModel(planningPrompt, hooks)) ??
    detectIntent(planningPrompt);
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
    case "greeting":
      return runGreetingPrompt(hooks, options.pageContext);
    case "page_context":
      return runPageContextPrompt(hooks, options.pageContext, options.screenshot);
    case "agent_create":
      return runAgentCreatePrompt(prompt, hooks, audience);
    case "configuration":
      return runConfigurationPrompt(prompt, hooks);
    default:
      return runFallback(prompt, hooks, options.pageContext, options.screenshot);
  }
}
