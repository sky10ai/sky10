import { type ReactNode, useCallback, useEffect, useState } from "react";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import { subscribe } from "../lib/events";
import { apps, wallet } from "../lib/rpc";
import { formatBytes, useRPC } from "../lib/useRPC";

const LIMA_APP_ID = "lima";

const APP_LINKS = {
  ows: {
    name: "Open Wallet Standard",
    siteUrl: "https://openwallet.sh/",
    githubUrl: "https://github.com/open-wallet-standard/core",
    xHandle: "@OpenWallet",
    xUrl: "https://x.com/OpenWallet",
  },
  lima: {
    name: "Lima",
    siteUrl: "https://lima-vm.io/",
    githubUrl: "https://github.com/lima-vm/lima",
    xHandle: "@TheLimaProject",
    xUrl: "https://x.com/TheLimaProject",
  },
} as const;

type InstallProgress = {
  downloaded: number;
  total: number;
};

type DetailItem = {
  label: string;
  value: ReactNode;
  full?: boolean;
  mono?: boolean;
};

export default function SettingsApps() {
  const {
    data: walletStatus,
    error: walletError,
    refetch: refetchWallet,
  } = useRPC(() => wallet.status(), [], {
    refreshIntervalMs: 30_000,
  });
  const {
    data: walletRelease,
    error: walletReleaseError,
    refetch: refetchWalletRelease,
  } = useRPC(() => wallet.checkUpdate());
  const {
    data: limaStatus,
    error: limaError,
    refetch: refetchLima,
  } = useRPC(() => apps.status({ id: LIMA_APP_ID }), [], {
    refreshIntervalMs: 30_000,
  });
  const {
    data: limaRelease,
    error: limaReleaseError,
    refetch: refetchLimaRelease,
  } = useRPC(() => apps.checkUpdate({ id: LIMA_APP_ID }));

  const [walletInstallProgress, setWalletInstallProgress] = useState<{
    downloaded: number;
    total: number;
  } | null>(null);
  const [walletActionError, setWalletActionError] = useState<string | null>(
    null,
  );
  const [walletActionMessage, setWalletActionMessage] = useState<string | null>(
    null,
  );
  const [walletInstalling, setWalletInstalling] = useState(false);
  const [walletUninstalling, setWalletUninstalling] = useState(false);
  const [walletDetailsOpen, setWalletDetailsOpen] = useState(false);

  const [limaInstallProgress, setLimaInstallProgress] = useState<{
    downloaded: number;
    total: number;
  } | null>(null);
  const [limaActionError, setLimaActionError] = useState<string | null>(null);
  const [limaActionMessage, setLimaActionMessage] = useState<string | null>(
    null,
  );
  const [limaInstalling, setLimaInstalling] = useState(false);
  const [limaUninstalling, setLimaUninstalling] = useState(false);
  const [limaDetailsOpen, setLimaDetailsOpen] = useState(false);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event === "wallet:install:progress") {
        const d = data as { downloaded: number; total: number };
        setWalletInstallProgress(d);
        return;
      }
      if (event === "wallet:install:complete") {
        setWalletInstalling(false);
        setWalletInstallProgress(null);
        setWalletActionError(null);
        setWalletActionMessage("OWS installed.");
        refetchWallet();
        refetchWalletRelease();
        return;
      }
      if (event === "wallet:install:error") {
        const d = data as { message: string };
        setWalletInstalling(false);
        setWalletInstallProgress(null);
        setWalletActionError(d.message);
        return;
      }

      if (!data || typeof data !== "object") return;
      const payload = data as {
        id?: string;
        downloaded?: number;
        total?: number;
        message?: string;
        status?: string;
      };
      if (payload.id !== LIMA_APP_ID) return;

      if (event === "apps:install:progress") {
        setLimaInstalling(true);
        setLimaInstallProgress({
          downloaded: payload.downloaded ?? 0,
          total: payload.total ?? 0,
        });
        return;
      }
      if (event === "apps:install:complete") {
        setLimaInstalling(false);
        setLimaInstallProgress(null);
        setLimaActionError(null);
        setLimaActionMessage(
          payload.status === "already up to date"
            ? "Managed Lima already up to date."
            : "Managed Lima installed.",
        );
        refetchLima();
        refetchLimaRelease();
        return;
      }
      if (event === "apps:install:error") {
        setLimaInstalling(false);
        setLimaInstallProgress(null);
        setLimaActionError(payload.message ?? "Install failed");
      }
    });
  }, [refetchLima, refetchLimaRelease, refetchWallet, refetchWalletRelease]);

  const handleWalletInstall = useCallback(async () => {
    setWalletInstalling(true);
    setWalletActionError(null);
    setWalletActionMessage(null);
    setWalletInstallProgress(null);
    try {
      await wallet.install();
    } catch (e: unknown) {
      setWalletInstalling(false);
      setWalletActionError(e instanceof Error ? e.message : "Install failed");
    }
  }, []);

  const handleWalletDelete = useCallback(async () => {
    setWalletUninstalling(true);
    setWalletActionError(null);
    setWalletActionMessage(null);
    try {
      const result = await wallet.uninstall();
      refetchWallet();
      refetchWalletRelease();
      setWalletActionMessage(
        result.removed
          ? `Removed managed OWS binary from ${result.path}.`
          : `No managed OWS binary found at ${result.path}.`,
      );
    } catch (e: unknown) {
      setWalletActionError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setWalletUninstalling(false);
    }
  }, [refetchWallet, refetchWalletRelease]);

  const handleLimaInstall = useCallback(async () => {
    setLimaInstalling(true);
    setLimaActionError(null);
    setLimaActionMessage(null);
    setLimaInstallProgress(null);
    try {
      await apps.install({ id: LIMA_APP_ID });
    } catch (e: unknown) {
      setLimaInstalling(false);
      setLimaActionError(e instanceof Error ? e.message : "Install failed");
    }
  }, []);

  const handleLimaDelete = useCallback(async () => {
    setLimaUninstalling(true);
    setLimaActionError(null);
    setLimaActionMessage(null);
    try {
      const result = await apps.uninstall({ id: LIMA_APP_ID });
      refetchLima();
      refetchLimaRelease();
      setLimaActionMessage(
        result.removed
          ? `Removed managed Lima binary from ${result.path}.`
          : `No managed Lima binary found at ${result.path}.`,
      );
    } catch (e: unknown) {
      setLimaActionError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setLimaUninstalling(false);
    }
  }, [refetchLima, refetchLimaRelease]);

  const walletInstalled = Boolean(walletStatus?.installed);
  const walletManaged = Boolean(walletStatus?.managed);
  const walletUpdateAvailable = Boolean(
    walletInstalled && walletRelease?.available,
  );
  const walletBinaryPath = walletStatus?.bin_path || "Not installed";
  const walletManagedPath = walletStatus?.managed_path;
  const walletBusy = walletInstalling || walletUninstalling;

  const limaInstalled = Boolean(limaStatus?.installed);
  const limaManaged = Boolean(limaStatus?.managed);
  const limaUpdateAvailable = Boolean(limaInstalled && limaRelease?.available);
  const limaBinaryPath = limaStatus?.active_path || "Not installed";
  const limaManagedPath = limaStatus?.managed_path;
  const limaUnsupportedPlatform = Boolean(
    limaRelease && !limaRelease.asset_url,
  );
  const limaBusy = limaInstalling || limaUninstalling;

  return (
    <SettingsPage
      backHref="/settings"
      description="Manage helper binaries installed by sky10."
      title="Managed Apps"
      width="narrow"
    >
      <section className="order-2 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        <div className="flex flex-col gap-6">
          <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-start">
            <div className="min-w-0 space-y-3">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-tertiary/10 text-tertiary">
                  <Icon className="text-2xl" name="download" />
                </div>
                <div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                    <h2 className="text-2xl font-semibold text-on-surface">
                      OWS Binary
                    </h2>
                    <StatusBadge
                      icon={
                        walletInstalling
                          ? "downloading"
                          : walletInstalled
                            ? "check_circle"
                            : "download"
                      }
                      pulse={walletInstalling}
                      tone={
                        walletInstalling
                          ? "processing"
                          : walletInstalled
                            ? "success"
                            : "neutral"
                      }
                    >
                      {walletInstalling
                        ? "Installing"
                        : walletInstalled
                          ? "Installed"
                          : "Not Installed"}
                    </StatusBadge>
                    <AppResourceLinks app={APP_LINKS.ows} />
                  </div>
                  <p className="text-sm text-secondary">
                    Installation and update state for the Open Wallet Standard
                    executable.
                  </p>
                </div>
              </div>
              {walletUpdateAvailable && (
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge icon="system_update_alt" tone="processing">
                    Update Available
                  </StatusBadge>
                </div>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-3 md:justify-end">
              <button
                className="inline-flex items-center gap-2 rounded-full bg-tertiary px-5 py-2.5 text-sm font-semibold text-on-tertiary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                disabled={walletBusy}
                onClick={handleWalletInstall}
                type="button"
              >
                <Icon
                  name={
                    walletUpdateAvailable ? "system_update_alt" : "download"
                  }
                />
                {walletInstalling
                  ? "Installing..."
                  : walletUpdateAvailable
                    ? "Update"
                    : walletInstalled
                      ? "Reinstall"
                      : "Install"}
              </button>
              <button
                className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                disabled={!walletManaged || walletBusy}
                onClick={handleWalletDelete}
                type="button"
              >
                <Icon name="delete" />
                {walletUninstalling ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>

          <ManagedAppDetails
            controlsId="ows-managed-app-details"
            expanded={walletDetailsOpen}
            items={[
              {
                label: "Current Version",
                value:
                  walletStatus?.version ||
                  walletRelease?.current ||
                  "Not installed",
              },
              {
                label: "Latest Version",
                value: walletRelease?.latest || "Unavailable",
              },
              {
                full: true,
                label: "Management Mode",
                value: walletManaged
                  ? "Managed by sky10"
                  : walletInstalled
                    ? "External PATH install"
                    : "Not installed",
              },
              {
                full: true,
                label: "Install Location",
                mono: true,
                value: walletBinaryPath,
              },
              ...(walletManagedPath
                ? [
                    {
                      full: true,
                      label: "Managed Install Path",
                      mono: true,
                      value: walletManagedPath,
                    },
                  ]
                : []),
            ]}
            notes={
              <>
                <p className="text-sm text-secondary">
                  This page is intentionally about the binary only: install
                  state, path, version checks, and updates.
                </p>
                <p className="text-sm text-secondary">
                  Delete removes only the managed binary under sky10 control. It
                  does not touch OWS wallet data or any unrelated system install
                  on PATH.
                </p>
                {!walletManaged && walletInstalled && (
                  <p className="text-sm text-secondary">
                    The current OWS binary was discovered on PATH, so delete is
                    disabled until sky10 is the manager of that binary.
                  </p>
                )}
              </>
            }
            onToggle={() => setWalletDetailsOpen((open) => !open)}
          />

          {walletInstalling && (
            <InstallProgressPanel progress={walletInstallProgress} />
          )}

          {(walletActionError || walletError || walletReleaseError) && (
            <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
              {walletActionError ?? walletError ?? walletReleaseError}
            </div>
          )}

          {walletActionMessage && (
            <div className="rounded-xl bg-primary/10 p-4 text-sm text-primary">
              {walletActionMessage}
            </div>
          )}

        </div>
      </section>

      <section className="order-1 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        <div className="flex flex-col gap-6">
          <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-start">
            <div className="min-w-0 space-y-3">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-tertiary/10 text-tertiary">
                  <Icon className="text-2xl" name="terminal" />
                </div>
                <div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                    <h2 className="text-2xl font-semibold text-on-surface">
                      Lima Runtime
                    </h2>
                    <StatusBadge
                      icon={
                        limaInstalling
                          ? "downloading"
                          : limaInstalled
                            ? "check_circle"
                            : "download"
                      }
                      pulse={limaInstalling}
                      tone={
                        limaInstalling
                          ? "processing"
                          : limaInstalled
                            ? "success"
                            : "neutral"
                      }
                    >
                      {limaInstalling
                        ? limaUpdateAvailable
                          ? "Updating"
                          : "Installing"
                        : limaInstalled
                          ? "Installed"
                          : "Not Installed"}
                    </StatusBadge>
                    <AppResourceLinks app={APP_LINKS.lima} />
                  </div>
                  <p className="text-sm text-secondary">
                    Install and manage the Lima runtime that sky10 sandbox flows
                    use when a managed copy is available.
                  </p>
                </div>
              </div>
              {limaUpdateAvailable && (
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge icon="system_update_alt" tone="processing">
                    Update Available
                  </StatusBadge>
                </div>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-3 md:justify-end">
              <button
                className="inline-flex items-center gap-2 rounded-full bg-tertiary px-5 py-2.5 text-sm font-semibold text-on-tertiary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                disabled={limaBusy || limaUnsupportedPlatform}
                onClick={handleLimaInstall}
                type="button"
              >
                <Icon
                  name={limaUpdateAvailable ? "system_update_alt" : "download"}
                />
                {limaInstalling
                  ? limaUpdateAvailable
                    ? "Updating..."
                    : "Installing..."
                  : limaUpdateAvailable
                    ? "Update"
                    : limaInstalled
                      ? "Reinstall"
                      : "Install"}
              </button>
              <button
                className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
                disabled={!limaManaged || limaBusy}
                onClick={handleLimaDelete}
                type="button"
              >
                <Icon name="delete" />
                {limaUninstalling ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>

          <ManagedAppDetails
            controlsId="lima-managed-app-details"
            expanded={limaDetailsOpen}
            items={[
              {
                label: "Current Version",
                value:
                  limaStatus?.version ||
                  limaRelease?.current ||
                  "Not installed",
              },
              {
                label: "Latest Version",
                value: limaRelease?.latest || "Unavailable",
              },
              {
                full: true,
                label: "Management Mode",
                value: limaManaged
                  ? "Managed by sky10"
                  : limaInstalled
                    ? "External PATH install"
                    : "Not installed",
              },
              {
                full: true,
                label: "Install Location",
                mono: true,
                value: limaBinaryPath,
              },
              ...(limaManagedPath
                ? [
                    {
                      full: true,
                      label: "Managed Install Path",
                      mono: true,
                      value: limaManagedPath,
                    },
                  ]
                : []),
            ]}
            notes={
              <>
                <p className="text-sm text-secondary">
                  Sandbox create, start, stop, delete, and terminal flows prefer
                  the managed Lima binary when it is installed.
                </p>
                <p className="text-sm text-secondary">
                  If no managed Lima binary is active yet, sky10 falls back to
                  `limactl` from `PATH`.
                </p>
                <p className="text-sm text-secondary">
                  Delete removes only the managed Lima binary under sky10
                  control. It does not touch any unrelated system install on
                  PATH.
                </p>
                {!limaManaged && limaInstalled && (
                  <p className="text-sm text-secondary">
                    The current Lima binary was discovered on PATH, so delete is
                    disabled until sky10 is the manager of that binary.
                  </p>
                )}
              </>
            }
            onToggle={() => setLimaDetailsOpen((open) => !open)}
          />

          {limaInstalling && (
            <InstallProgressPanel progress={limaInstallProgress} />
          )}

          {(limaActionError || limaError || limaReleaseError) && (
            <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
              {limaActionError ?? limaError ?? limaReleaseError}
            </div>
          )}

          {!limaActionError &&
            !limaError &&
            !limaReleaseError &&
            limaUnsupportedPlatform && (
              <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
                A managed Lima bundle is not available for this platform yet.
              </div>
            )}

          {limaActionMessage && (
            <div className="rounded-xl bg-primary/10 p-4 text-sm text-primary">
              {limaActionMessage}
            </div>
          )}

        </div>
      </section>
    </SettingsPage>
  );
}

function ManagedAppDetails({
  controlsId,
  expanded,
  items,
  notes,
  onToggle,
}: {
  controlsId: string;
  expanded: boolean;
  items: DetailItem[];
  notes: ReactNode;
  onToggle: () => void;
}) {
  return (
    <>
      <AppDetailsToggle
        controlsId={controlsId}
        expanded={expanded}
        onToggle={onToggle}
      />

      {expanded && (
        <div className="space-y-4" id={controlsId}>
          <div className="grid gap-4 md:grid-cols-2">
            {items.map((item) => (
              <DetailTile key={item.label} {...item} />
            ))}
          </div>

          <div className="rounded-xl bg-surface-container p-5">
            <div className="space-y-2">
              <p className="text-sm font-medium text-on-surface">Notes</p>
              {notes}
            </div>
          </div>
        </div>
      )}
    </>
  );
}

function DetailTile({ full = false, label, mono = false, value }: DetailItem) {
  return (
    <div
      className={`rounded-xl bg-surface-container p-4 ${full ? "md:col-span-2" : ""}`}
    >
      <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
        {label}
      </p>
      <p
        className={
          mono
            ? "mt-2 break-all font-mono text-xs text-secondary"
            : "mt-2 text-sm font-semibold text-on-surface"
        }
      >
        {value}
      </p>
    </div>
  );
}

function InstallProgressPanel({
  progress,
}: {
  progress: InstallProgress | null;
}) {
  const percent =
    progress && progress.total > 0
      ? Math.round((progress.downloaded / progress.total) * 100)
      : 0;

  return (
    <div className="rounded-xl bg-surface-container p-5">
      <div className="space-y-3">
        <p className="text-sm font-medium text-on-surface">
          Downloading and activating the binary...
        </p>
        <div className="h-2 overflow-hidden rounded-full bg-surface-container-high">
          <div
            className="h-full rounded-full bg-tertiary transition-all duration-300"
            style={{ width: `${percent}%` }}
          />
        </div>
        {progress && progress.total > 0 && (
          <p className="text-[10px] text-secondary">
            {formatBytes(progress.downloaded)}
            {" / "}
            {formatBytes(progress.total)}
          </p>
        )}
      </div>
    </div>
  );
}

function AppResourceLinks({
  app,
}: {
  app: (typeof APP_LINKS)[keyof typeof APP_LINKS];
}) {
  const linkClass =
    "inline-flex h-7 w-7 items-center justify-center rounded-full border border-outline-variant/20 text-secondary transition-colors hover:border-primary/30 hover:bg-primary/10 hover:text-primary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary/40";

  return (
    <div className="flex items-center gap-1">
      <a
        aria-label={`Open ${app.name} site`}
        className={linkClass}
        href={app.siteUrl}
        rel="noopener noreferrer"
        target="_blank"
        title={`${app.name} site`}
      >
        <Icon className="text-sm" name="link" />
      </a>
      <a
        aria-label={`Open ${app.name} GitHub repository`}
        className={linkClass}
        href={app.githubUrl}
        rel="noopener noreferrer"
        target="_blank"
        title={`${app.name} GitHub repository`}
      >
        <GitHubIcon className="h-3.5 w-3.5" />
      </a>
      <a
        aria-label={`Open ${app.name} on X, ${app.xHandle}`}
        className={linkClass}
        href={app.xUrl}
        rel="noopener noreferrer"
        target="_blank"
        title={`${app.xHandle} on X`}
      >
        <XIcon className="h-3 w-3" />
      </a>
    </div>
  );
}

function AppDetailsToggle({
  controlsId,
  expanded,
  onToggle,
}: {
  controlsId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      aria-controls={controlsId}
      aria-expanded={expanded}
      className="inline-flex w-fit items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-1.5 text-xs font-semibold text-secondary transition-colors hover:border-primary/20 hover:bg-surface-container hover:text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary/40"
      onClick={onToggle}
      type="button"
    >
      <Icon
        className={`text-base transition-transform ${expanded ? "" : "-rotate-90"}`}
        name="expand_more"
      />
      Details
    </button>
  );
}

function GitHubIcon({ className = "" }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      className={className}
      fill="currentColor"
      viewBox="0 0 16 16"
    >
      <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82A7.7 7.7 0 0 1 8 3.86c.68 0 1.37.09 2.01.27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z" />
    </svg>
  );
}

function XIcon({ className = "" }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      className={className}
      fill="currentColor"
      viewBox="0 0 24 24"
    >
      <path d="M18.9 2h3.68l-8.04 9.19L24 22h-7.41l-5.81-7.59L4.14 22H.46l8.6-9.83L0 2h7.59l5.24 6.93L18.9 2Zm-1.29 18.1h2.04L6.48 3.8H4.29L17.61 20.1Z" />
    </svg>
  );
}
