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
