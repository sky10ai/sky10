import type {
  AgentListResult,
  DeviceListResult,
  HealthResult,
  LinkStatus,
  RootAgentToolTrace,
  SandboxListResult,
  SyncActivityResult,
} from "./rpc";

export type { RootAgentToolTrace } from "./rpc";

export type RootAgentStatus = "complete" | "error" | "running";
export type AgentAudience = "for_me" | "for_others";

export interface RootAgentResult {
  answer: string;
  followUps?: string[];
  status: Exclude<RootAgentStatus, "running">;
}

export interface RootAgentHooks {
  onStatus?: (value: string) => void;
  onText?: (value: string) => void;
  onTool?: (trace: RootAgentToolTrace) => void;
}

export type AssistantIntent =
  | "agent_create"
  | "agents"
  | "configuration"
  | "daemon_version"
  | "devices"
  | "drives"
  | "fallback"
  | "network"
  | "node_diagnosis"
  | "sandboxes"
  | "sync_activity";

function makeID() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function lower(value: string) {
  return value.trim().toLowerCase();
}

function includesAny(value: string, needles: readonly string[]) {
  return needles.some((needle) => value.includes(needle));
}

function asksForVersion(value: string) {
  if (!value.includes("version")) return false;
  return (
    value.includes("what version") ||
    value.includes("version of") ||
    value.includes("sky10 version") ||
    includesAny(value, ["daemon", "sky10", "app", "cli", "binary", "build"])
  );
}

export function detectIntent(prompt: string): AssistantIntent {
  const value = lower(prompt);

  if (
    value.includes("create me an agent") ||
    value.startsWith("create an agent") ||
    value.includes("build me an agent")
  ) {
    return "agent_create";
  }

  if (
    value.includes("configure") ||
    value.includes("set up") ||
    value.includes("setup") ||
    value.includes("install") ||
    value.includes("uninstall") ||
    value.includes("api key") ||
    value.includes("secret") ||
    value.includes("wallet") ||
    value.includes("payment") ||
    value.includes("create a drive") ||
    value.includes("new drive") ||
    value.includes("invite device") ||
    value.includes("join device") ||
    value.includes("create a sandbox") ||
    value.includes("start sandbox") ||
    value.includes("stop sandbox")
  ) {
    return "configuration";
  }

  if (asksForVersion(value)) {
    return "daemon_version";
  }

  if (
    value.includes("what's wrong") ||
    value.includes("whats wrong") ||
    value.includes("diagnose") ||
    value.includes("health") ||
    value.includes("looks wrong") ||
    value.includes("status summary") ||
    value.includes("needs attention") ||
    value.includes("plain english")
  ) {
    return "node_diagnosis";
  }

  if (value.includes("healthy") && value.includes("network")) return "network";
  if (value.includes("activity") || value.includes("conflict") || value.includes("path issue")) {
    return "sync_activity";
  }

  if (value.includes("drive")) return "drives";
  if (value.includes("device")) return "devices";
  if (value.includes("sandbox")) return "sandboxes";
  if (value.includes("network") || value.includes("peer")) return "network";
  if (value.includes("agent")) return "agents";

  return "fallback";
}

export async function pause(ms: number) {
  if (ms <= 0) return;
  await new Promise((resolve) => window.setTimeout(resolve, ms));
}

export function summarizeHealth(health: HealthResult) {
  const pending = health.outbox_pending + health.transfer_pending;
  const issues: string[] = [];

  if (health.sync_error_drives > 0) {
    issues.push(`${health.sync_error_drives} drive${health.sync_error_drives === 1 ? "" : "s"} degraded`);
  }
  if (health.path_issue_drives > 0) {
    issues.push(`${health.path_issue_drives} drive${health.path_issue_drives === 1 ? "" : "s"} with path issues`);
  }
  if (health.conflict_drives > 0) {
    issues.push(`${health.conflict_files} conflicted file${health.conflict_files === 1 ? "" : "s"}`);
  }
  if (issues.length === 0) {
    issues.push("no major storage issues surfaced");
  }

  return `${health.drives_running}/${health.drives} drives live · ${pending} pending · ${issues.join(" · ")}`;
}

