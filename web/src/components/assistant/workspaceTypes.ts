import type { AgentAudience, RootAssistantToolTrace } from "../../lib/rootAssistant";
export type { AgentAudience } from "../../lib/rootAssistant";

export type WorkspaceRunStatus = "complete" | "error" | "running";

export interface WorkspaceRun {
  id: string;
  audience: AgentAudience;
  prompt: string;
  answer: string;
  status: WorkspaceRunStatus;
  createdAt: string;
  updatedAt: string;
  toolTraces: RootAssistantToolTrace[];
  followUps?: string[];
}

export function toolTone(status: RootAssistantToolTrace["status"]) {
  if (status === "complete") return "live";
  if (status === "error") return "danger";
  return "processing";
}

export function runTone(status: WorkspaceRunStatus) {
  if (status === "complete") return "live";
  if (status === "error") return "danger";
  return "processing";
}
