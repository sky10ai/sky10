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

export const AUDIENCE_OPTIONS: Array<{
  description: string;
  id: AgentAudience;
  title: string;
}> = [
  {
    id: "for_me",
    title: "For me",
    description: "Automate my files, workflows, and personal tasks.",
  },
  {
    id: "for_others",
    title: "For others",
    description: "Offer goods or services people can use and eventually pay for.",
  },
] as const;

export const SUGGESTED_PROMPTS: Record<AgentAudience, readonly string[]> = {
  for_me: [
    "Create an agent that watches my Downloads folder and organizes receipts.",
    "Create an agent that turns meeting recordings into notes and action items.",
    "Create an agent that watches a folder and transcribes new media.",
    "Create an agent that checks my sync health every morning and tells me what needs attention.",
  ],
  for_others: [
    "Create an agent that transcribes podcasts and charges per upload.",
    "Create an agent that makes British-accent dubbed videos for clients.",
    "Create an agent that turns long videos into clips people can order.",
    "Create an agent that sells weekly sync health reports to small teams.",
  ],
} as const;

export const AUDIENCE_PLACEHOLDERS: Record<AgentAudience, string> = {
  for_me:
    "Create an agent that watches a folder, processes files, and writes outputs back safely.",
  for_others:
    "Create an agent that offers a service, handles customer work safely, and can eventually get paid.",
};

export function audienceLabel(audience: AgentAudience) {
  return audience === "for_me" ? "For me" : "For others";
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
