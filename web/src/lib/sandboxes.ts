import type { SandboxLogEntry, SandboxProgress, SandboxRecord } from "./rpc";

export const SANDBOX_TEMPLATES = [
  {
    id: "ubuntu",
    provider: "lima",
    label: "Ubuntu 24.04",
    summary: "Clean Linux runtime",
    description: "A plain Ubuntu sandbox running under Lima on macOS.",
  },
  {
    id: "openclaw",
    provider: "lima",
    label: "OpenClaw Sandbox",
    summary: "Managed browser runtime",
    description:
      "Ubuntu VM on Lima that installs guest-local sky10, OpenClaw, a bundled sky10 channel plugin, and guest UIs on ports 9101 and 18790.",
  },
  {
    id: "hermes",
    provider: "lima",
    label: "Hermes Sandbox",
    summary: "Hermes chat + TUI runtime",
    description:
      "Ubuntu VM on Lima that installs Hermes Agent, links /sandbox-state/.env into ~/.hermes/.env, bridges Hermes into the host sky10 agent list, and keeps the Hermes TUI available in /shared/workspace.",
  },
] as const;

export const DEFAULT_SANDBOX_TEMPLATE = SANDBOX_TEMPLATES[0];

export function sandboxTemplateById(templateId?: string) {
  return SANDBOX_TEMPLATES.find((template) => template.id === templateId) ?? DEFAULT_SANDBOX_TEMPLATE;
}

export function nextSandboxName(templateId: string = DEFAULT_SANDBOX_TEMPLATE.id) {
  const prefix = templateId === "openclaw" ? "openclaw" : templateId === "hermes" ? "hermes" : "linux";
  return `${prefix}-${Math.random().toString(36).slice(2, 6)}`;
}

export function sandboxSlug(name: string) {
  return name
    .trim()
    .toLowerCase()
    .match(/[a-z0-9]+/g)
    ?.join("-") ?? "";
}

export function sandboxTone(status: string): "processing" | "success" | "neutral" | "danger" {
  switch (status) {
    case "creating":
    case "starting":
      return "processing";
    case "ready":
      return "success";
    case "error":
      return "danger";
    default:
      return "neutral";
  }
}

export function sandboxLabel(status: string) {
  switch (status) {
    case "creating":
      return "Creating";
    case "starting":
      return "Starting";
    case "ready":
      return "Ready";
    case "stopped":
      return "Stopped";
    case "error":
      return "Error";
    default:
      return status || "Unknown";
  }
}

export function sandboxCurrentProgress(record: Pick<SandboxRecord, "status" | "progress">): SandboxProgress | null {
  const progress = record.progress;
  if (!progress || !progress.summary?.trim()) {
    return null;
  }
  if (record.status !== "creating" && record.status !== "starting" && record.status !== "error") {
    return null;
  }
  return progress;
}

export function sandboxLogKey(entry: SandboxLogEntry, index: number) {
  return `${entry.time}:${entry.stream}:${index}`;
}
