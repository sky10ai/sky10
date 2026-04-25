import type { HealthResult, SandboxRuntimeStatusResult } from "./rpc";

export type RuntimeTone =
  | "danger"
  | "live"
  | "neutral"
  | "processing"
  | "success";

export interface RuntimeStatusView {
  currentVersion?: string;
  detail: string;
  guestVersion?: string;
  hasDrift: boolean;
  hostVersion?: string;
  icon: string;
  label: string;
  latestVersion?: string;
  tone: RuntimeTone;
  updateReady: boolean;
}

interface RuntimeUpdateStatus {
  cliStaged?: boolean;
  current?: string;
  latest?: string;
  menuStaged?: boolean;
  ready?: boolean;
}

export function sandboxRuntimeView(
  runtime: SandboxRuntimeStatusResult | null | undefined,
  hostHealth: HealthResult | null | undefined,
): RuntimeStatusView | null {
  if (!runtime) return null;

  const hostVersion = normalizeRuntimeVersion(hostHealth?.version);
  const guestVersion = normalizeRuntimeVersion(runtime.version);
  const updateStatus = parseRuntimeUpdateStatus(runtime.update_status);
  const currentVersion =
    normalizeRuntimeVersion(updateStatus.current) || guestVersion;
  const latestVersion = normalizeRuntimeVersion(updateStatus.latest);
  const updateReady =
    updateStatus.ready === true ||
    Boolean(latestVersion && currentVersion && latestVersion !== currentVersion);

  if (!runtime.reachable || runtime.error) {
    return {
      currentVersion,
      detail: runtime.error || "Guest runtime did not answer.",
      guestVersion,
      hasDrift: false,
      hostVersion,
      icon: "error",
      label: "Runtime unreachable",
      latestVersion,
      tone: "danger",
      updateReady: false,
    };
  }

  if (hostVersion && guestVersion && hostVersion !== guestVersion) {
    const comparison = compareSemver(hostVersion, guestVersion);
    const guestLabel = guestVersion || "unknown";
    const hostLabel = hostVersion || "unknown";

    if (comparison > 0) {
      return {
        currentVersion,
        detail: `Guest ${guestLabel} / host ${hostLabel}`,
        guestVersion,
        hasDrift: true,
        hostVersion,
        icon: "system_update_alt",
        label: "Guest stale",
        latestVersion,
        tone: "danger",
        updateReady,
      };
    }

    return {
      currentVersion,
      detail: `Guest ${guestLabel} / host ${hostLabel}`,
      guestVersion,
      hasDrift: true,
      hostVersion,
      icon: "compare_arrows",
      label: comparison < 0 ? "Guest newer" : "Version drift",
      latestVersion,
      tone: "processing",
      updateReady,
    };
  }

  if (updateReady) {
    return {
      currentVersion,
      detail: latestVersion
        ? `Guest update staged: ${currentVersion || "current"} -> ${latestVersion}`
        : "Guest update is staged.",
      guestVersion,
      hasDrift: true,
      hostVersion,
      icon: "system_update_alt",
      label: "Update staged",
      latestVersion,
      tone: "processing",
      updateReady,
    };
  }

  if (runtime.update_status_error) {
    return {
      currentVersion,
      detail: runtime.update_status_error,
      guestVersion,
      hasDrift: false,
      hostVersion,
      icon: "help",
      label: "Update unknown",
      latestVersion,
      tone: "neutral",
      updateReady: false,
    };
  }

  return {
    currentVersion,
    detail: guestVersion ? `Guest ${guestVersion}` : "Guest runtime answered.",
    guestVersion,
    hasDrift: false,
    hostVersion,
    icon: "check_circle",
    label: "Runtime current",
    latestVersion,
    tone: "live",
    updateReady: false,
  };
}

export function runtimeUpdateLabel(
  runtime: SandboxRuntimeStatusResult | null | undefined,
): string {
  if (!runtime) return "Checking...";
  if (!runtime.reachable) return runtime.error || "Unavailable";
  if (runtime.update_status_error) return runtime.update_status_error;

  const updateStatus = parseRuntimeUpdateStatus(runtime.update_status);
  const current = normalizeRuntimeVersion(updateStatus.current);
  const latest = normalizeRuntimeVersion(updateStatus.latest);
  if (updateStatus.ready || (current && latest && current !== latest)) {
    return latest ? `Staged ${latest}` : "Staged";
  }
  return current ? `Current ${current}` : "No staged update";
}

export function normalizeRuntimeVersion(value?: string): string {
  const trimmed = value?.trim() ?? "";
  if (!trimmed) return "";
  const semver = trimmed.match(/v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?/);
  if (semver) {
    return semver[0].startsWith("v") ? semver[0] : `v${semver[0]}`;
  }
  return trimmed.split(/\s+/)[0] ?? "";
}

function parseRuntimeUpdateStatus(
  updateStatus: Record<string, unknown> | undefined,
): RuntimeUpdateStatus {
  if (!updateStatus) return {};
  return {
    cliStaged: boolField(updateStatus, "cli_staged"),
    current: stringField(updateStatus, "current"),
    latest: stringField(updateStatus, "latest"),
    menuStaged: boolField(updateStatus, "menu_staged"),
    ready: boolField(updateStatus, "ready"),
  };
}

function stringField(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return typeof value === "string" ? value : undefined;
}

function boolField(record: Record<string, unknown>, key: string) {
  const value = record[key];
  return typeof value === "boolean" ? value : undefined;
}

function compareSemver(left: string, right: string) {
  const a = semverParts(left);
  const b = semverParts(right);
  if (!a || !b) return 0;
  if (a[0] !== b[0]) return a[0] - b[0];
  if (a[1] !== b[1]) return a[1] - b[1];
  if (a[2] !== b[2]) return a[2] - b[2];
  return 0;
}

function semverParts(version: string): [number, number, number] | null {
  const match = version.match(/^v?(\d+)\.(\d+)\.(\d+)/);
  if (!match) return null;
  return [
    Number(match[1] ?? 0),
    Number(match[2] ?? 0),
    Number(match[3] ?? 0),
  ];
}
