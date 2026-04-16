import { useCallback, useEffect, useState } from "react";
import { subscribe } from "../lib/events";
import { system, type HealthResult } from "../lib/rpc";
import { formatBytes, useRPC } from "../lib/useRPC";
import { Icon } from "./Icon";

const UPDATE_REFRESH_EVENTS = [
  "update:available",
  "update:download:complete",
  "update:download:error",
  "update:install:complete",
  "update:install:error",
] as const;

type UpdateAction = "idle" | "downloading" | "installing" | "restarting";

const VERSION_PATTERN = /^(v[\d.]+(?:-\w+)?)\s+\((\w+)\)\s+built\s+(.+)$/;

export function parseVersionDetails(version: string) {
  const match = version.match(VERSION_PATTERN);
  return {
    buildDate: match?.[3]?.split("T")[0] ?? "",
    commit: match?.[2] ?? "",
    version: match?.[1] ?? version.split(" ")[0] ?? version,
  };
}

function VersionDetailsCard({ health }: { health?: HealthResult | null }) {
  const version = health?.version ?? "";
  const versionInfo = parseVersionDetails(version);
  const {
    data: updateInfo,
    error: updateCheckError,
    refetch: refetchUpdateInfo,
  } = useRPC(() => system.update.check(), [], {
    live: UPDATE_REFRESH_EVENTS,
  });
  const {
    data: stagedUpdate,
    refetch: refetchStagedUpdate,
  } = useRPC(() => system.update.status(), [], {
    live: UPDATE_REFRESH_EVENTS,
    refreshIntervalMs: 30_000,
  });

  const [updateAction, setUpdateAction] = useState<UpdateAction>("idle");
  const [updateProgress, setUpdateProgress] = useState<{
    downloaded: number;
    total: number;
  } | null>(null);
  const [updateError, setUpdateError] = useState<string | null>(null);
  const [legacyRestartTarget, setLegacyRestartTarget] = useState<string | null>(null);

  const refreshUpdateState = useCallback(() => {
    refetchUpdateInfo({ background: true });
    refetchStagedUpdate({ background: true });
  }, [refetchStagedUpdate, refetchUpdateInfo]);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event === "update:download:progress" || event === "update:progress") {
        const progress = data as { downloaded: number; total: number };
        setUpdateAction("downloading");
        setUpdateProgress(progress);
        return;
      }
      if (event === "update:complete") {
        const payload = data as { updated?: string };
        setUpdateAction("idle");
        setUpdateProgress(null);
        setUpdateError(null);
        setLegacyRestartTarget(payload.updated ?? updateInfo?.latest ?? null);
        refreshUpdateState();
        return;
      }
      if (event === "update:error") {
        const payload = data as { message: string };
        setUpdateAction("idle");
        setUpdateProgress(null);
        setUpdateError(payload.message);
        refreshUpdateState();
        return;
      }
      if (event === "update:download:complete") {
        setUpdateAction("idle");
        setUpdateProgress(null);
        setUpdateError(null);
        setLegacyRestartTarget(null);
        refreshUpdateState();
        return;
      }
      if (event === "update:download:error") {
        const payload = data as { message: string };
        setUpdateAction("idle");
        setUpdateProgress(null);
        setUpdateError(payload.message);
        setLegacyRestartTarget(null);
        refreshUpdateState();
        return;
      }
      if (event === "update:install:error") {
        const payload = data as { message: string };
        setUpdateAction("idle");
        setUpdateError(payload.message);
        refreshUpdateState();
      }
    });
  }, [refreshUpdateState, updateInfo?.latest]);

  const waitForUpdatedUI = useCallback(async (targetVersion: string) => {
    const deadline = Date.now() + 30_000;

    while (Date.now() < deadline) {
      try {
        const response = await fetch("/health", { cache: "no-store" });
        if (response.ok) {
          const body = (await response.json()) as { version?: string };
          if (body.version?.includes(targetVersion)) {
            window.location.reload();
            return true;
          }
        }
      } catch {
        // Restart window in progress.
      }

      await new Promise((resolve) => window.setTimeout(resolve, 1000));
    }

    return false;
  }, []);

  const handleDownloadUpdate = useCallback(async () => {
    setUpdateAction("downloading");
    setUpdateError(null);
    setUpdateProgress(null);
    setLegacyRestartTarget(null);
    try {
      if (stagedUpdate?.mode === "legacy") {
        await system.update.run();
        return;
      }
      await system.update.download();
    } catch (error: unknown) {
      setUpdateAction("idle");
      setUpdateError(
        error instanceof Error ? error.message : "Download failed",
      );
    }
  }, [stagedUpdate?.mode]);

  const handleInstallUpdate = useCallback(async () => {
    if (legacyRestartTarget) {
      setUpdateAction("restarting");
      setUpdateError(null);
      try {
        await system.restart();
        const restarted = await waitForUpdatedUI(legacyRestartTarget);
        if (!restarted) {
          setUpdateAction("idle");
          setUpdateError(
            "Update installed. The daemon restart is taking longer than expected.",
          );
        }
      } catch (error: unknown) {
        setUpdateAction("idle");
        setUpdateError(error instanceof Error ? error.message : "Restart failed");
      }
      return;
    }

    setUpdateAction("installing");
    setUpdateError(null);
    try {
      const result = await system.update.install();
      refreshUpdateState();
      if (result.restarting) {
        setUpdateAction("restarting");
        const restarted = await waitForUpdatedUI(result.latest);
        if (!restarted) {
          setUpdateAction("idle");
          setUpdateError(
            "Update installed. The daemon restart is taking longer than expected.",
          );
        }
        return;
      }
      setUpdateAction("idle");
    } catch (error: unknown) {
      setUpdateAction("idle");
      setUpdateError(error instanceof Error ? error.message : "Install failed");
    }
  }, [legacyRestartTarget, refreshUpdateState, waitForUpdatedUI]);

  const legacyMode = stagedUpdate?.mode === "legacy";
  const updateReady = Boolean(legacyRestartTarget) || (stagedUpdate?.ready ?? false);
  const updateAvailable = !updateReady && Boolean(updateInfo?.available);
  const updateTargetVersion = legacyRestartTarget ?? stagedUpdate?.latest ?? updateInfo?.latest ?? "";
  const updateNeedsRestart = Boolean(legacyRestartTarget) || (updateReady
    ? stagedUpdate?.cli_staged ?? false
    : updateInfo?.cli_available ?? false);
  const updateBusy = updateAction !== "idle";

  let updateMessage = "No update available right now.";
  if (updateAction === "downloading") {
    updateMessage = updateTargetVersion
      ? `Downloading ${updateTargetVersion}...`
      : "Downloading the latest update...";
  } else if (updateAction === "installing") {
    updateMessage = updateTargetVersion
      ? `Installing ${updateTargetVersion}...`
      : "Installing the staged update...";
  } else if (updateAction === "restarting") {
    updateMessage = "Restarting sky10. This page will refresh automatically.";
  } else if (legacyRestartTarget) {
    updateMessage = `${legacyRestartTarget} is installed. Restart to switch this UI to the new version.`;
  } else if (updateReady && updateTargetVersion) {
    updateMessage = updateNeedsRestart
      ? `${updateTargetVersion} is ready. Install it in place and restart this UI.`
      : `${updateTargetVersion} is ready to install.`;
  } else if (updateAvailable && updateTargetVersion) {
    updateMessage = legacyMode
      ? `${updateTargetVersion} is available. Update now, then restart when it finishes.`
      : `${updateTargetVersion} is available. Download it now and install when you're ready.`;
  } else if (updateCheckError) {
    updateMessage = "Update check is unavailable right now.";
  }

  const details = [
    {
      label: "Version",
      value: versionInfo.version || version || "...",
    },
    {
      label: "Commit",
      monospace: true,
      value: versionInfo.commit || "Unavailable",
    },
    {
      label: "Build Date",
      value: versionInfo.buildDate || "Unavailable",
    },
    {
      label: "Uptime",
      value: health?.uptime ?? "...",
    },
    {
      label: "RPC Clients",
      value: String(health?.rpc_clients ?? 0),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-4 py-2">
        {details.map((detail, index) => (
          <div
            key={detail.label}
            className={`flex items-start justify-between gap-4 py-3 ${index < details.length - 1 ? "border-b border-outline-variant/10" : ""}`}
          >
            <span className="text-sm text-secondary">{detail.label}</span>
            <span className={detail.monospace
              ? "rounded bg-surface-container px-2 py-0.5 font-mono text-xs text-on-surface"
              : "text-right text-sm font-semibold text-on-surface"}
            >
              {detail.value}
            </span>
          </div>
        ))}
      </div>

      <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-4 space-y-4">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Software Update
            </p>
            <p className="text-sm text-on-surface">{updateMessage}</p>
          </div>
          {updateTargetVersion && (
            <span className="shrink-0 rounded-full bg-primary/10 px-3 py-1 text-[10px] font-bold uppercase tracking-widest text-primary">
              {updateTargetVersion}
            </span>
          )}
        </div>

        {updateAction === "downloading" && (
          <div className="space-y-2">
            <div className="h-2 w-full overflow-hidden rounded-full bg-surface-container">
              <div
                className="h-full rounded-full bg-primary transition-all duration-300"
                style={{
                  width: updateProgress && updateProgress.total > 0
                    ? `${Math.round((updateProgress.downloaded / updateProgress.total) * 100)}%`
                    : "18%",
                }}
              />
            </div>
            {updateProgress && updateProgress.total > 0 && (
              <p className="text-[10px] text-secondary">
                {formatBytes(updateProgress.downloaded)}
                {" / "}
                {formatBytes(updateProgress.total)}
              </p>
            )}
          </div>
        )}

        {(updateError || (!updateReady && !updateAvailable && updateCheckError)) && (
          <p className="text-xs text-error">
            {updateError ?? updateCheckError}
          </p>
        )}

        {(updateReady || updateAvailable || updateBusy) && (
          <button
            onClick={updateReady ? handleInstallUpdate : handleDownloadUpdate}
            disabled={updateBusy}
            className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95 hover:shadow-xl disabled:cursor-not-allowed disabled:opacity-60"
            type="button"
          >
            {updateBusy && (
              <Icon name="progress_activity" className="text-base animate-spin" />
            )}
            {updateAction === "downloading" && "Downloading..."}
            {updateAction === "installing" && "Installing..."}
            {updateAction === "restarting" && "Restarting..."}
            {!updateBusy && legacyRestartTarget && "Restart now"}
            {!updateBusy && !legacyRestartTarget && updateReady && (updateNeedsRestart ? "Install and restart" : "Install update")}
            {!updateBusy && updateAvailable && (legacyMode ? "Update now" : "Download update")}
          </button>
        )}
      </div>
    </div>
  );
}

