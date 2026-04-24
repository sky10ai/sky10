import type { SandboxForwardedEndpoint, SandboxLogEntry, SandboxProgress, SandboxRecord } from "./rpc";

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
    id: "openclaw-docker",
    provider: "lima",
    label: "OpenClaw Sandbox + Docker",
    summary: "Docker-backed browser runtime",
    description:
      "Ubuntu VM on Lima that installs Docker in the guest, then runs guest-local sky10, OpenClaw, Chromium, and Caddy inside Docker containers with the same shared workspace and guest UIs.",
  },
  {
    id: "hermes",
    provider: "lima",
    label: "Hermes Sandbox",
    summary: "Hermes chat + TUI runtime",
    description:
      "Ubuntu VM on Lima that installs Hermes Agent, links /sandbox-state/.env into ~/.hermes/.env, bridges Hermes into the host sky10 agent list, and keeps the Hermes TUI available in /shared/workspace.",
  },
  {
    id: "hermes-docker",
    provider: "lima",
    label: "Hermes Sandbox + Docker",
    summary: "Docker-backed chat + TUI runtime",
    description:
      "Ubuntu VM on Lima that installs Docker in the guest, then runs guest-local sky10, Hermes Agent, and the host chat bridge inside Docker while keeping hermes-shared available from the sandbox terminal.",
  },
] as const;

export const DEFAULT_SANDBOX_TEMPLATE = SANDBOX_TEMPLATES[0];

export function sandboxTemplateById(templateId?: string) {
  return SANDBOX_TEMPLATES.find((template) => template.id === templateId) ?? DEFAULT_SANDBOX_TEMPLATE;
}

export function nextSandboxName(templateId: string = DEFAULT_SANDBOX_TEMPLATE.id) {
  const prefix = isOpenClawTemplate(templateId) ? "openclaw" : isHermesTemplate(templateId) ? "hermes" : "linux";
  return `${prefix}-${Math.random().toString(36).slice(2, 6)}`;
}

export function isOpenClawTemplate(templateId?: string) {
  return templateId === "openclaw" || templateId === "openclaw-docker";
}

export function isHermesTemplate(templateId?: string) {
  return templateId === "hermes" || templateId === "hermes-docker";
}

export function isDockerTemplate(templateId?: string) {
  return templateId === "openclaw-docker" || templateId === "hermes-docker";
}

export function sandboxForwardedEndpoint(
  record: SandboxRecord | null | undefined,
  name: "sky10" | "openclaw_gateway",
): SandboxForwardedEndpoint | null {
  const endpoint = record?.forwarded_endpoints?.find(
    (item) => item.name === name && item.host && item.host_port && item.host_port > 0,
  );
  if (endpoint) return endpoint;

  const host = record?.forwarded_host?.trim();
  const basePort = record?.forwarded_port;
  if (!host || !basePort || basePort <= 0) return null;
  if (name === "sky10") return { name, host, host_port: basePort };
  if (name === "openclaw_gateway" && isOpenClawTemplate(record?.template)) {
    return { name, host, host_port: basePort + 1 };
  }
  return null;
}

export function sandboxForwardedURL(
  record: SandboxRecord | null | undefined,
  name: "sky10" | "openclaw_gateway",
  path = "",
) {
  const endpoint = sandboxForwardedEndpoint(record, name);
  if (!endpoint?.host || !endpoint.host_port) return "";
  return `http://${endpoint.host}:${endpoint.host_port}${path}`;
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