export function summarizeNetwork(status: LinkStatus) {
  const notes: string[] = [`${status.peers} peer${status.peers === 1 ? "" : "s"}`];

  if (status.health.transport_degraded_reason) {
    notes.push(`transport: ${status.health.transport_degraded_reason.replaceAll("_", " ")}`);
  }
  if (status.health.delivery_degraded_reason) {
    notes.push(`delivery: ${status.health.delivery_degraded_reason.replaceAll("_", " ")}`);
  }
  if (status.health.coordination_degraded_reason) {
    notes.push(`coordination: ${status.health.coordination_degraded_reason.replaceAll("_", " ")}`);
  }
  if (
    !status.health.transport_degraded_reason &&
    !status.health.delivery_degraded_reason &&
    !status.health.coordination_degraded_reason
  ) {
    notes.push("network looks healthy");
  }

  return notes.join(" · ");
}

export function summarizeDevices(result: DeviceListResult) {
  const current = result.devices.find((device) => device.current);
  return `${result.devices.length} device${result.devices.length === 1 ? "" : "s"} known${current ? ` · current node ${current.name}` : ""}`;
}

export function summarizeAgents(result: AgentListResult) {
  return `${result.count} registered agent${result.count === 1 ? "" : "s"}`;
}

export function summarizeSandboxes(result: SandboxListResult) {
  if (result.sandboxes.length === 0) {
    return "no sandboxes provisioned";
  }
  const busy = result.sandboxes.filter((item) =>
    ["creating", "provisioning", "starting", "stopping"].includes(item.status)
  ).length;
  const failed = result.sandboxes.filter((item) =>
    item.status.includes("error") || item.status.includes("failed")
  ).length;
  const notes = [`${result.sandboxes.length} sandbox${result.sandboxes.length === 1 ? "" : "es"}`];
  if (busy > 0) notes.push(`${busy} in progress`);
  if (failed > 0) notes.push(`${failed} need attention`);
  return notes.join(" · ");
}

export function summarizeActivity(result: SyncActivityResult) {
  const notes = [
    `${result.pending.length} pending transfer${result.pending.length === 1 ? "" : "s"}`,
    `${result.conflicts.length} conflict${result.conflicts.length === 1 ? "" : "s"}`,
    `${result.path_issues.length} path issue${result.path_issues.length === 1 ? "" : "s"}`,
  ];
  return notes.join(" · ");
}

export async function recordTool<T>(
  hooks: RootAgentHooks,
  tool: string,
  rpcMethod: string,
  title: string,
  detail: string,
  run: () => Promise<T>,
  summarize: (result: T) => string
) {
  const startedAt = new Date().toISOString();
  const id = makeID();

  hooks.onTool?.({
    id,
    title,
    tool,
    rpcMethod,
    status: "running",
    detail,
    startedAt,
  });

  try {
    const result = await run();
    hooks.onTool?.({
      id,
      title,
      tool,
      rpcMethod,
      status: "complete",
      detail: summarize(result),
      startedAt,
      finishedAt: new Date().toISOString(),
    });
    await pause(120);
    return result;
  } catch (error) {
    const message = error instanceof Error ? error.message : "Tool failed";
    hooks.onTool?.({
      id,
      title,
      tool,
      rpcMethod,
      status: "error",
      detail: message,
      startedAt,
      finishedAt: new Date().toISOString(),
    });
    throw error;
  }
}

export async function streamParagraphs(hooks: RootAgentHooks, paragraphs: string[]) {
  let buffer = "";
  for (const paragraph of paragraphs) {
    buffer = buffer ? `${buffer}\n\n${paragraph}` : paragraph;
    hooks.onText?.(buffer);
    await pause(140);
  }
  return buffer;
}