export function VersionOverlay({
  health,
  onClose,
  open,
}: {
  health?: HealthResult | null;
  onClose: () => void;
  open: boolean;
}) {
  useEffect(() => {
    if (!open) {
      return;
    }

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, open]);

  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-[80]" role="presentation">
      <div
        className="absolute inset-0 bg-on-surface/20 backdrop-blur-sm"
        onClick={onClose}
      />

      <div className="absolute left-4 top-4 w-[min(28rem,calc(100vw-2rem))] sm:left-6 sm:top-6">
        <div
          aria-labelledby="build-details-title"
          aria-modal="true"
          className="overflow-hidden rounded-[28px] border border-outline-variant/20 bg-surface-container-high p-5 shadow-2xl"
          role="dialog"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-1">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Build Details
              </p>
              <h2 className="text-xl font-semibold text-on-surface" id="build-details-title">
                Version, build, and updates
              </h2>
              <p className="text-sm text-secondary">
                Runtime details moved out of Settings and into the sidebar trigger.
              </p>
            </div>
            <button
              aria-label="Close build details"
              className="inline-flex h-10 w-10 items-center justify-center rounded-full border border-outline-variant/20 text-secondary transition-colors hover:border-primary/20 hover:text-on-surface"
              onClick={onClose}
              type="button"
            >
              <Icon name="close" />
            </button>
          </div>

          <div className="mt-5">
            <VersionDetailsCard health={health} />
          </div>
        </div>
      </div>
    </div>
  );
}
