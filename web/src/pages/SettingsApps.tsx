import { type ReactNode, useCallback, useEffect, useState } from "react";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import { subscribe } from "../lib/events";
import { type AppInfo, apps } from "../lib/rpc";
import { formatBytes, useRPC } from "../lib/useRPC";

type AppLinkConfig = {
  name: string;
  siteUrl?: string;
  githubUrl?: string;
  xHandle?: string;
  xUrl?: string;
};

const APP_LINKS: Record<string, AppLinkConfig> = {
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
  bun: {
    name: "Bun",
    siteUrl: "https://bun.sh/",
    githubUrl: "https://github.com/oven-sh/bun",
    xHandle: "@bunjavascript",
    xUrl: "https://x.com/bunjavascript",
  },
  zerobox: {
    name: "Zerobox",
    githubUrl: "https://github.com/afshinm/zerobox",
  },
};

const APP_ICONS: Record<string, string> = {
  ows: "download",
  lima: "terminal",
  bun: "bolt",
  zerobox: "shield",
};

const APP_DESCRIPTIONS: Record<string, string> = {
  ows: "Installation and update state for the Open Wallet Standard executable.",
  lima:
    "Install and manage the Lima runtime that sky10 sandbox flows use when a managed copy is available.",
  bun: "Install and manage the Bun JavaScript runtime that powers bundled adapters.",
  zerobox:
    "Install and manage the Zerobox sandbox launcher used by external adapters.",
};

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
  const { data: list, error: listError } = useRPC(() => apps.list(), [], {
    refreshIntervalMs: 60_000,
  });

  return (
    <SettingsPage
      backHref="/settings"
      description="Manage helper binaries installed by sky10. Wallet creation, balances, funding, and transfers live on the dedicated Wallet page."
      pinnablePageID="apps"
      title="Managed Apps"
      width="narrow"
    >
      {listError && (
        <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
          {listError}
        </div>
      )}
      {list?.apps.map((app) => (
        <ManagedAppCard app={app} key={app.id} />
      ))}
    </SettingsPage>
  );
}

