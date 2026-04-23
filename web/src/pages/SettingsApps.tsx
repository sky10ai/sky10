import { useCallback, useEffect, useState } from "react";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import { subscribe } from "../lib/events";
import { apps, wallet } from "../lib/rpc";
import { formatBytes, useRPC } from "../lib/useRPC";

const LIMA_APP_ID = "lima";

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
      <section className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        <div className="flex flex-col gap-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="space-y-3">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-tertiary/10 text-tertiary">
                  <Icon className="text-2xl" name="download" />
                </div>
                <div>
                  <h2 className="text-2xl font-semibold text-on-surface">
                    OWS Binary
                  </h2>
                  <p className="text-sm text-secondary">
                    Installation and update state for the Open Wallet Standard
                    executable.
                  </p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
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
                {walletUpdateAvailable && (
                  <StatusBadge icon="system_update_alt" tone="processing">
                    Update Available
                  </StatusBadge>
                )}
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
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

          <div className="grid gap-4 md:grid-cols-2">
            <div className="rounded-xl bg-surface-container p-4">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Current Version
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {walletStatus?.version ||
                  walletRelease?.current ||
                  "Not installed"}
              </p>
            </div>
            <div className="rounded-xl bg-surface-container p-4">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Latest Version
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {walletRelease?.latest || "Unavailable"}
              </p>
            </div>
            <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Management Mode
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {walletManaged
                  ? "Managed by sky10"
                  : walletInstalled
                    ? "External PATH install"
                    : "Not installed"}
              </p>
            </div>
            <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Install Location
              </p>
              <p className="mt-2 break-all font-mono text-xs text-secondary">
                {walletBinaryPath}
              </p>
            </div>
            {walletManagedPath && (
              <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Managed Install Path
                </p>
                <p className="mt-2 break-all font-mono text-xs text-secondary">
                  {walletManagedPath}
                </p>
              </div>
            )}
          </div>

          {walletInstalling && (
            <div className="rounded-xl bg-surface-container p-5">
              <div className="space-y-3">
                <p className="text-sm font-medium text-on-surface">
                  Downloading and activating the binary...
                </p>
                <div className="h-2 overflow-hidden rounded-full bg-surface-container-high">
                  <div
                    className="h-full rounded-full bg-tertiary transition-all duration-300"
                    style={{
                      width:
                        walletInstallProgress && walletInstallProgress.total > 0
                          ? `${Math.round((walletInstallProgress.downloaded / walletInstallProgress.total) * 100)}%`
                          : "0%",
                    }}
                  />
                </div>
                {walletInstallProgress && walletInstallProgress.total > 0 && (
                  <p className="text-[10px] text-secondary">
                    {formatBytes(walletInstallProgress.downloaded)}
                    {" / "}
                    {formatBytes(walletInstallProgress.total)}
                  </p>
                )}
              </div>
            </div>
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

          <div className="rounded-xl bg-surface-container p-5">
            <div className="space-y-2">
              <p className="text-sm font-medium text-on-surface">Notes</p>
              <p className="text-sm text-secondary">
                This page is intentionally about the binary only: install state,
                path, version checks, and updates.
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
            </div>
          </div>
        </div>
      </section>

      <section className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        <div className="flex flex-col gap-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="space-y-3">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-tertiary/10 text-tertiary">
                  <Icon className="text-2xl" name="terminal" />
                </div>
                <div>
                  <h2 className="text-2xl font-semibold text-on-surface">
                    Lima Runtime
                  </h2>
                  <p className="text-sm text-secondary">
                    Install and manage the Lima runtime that sky10 sandbox flows
                    use when a managed copy is available.
                  </p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
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
                {limaUpdateAvailable && (
                  <StatusBadge icon="system_update_alt" tone="processing">
                    Update Available
                  </StatusBadge>
                )}
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
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

          <div className="grid gap-4 md:grid-cols-2">
            <div className="rounded-xl bg-surface-container p-4">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Current Version
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {limaStatus?.version || limaRelease?.current || "Not installed"}
              </p>
            </div>
            <div className="rounded-xl bg-surface-container p-4">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Latest Version
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {limaRelease?.latest || "Unavailable"}
              </p>
            </div>
            <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Management Mode
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {limaManaged
                  ? "Managed by sky10"
                  : limaInstalled
                    ? "External PATH install"
                    : "Not installed"}
              </p>
            </div>
            <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Install Location
              </p>
              <p className="mt-2 break-all font-mono text-xs text-secondary">
                {limaBinaryPath}
              </p>
            </div>
            {limaManagedPath && (
              <div className="rounded-xl bg-surface-container p-4 md:col-span-2">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Managed Install Path
                </p>
                <p className="mt-2 break-all font-mono text-xs text-secondary">
                  {limaManagedPath}
                </p>
              </div>
            )}
          </div>

          {limaInstalling && (
            <div className="rounded-xl bg-surface-container p-5">
              <div className="space-y-3">
                <p className="text-sm font-medium text-on-surface">
                  Downloading and activating the binary...
                </p>
                <div className="h-2 overflow-hidden rounded-full bg-surface-container-high">
                  <div
                    className="h-full rounded-full bg-tertiary transition-all duration-300"
                    style={{
                      width:
                        limaInstallProgress && limaInstallProgress.total > 0
                          ? `${Math.round((limaInstallProgress.downloaded / limaInstallProgress.total) * 100)}%`
                          : "0%",
                    }}
                  />
                </div>
                {limaInstallProgress && limaInstallProgress.total > 0 && (
                  <p className="text-[10px] text-secondary">
                    {formatBytes(limaInstallProgress.downloaded)}
                    {" / "}
                    {formatBytes(limaInstallProgress.total)}
                  </p>
                )}
              </div>
            </div>
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

          <div className="rounded-xl bg-surface-container p-5">
            <div className="space-y-2">
              <p className="text-sm font-medium text-on-surface">Notes</p>
              <p className="text-sm text-secondary">
                Sandbox create, start, stop, delete, and terminal flows prefer
                the managed Lima binary when it is installed.
              </p>
              <p className="text-sm text-secondary">
                If no managed Lima binary is active yet, sky10 falls back to
                `limactl` from `PATH`.
              </p>
              <p className="text-sm text-secondary">
                Delete removes only the managed Lima binary under sky10 control.
                It does not touch any unrelated system install on PATH.
              </p>
              {!limaManaged && limaInstalled && (
                <p className="text-sm text-secondary">
                  The current Lima binary was discovered on PATH, so delete is
                  disabled until sky10 is the manager of that binary.
                </p>
              )}
            </div>
          </div>
        </div>
      </section>
    </SettingsPage>
  );
}
