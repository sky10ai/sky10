import type { SandboxLogEntry } from "./rpc";

export const SANDBOX_TEMPLATES = [
  {
    id: "ubuntu",
    provider: "lima",
    label: "Ubuntu 24.04",
    summary: "Clean Linux runtime",
    description: "A plain Ubuntu sandbox running under Lima on macOS.",
  },
] as const;

export function nextSandboxName() {
  return `linux-${Math.random().toString(36).slice(2, 6)}`;
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

export function sandboxLogKey(entry: SandboxLogEntry, index: number) {
  return `${entry.time}:${entry.stream}:${index}`;
}