function ManagedAppCard({ app }: { app: AppInfo }) {
  const { id, name } = app;
  const links = APP_LINKS[id];
  const icon = APP_ICONS[id] ?? "download";
  const description =
    APP_DESCRIPTIONS[id] ?? `Install and manage the managed ${name} binary.`;

  const {
    data: status,
    error: statusError,
    refetch: refetchStatus,
  } = useRPC(() => apps.status({ id }), [id], {
    refreshIntervalMs: 30_000,
  });
  const {
    data: release,
    error: releaseError,
    refetch: refetchRelease,
  } = useRPC(() => apps.checkUpdate({ id }), [id]);

  const [installProgress, setInstallProgress] =
    useState<InstallProgress | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const [installing, setInstalling] = useState(false);
  const [uninstalling, setUninstalling] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);

  useEffect(() => {
    return subscribe((event, data) => {
      if (!data || typeof data !== "object") return;
      const payload = data as {
        id?: string;
        downloaded?: number;
        total?: number;
        message?: string;
        status?: string;
      };
      if (payload.id !== id) return;

      if (event === "apps:install:progress") {
        setInstalling(true);
        setInstallProgress({
          downloaded: payload.downloaded ?? 0,
          total: payload.total ?? 0,
        });
        return;
      }
      if (event === "apps:install:complete") {
        setInstalling(false);
        setInstallProgress(null);
        setActionError(null);
        setActionMessage(
          payload.status === "already up to date"
            ? `Managed ${name} already up to date.`
            : `Managed ${name} installed.`,
        );
        refetchStatus();
        refetchRelease();
        return;
      }
      if (event === "apps:install:error") {
        setInstalling(false);
        setInstallProgress(null);
        setActionError(payload.message ?? "Install failed");
      }
    });
  }, [id, name, refetchStatus, refetchRelease]);

  const handleInstall = useCallback(async () => {
    setInstalling(true);
    setActionError(null);
    setActionMessage(null);
    setInstallProgress(null);
    try {
      await apps.install({ id });
    } catch (e: unknown) {
      setInstalling(false);
      setActionError(e instanceof Error ? e.message : "Install failed");
    }
  }, [id]);

  const handleDelete = useCallback(async () => {
    setUninstalling(true);
    setActionError(null);
    setActionMessage(null);
    try {
      const result = await apps.uninstall({ id });
      refetchStatus();
      refetchRelease();
      setActionMessage(
        result.removed
          ? `Removed managed ${name} binary from ${result.path}.`
          : `No managed ${name} binary found at ${result.path}.`,
      );
    } catch (e: unknown) {
      setActionError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setUninstalling(false);
    }
  }, [id, name, refetchStatus, refetchRelease]);

  const installed = Boolean(status?.installed);
  const managed = Boolean(status?.managed);
  const updateAvailable = Boolean(installed && release?.available);
  const binaryPath = status?.active_path || "Not installed";
  const managedPath = status?.managed_path;
  const unsupportedPlatform = Boolean(release && !release.asset_url);
  const busy = installing || uninstalling;

  return (
    <section className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
      <div className="flex flex-col gap-6">
        <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_auto] md:items-start">
          <div className="min-w-0 space-y-3">
            <div className="flex items-center gap-3">
              <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-tertiary/10 text-tertiary">
                <Icon className="text-2xl" name={icon} />
              </div>
              <div>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                  <h2 className="text-2xl font-semibold text-on-surface">
                    {name}
                  </h2>
                  <StatusBadge
                    icon={
                      installing
                        ? "downloading"
                        : installed
                          ? "check_circle"
                          : "download"
                    }
                    pulse={installing}
                    tone={
                      installing
                        ? "processing"
                        : installed
                          ? "success"
                          : "neutral"
                    }
                  >
                    {installing
                      ? updateAvailable
                        ? "Updating"
                        : "Installing"
                      : installed
                        ? "Installed"
                        : "Not Installed"}
                  </StatusBadge>
                  {links && <AppResourceLinks app={links} />}
                </div>
                <p className="text-sm text-secondary">{description}</p>
              </div>
            </div>
            {updateAvailable && (
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
              disabled={busy || unsupportedPlatform}
              onClick={handleInstall}
              type="button"
            >
              <Icon
                name={updateAvailable ? "system_update_alt" : "download"}
              />
              {installing
                ? updateAvailable
                  ? "Updating..."
                  : "Installing..."
                : updateAvailable
                  ? "Update"
                  : installed
                    ? "Reinstall"
                    : "Install"}
            </button>
            <button
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-secondary transition-colors disabled:opacity-50"
              disabled={!managed || busy}
              onClick={handleDelete}
              type="button"
            >
              <Icon name="delete" />
              {uninstalling ? "Deleting..." : "Delete"}
            </button>
          </div>
        </div>

        <ManagedAppDetails
          controlsId={`${id}-managed-app-details`}
          expanded={detailsOpen}
          items={[
            {
              label: "Current Version",
              value: status?.version || release?.current || "Not installed",
            },
            {
              label: "Latest Version",
              value: release?.latest || "Unavailable",
            },
            {
              full: true,
              label: "Management Mode",
              value: managed
                ? "Managed by sky10"
                : installed
                  ? "External PATH install"
                  : "Not installed",
            },
            {
              full: true,
              label: "Install Location",
              mono: true,
              value: binaryPath,
            },
            ...(managedPath
              ? [
                  {
                    full: true,
                    label: "Managed Install Path",
                    mono: true,
                    value: managedPath,
                  },
                ]
              : []),
          ]}
          notes={
            <>
              <p className="text-sm text-secondary">
                Delete removes only the managed {name} binary under sky10
                control. It does not touch any unrelated system install on
                PATH.
              </p>
              {!managed && installed && (
                <p className="text-sm text-secondary">
                  The current {name} binary was discovered on PATH, so delete
                  is disabled until sky10 is the manager of that binary.
                </p>
              )}
            </>
          }
          onToggle={() => setDetailsOpen((open) => !open)}
        />

        {installing && <InstallProgressPanel progress={installProgress} />}

        {(actionError || statusError || releaseError) && (
          <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
            {actionError ?? statusError ?? releaseError}
          </div>
        )}

        {!actionError &&
          !statusError &&
          !releaseError &&
          unsupportedPlatform && (
            <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
              A managed {name} bundle is not available for this platform yet.
            </div>
          )}

        {actionMessage && (
          <div className="rounded-xl bg-primary/10 p-4 text-sm text-primary">
            {actionMessage}
          </div>
        )}
      </div>
    </section>
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

function AppResourceLinks({ app }: { app: AppLinkConfig }) {
  const linkClass =
    "inline-flex h-7 w-7 items-center justify-center rounded-full border border-outline-variant/20 text-secondary transition-colors hover:border-primary/30 hover:bg-primary/10 hover:text-primary focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary/40";

  return (
    <div className="flex items-center gap-1">
      {app.siteUrl && (
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
      )}
      {app.githubUrl && (
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
      )}
      {app.xUrl && app.xHandle && (
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
      )}
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
