import type { AgentInfo, AgentSandboxInfo, SandboxProgress } from "./rpc";
import { sandboxLabel, sandboxTone } from "./sandboxes";

export function agentCanConnect(agent: AgentInfo | null | undefined) {
  if (!agent) return false;
  const sandbox = agent.sandbox;
  if (!sandbox) {
    return agent.status !== "disconnected";
  }
  return sandboxConnectable(sandbox);
}

export function agentStatusLabel(agent: AgentInfo) {
  const sandbox = agent.sandbox;
  if (!sandbox) {
    return agent.status === "connected"
      ? "Connected"
      : agent.status || "Unknown";
  }
  if (sandboxConnectable(sandbox) && agent.status === "connected") {
    return "Connected";
  }
  return sandboxLabel(sandbox.status);
}

export function agentStatusTone(
  agent: AgentInfo,
): "danger" | "live" | "neutral" | "processing" | "success" {
  const sandbox = agent.sandbox;
  if (!sandbox) {
    return agent.status === "connected" ? "live" : "neutral";
  }
  if (sandboxConnectable(sandbox) && agent.status === "connected") {
    return "live";
  }
  return sandboxTone(sandbox.status);
}

export function agentStatusPulses(agent: AgentInfo) {
  const sandbox = agent.sandbox;
  if (!sandbox) return agent.status === "connected";
  return sandbox.status === "creating" || sandbox.status === "starting";
}

export function agentBootProgress(
  agent: AgentInfo | null | undefined,
): SandboxProgress | null {
  const sandbox = agent?.sandbox;
  const progress = sandbox?.progress;
  if (!sandbox || !progress || !progress.summary?.trim()) return null;
  if (
    sandbox.status !== "creating" &&
    sandbox.status !== "starting" &&
    sandbox.status !== "error"
  ) {
    return null;
  }
  return progress;
}

function sandboxConnectable(sandbox: AgentSandboxInfo) {
  const status = sandbox.status?.trim().toLowerCase() ?? "";
  if (status === "ready") return true;
  return status === "" && sandbox.vm_status?.trim().toLowerCase() === "running";
}
