import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { WALLET_EVENT_TYPES, subscribe } from "../lib/events";
import { wallet } from "../lib/rpc";
import { formatBytes, useRPC } from "../lib/useRPC";

export default function SettingsApps() {
  const {
    data: walletStatus,
    error: walletError,
    refetch: refetchWallet,
  } = useRPC(() => wallet.status(), [], {
    live: WALLET_EVENT_TYPES,
    refreshIntervalMs: 30_000,
  });
  const {
    data: walletRelease,
    error: walletReleaseError,
    refetch: refetchWalletRelease,
  } = useRPC(() => wallet.checkUpdate(), [], {
    live: WALLET_EVENT_TYPES,
    refreshIntervalMs: 30_000,
  });

  const [installProgress, setInstallProgress] = useState<{
    downloaded: number;
    total: number;
  } | null>(null);
  const [installError, setInstallError] = useState<string | null>(null);
  const [installing, setInstalling] = useState(false);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event === "wallet:install:progress") {
        const d = data as { downloaded: number; total: number };
        setInstallProgress(d);
        return;
      }
      if (event === "wallet:install:complete") {
        setInstalling(false);
        setInstallProgress(null);
        setInstallError(null);
        refetchWallet();
        refetchWalletRelease();
        return;
      }
      if (event === "wallet:install:error") {
        const d = data as { message: string };
        setInstalling(false);
        setInstallProgress(null);
        setInstallError(d.message);
      }
    });
  }, [refetchWallet, refetchWalletRelease]);

  const handleInstall = useCallback(async () => {
    setInstalling(true);
    setInstallError(null);
    setInstallProgress(null);
    try {
      await wallet.install();
    } catch (e: unknown) {
      setInstalling(false);
      setInstallError(e instanceof Error ? e.message : "Install failed");
    }
  }, []);

  const installed = Boolean(walletStatus?.installed);
  const updateAvailable = Boolean(installed && walletRelease?.available);
  const binaryPath = walletStatus?.bin_path || "Not installed";

  return (
    <div className="p-12 max-w-5xl mx-auto space-y-10">
      <PageHeader
        eyebrow="Power User"
        title="Managed Apps"
        description="Binary management only. Wallet creation, balances, funding, and transfers stay on the main Settings page."
        actions={(
          <Link
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
            to="/settings"
          >
            <Icon className="text-base" name="arrow_back" />
            Back to Settings
          </Link>
        )}
      />

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
                    Installation and update state for the Open Wallet Standard executable.
                  </p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge
                  icon={installing ? "downloading" : installed ? "check_circle" : "download"}
                  pulse={installing}
                  tone={installing ? "processing" : installed ? "success" : "neutral"}
                >
                  {installing ? "Installing" : installed ? "Installed" : "Not Installed"}
                </StatusBadge>
                {updateAvailable && (
                  <StatusBadge icon="system_update_alt" tone="processing">
                    Update Available
                  </StatusBadge>
                )}
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
              <button
                className="inline-flex items-center gap-2 rounded-full bg-tertiary px-5 py-2.5 text-sm font-semibold text-on-tertiary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                disabled={installing}
                onClick={handleInstall}
                type="button"
              >
                <Icon name={updateAvailable ? "system_update_alt" : "download"} />
                {updateAvailable ? "Update" : installed ? "Reinstall" : "Install"}
              </button>
              <button
                className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-secondary opacity-50"
                disabled
                type="button"
              >
                <Icon name="delete" />
                Delete
              </button>
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div className="rounded-xl bg-surface-container p-4">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Current Version
              </p>
              <p className="mt-2 text-sm font-semibold text-on-surface">
                {walletStatus?.version || walletRelease?.current || "Not installed"}
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
                Install Location
              </p>
              <p className="mt-2 break-all font-mono text-xs text-secondary">
                {binaryPath}
              </p>
            </div>
          </div>

          {installing && (
            <div className="rounded-xl bg-surface-container p-5">
              <div className="space-y-3">
                <p className="text-sm font-medium text-on-surface">
                  Downloading and activating the binary...
                </p>
                <div className="h-2 overflow-hidden rounded-full bg-surface-container-high">
                  <div
                    className="h-full rounded-full bg-tertiary transition-all duration-300"
                    style={{
                      width: installProgress && installProgress.total > 0
                        ? `${Math.round((installProgress.downloaded / installProgress.total) * 100)}%`
                        : "0%",
                    }}
                  />
                </div>
                {installProgress && installProgress.total > 0 && (
                  <p className="text-[10px] text-secondary">
                    {formatBytes(installProgress.downloaded)}
                    {" / "}
                    {formatBytes(installProgress.total)}
                  </p>
                )}
              </div>
            </div>
          )}

          {(installError || walletError || walletReleaseError) && (
            <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
              {installError ?? walletError ?? walletReleaseError}
            </div>
          )}

          <div className="rounded-xl bg-surface-container p-5">
            <div className="space-y-2">
              <p className="text-sm font-medium text-on-surface">
                Notes
              </p>
              <p className="text-sm text-secondary">
                This page is intentionally about the binary only: install state, path, version checks, and updates.
              </p>
              <p className="text-sm text-secondary">
                Delete is shown here because it belongs in app management, but uninstall is not wired in the backend yet.
              </p>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
