import type { AgentAudience, RootAgentToolTrace } from "../../lib/rootAgent";
export type { AgentAudience } from "../../lib/rootAgent";

export type WorkspaceRunStatus = "complete" | "error" | "running";

export interface WorkspaceRun {
  id: string;
  audience: AgentAudience;
  prompt: string;
  answer: string;
  status: WorkspaceRunStatus;
  createdAt: string;
  updatedAt: string;
  toolTraces: RootAgentToolTrace[];
  followUps?: string[];
}

function makeRunID() {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function createWorkspaceRun(
  prompt: string,
  audience: AgentAudience = "for_me",
): WorkspaceRun {
  const now = new Date().toISOString();
  return {
    audience,
    id: makeRunID(),
    prompt,
    answer: "",
    status: "running",
    createdAt: now,
    updatedAt: now,
    toolTraces: [],
  };
}

export function toolTone(status: RootAgentToolTrace["status"]) {
  if (status === "complete") return "live";
  if (status === "error") return "danger";
  return "processing";
}

export function runTone(status: WorkspaceRunStatus) {
  if (status === "complete") return "live";
  if (status === "error") return "danger";
  return "processing";
}
