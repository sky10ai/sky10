import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import {
  STORAGE_EVENT_TYPES,
  WALLET_EVENT_TYPES,
  subscribe,
} from "../lib/events";
import { identity, skylink, wallet } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

/** Subtract two decimal strings using BigInt to avoid float precision issues. */
function subDecimal(balance: string, amount: string, decimals: number): string {
  const scale = 10n ** BigInt(decimals);
  const toUnits = (s: string): bigint => {
    const [whole = "0", frac = ""] = s.split(".");
    const padded = frac.padEnd(decimals, "0").slice(0, decimals);
    return BigInt(whole || "0") * scale + BigInt(padded || "0");
  };
  const result = toUnits(balance) - toUnits(amount);
  if (result <= 0n) return "0";
  const w = result / scale;
  const f = result % scale;
  if (f === 0n) return w.toString();
  const fs = f.toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${w}.${fs}`;
}

function addDecimal(a: string, b: string, decimals: number): string {
  const scale = 10n ** BigInt(decimals);
  const toUnits = (s: string): bigint => {
    const [whole = "0", frac = ""] = s.split(".");
    const padded = frac.padEnd(decimals, "0").slice(0, decimals);
    return BigInt(whole || "0") * scale + BigInt(padded || "0");
  };
  const result = toUnits(a) + toUnits(b);
  const w = result / scale;
  const f = result % scale;
  if (f === 0n) return w.toString();
  const fs = f.toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${w}.${fs}`;
}

type WalletTab = "solana" | "base";

const SOLANA_CHAIN = "solana";
const BASE_CHAIN = "eip155:8453";

function walletExplorerHref(chain: WalletTab, address: string) {
  return chain === "solana"
    ? `https://explorer.solana.com/address/${encodeURIComponent(address)}`
    : `https://basescan.org/address/${encodeURIComponent(address)}`;
}

const settingsTools = [
  {
    description:
      "Inspect durable queues, approvals, retries, and delivery history.",
    icon: "inbox",
    label: "Mailbox",
    to: "/settings/mailbox",
  },
  {
    description:
      "Watch peers, relays, mailbox delivery health, and network events.",
    icon: "hub",
    label: "Network",
    to: "/settings/network",
  },
  {
    description:
      "Inspect replicated keys and edit the live distributed key-value store.",
    icon: "database",
    label: "Key-Value",
    to: "/settings/kv",
  },
  {
    description:
      "Track pending sync work and recent storage activity across drives.",
    icon: "monitor_heart",
    label: "Activity",
    to: "/settings/activity",
  },
] as const;

export default function Settings() {
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idInfo } = useRPC(() => identity.show(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idDevices } = useRPC(() => identity.devices(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: deviceData } = useRPC(() => identity.deviceList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const thisDevice = (deviceData?.devices ?? []).find(
    (d) => d.id === deviceData?.this_device,
  );

  const {
    data: walletStatus,
    error: walletError,
    refetch: refetchWallet,
  } = useRPC(() => wallet.status(), [], {
    live: WALLET_EVENT_TYPES,
    refreshIntervalMs: 30_000,
  });

  const [activeWalletTab, setActiveWalletTab] = useState<WalletTab>("solana");
  const [copiedAddress, setCopiedAddress] = useState<string | null>(null);
  const [showWithdraw, setShowWithdraw] = useState(false);
  const [withdrawTo, setWithdrawTo] = useState("");
  const [withdrawAmount, setWithdrawAmount] = useState("");
  const [withdrawToken, setWithdrawToken] = useState("SOL");
  const [withdrawing, setWithdrawing] = useState(false);
  const [withdrawError, setWithdrawError] = useState<string | null>(null);
  const [withdrawSuccess, setWithdrawSuccess] = useState(false);
  const [feeHint, setFeeHint] = useState<string | null>(null);

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
      } else if (event === "wallet:install:complete") {
        setInstalling(false);
        setInstallProgress(null);
        refetchWallet();
      } else if (event === "wallet:install:error") {
        const d = data as { message: string };
        setInstalling(false);
        setInstallProgress(null);
        setInstallError(d.message);
      }
    });
  }, [refetchWallet]);

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

  const hasWallets = walletStatus?.installed && (walletStatus.wallets ?? 0) > 0;
  const { data: walletList } = useRPC(
    () => (hasWallets ? wallet.list() : Promise.resolve(null)),
    [hasWallets],
  );
  const firstWallet = walletList?.wallets?.[0]?.name;
  const { data: solanaWalletAddr } = useRPC(
    () =>
      firstWallet
        ? wallet.address({ wallet: firstWallet, chain: SOLANA_CHAIN })
        : Promise.resolve(null),
    [firstWallet],
  );
  const { data: baseWalletAddr, error: baseWalletError } = useRPC(
    () =>
      firstWallet
        ? wallet.address({ wallet: firstWallet, chain: BASE_CHAIN })
        : Promise.resolve(null),
    [firstWallet],
  );
  const {
    data: solanaWalletBal,
    mutate: mutateSolanaBalance,
    pause: pauseSolanaBalance,
    resume: resumeSolanaBalance,
  } = useRPC(
    () =>
      firstWallet
        ? wallet.balance({ wallet: firstWallet, chain: SOLANA_CHAIN })
        : Promise.resolve(null),
    [firstWallet],
    { refreshIntervalMs: 30_000 },
  );
  const {
    data: baseWalletBal,
    error: baseWalletBalError,
    mutate: mutateBaseBalance,
    pause: pauseBaseBalance,
    resume: resumeBaseBalance,
  } = useRPC(
    () =>
      firstWallet
        ? wallet.balance({ wallet: firstWallet, chain: BASE_CHAIN })
        : Promise.resolve(null),
    [firstWallet],
    { refreshIntervalMs: 30_000 },
  );

  useEffect(() => {
    setShowWithdraw(false);
    setWithdrawTo("");
    setWithdrawAmount("");
    setWithdrawError(null);
    setFeeHint(null);
    setWithdrawToken(activeWalletTab === "solana" ? "SOL" : "ETH");
  }, [activeWalletTab]);

  const handleCopyWalletAddress = useCallback((address: string) => {
    navigator.clipboard.writeText(address);
    setCopiedAddress(address);
    setTimeout(
      () =>
        setCopiedAddress((current) => (current === address ? null : current)),
      2000,
    );
  }, []);

  return (
    <SettingsPage
      description="Manage your local node, integrations, runtimes, and operational tools."
      title="Settings"
      width="wide"
    >
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Link
          className="group rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
          to="/settings/visuals"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Visuals
              </p>
              <h3 className="text-xl font-semibold text-on-surface">
                Adjust interface appearance
              </h3>
              <p className="max-w-md text-sm text-secondary">
                Choose whether sky10 follows your system theme or stays fixed in
                light or dark mode.
              </p>
            </div>
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <Icon className="text-2xl" name="palette" />
            </div>
          </div>
          <div className="mt-4 inline-flex items-center gap-2 text-sm font-semibold text-primary transition-colors group-hover:text-on-surface">
            Open Visuals
            <Icon className="text-base" name="arrow_forward" />
          </div>
        </Link>

        <Link
          className="group rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
          to="/settings/sandboxes"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Sandboxes
              </p>
              <h3 className="text-xl font-semibold text-on-surface">
                Provision isolated Linux runtimes
              </h3>
              <p className="max-w-md text-sm text-secondary">
                Start a Lima-backed Ubuntu VM, watch provisioning logs, and
                manage each sandbox from its own detail page.
              </p>
            </div>
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <Icon className="text-2xl" name="deployed_code" />
            </div>
          </div>
          <div className="mt-4 inline-flex items-center gap-2 text-sm font-semibold text-primary transition-colors group-hover:text-on-surface">
            Open Sandboxes
            <Icon className="text-base" name="arrow_forward" />
          </div>
        </Link>

        <Link
          className="group rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
          to="/settings/codex"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                ChatGPT
              </p>
              <h3 className="text-xl font-semibold text-on-surface">
                Link Codex sign-in
              </h3>
              <p className="max-w-md text-sm text-secondary">
                Link ChatGPT directly in sky10 so this device can broker
                Codex-backed work without a manual API key.
              </p>
            </div>
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
              <Icon className="text-2xl" name="chat" />
            </div>
          </div>
          <div className="mt-4 inline-flex items-center gap-2 text-sm font-semibold text-primary transition-colors group-hover:text-on-surface">
            Open ChatGPT Link
            <Icon className="text-base" name="arrow_forward" />
          </div>
        </Link>

        <Link
          className="group rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
          to="/settings/secrets"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Secrets
              </p>
              <h3 className="text-xl font-semibold text-on-surface">
                Share private values across devices
              </h3>
              <p className="max-w-md text-sm text-secondary">
                Store API keys, tokens, certs, and small encrypted artifacts
                with trusted or explicit device scope.
              </p>
            </div>
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary-fixed/60 text-on-primary-fixed-variant">
              <Icon className="text-2xl" name="key_vertical" />
            </div>
          </div>
          <div className="mt-4 inline-flex items-center gap-2 text-sm font-semibold text-on-primary-fixed-variant transition-colors group-hover:text-on-surface">
            Open Secrets
            <Icon className="text-base" name="arrow_forward" />
          </div>
        </Link>

        <Link
          className="group rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
          to="/settings/apps"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Managed Apps
              </p>
              <h3 className="text-xl font-semibold text-on-surface">
                Install helper binaries
              </h3>
              <p className="max-w-md text-sm text-secondary">
                Review and manage tools sky10 installs locally, like wallet
                binaries and future sandbox dependencies.
              </p>
            </div>
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-tertiary/10 text-tertiary">
              <Icon className="text-2xl" name="download" />
            </div>
          </div>
          <div className="mt-4 inline-flex items-center gap-2 text-sm font-semibold text-tertiary transition-colors group-hover:text-on-surface">
            Open Managed Apps
            <Icon className="text-base" name="arrow_forward" />
          </div>
        </Link>
      </div>

      <section className="space-y-4">
        <div className="space-y-1">
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Operational Views
          </p>
          <h3 className="text-xl font-semibold text-on-surface">
            Open runtime dashboards from Settings
          </h3>
          <p className="max-w-2xl text-sm text-secondary">
            Mailbox, networking, KV inspection, and sync activity now live under
            the settings area instead of the primary sidebar.
          </p>
        </div>
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          {settingsTools.map((tool) => (
            <Link
              key={tool.to}
              className="group rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
              to={tool.to}
            >
              <div className="flex items-start justify-between gap-4">
                <div className="space-y-2">
                  <h4 className="text-lg font-semibold text-on-surface">
                    {tool.label}
                  </h4>
                  <p className="text-sm text-secondary">{tool.description}</p>
                </div>
                <div className="flex h-11 w-11 items-center justify-center rounded-2xl bg-surface-container text-primary transition-colors group-hover:bg-primary/10">
                  <Icon className="text-xl" name={tool.icon} />
                </div>
              </div>
              <div className="mt-4 inline-flex items-center gap-2 text-sm font-semibold text-primary transition-colors group-hover:text-on-surface">
                Open {tool.label}
                <Icon className="text-base" name="arrow_forward" />
              </div>
            </Link>
          ))}
        </div>
      </section>

      <div className="grid grid-cols-12 gap-6">
        <section className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 flex flex-col justify-between group hover:shadow-xl transition-all duration-500 border border-transparent">
          <div className="space-y-6">
            <div className="flex justify-between items-start">
              <div className="space-y-1">
                <h3 className="text-xl font-semibold flex items-center gap-2">
                  <Icon name="fingerprint" className="text-primary" />
                  Identity
                </h3>
                <p className="text-sm text-secondary">
                  Your unique identity across all devices.
                </p>
              </div>
              <span className="bg-primary/10 text-primary px-3 py-1 rounded-full text-[10px] font-bold uppercase tracking-widest">
                Active
              </span>
            </div>
            <div className="space-y-4">
              <div className="space-y-2">
                <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                  Identity Address
                </label>
                <div className="flex items-center gap-3 bg-surface-container p-4 rounded-lg group/addr cursor-pointer">
                  <code className="text-sm font-mono text-primary flex-1 break-all">
                    {idInfo?.address ?? linkStatus?.address ?? "loading..."}
                  </code>
                  <Icon
                    name="content_copy"
                    className="text-secondary group-hover/addr:text-primary transition-colors"
                  />
                </div>
              </div>
              <div className="grid grid-cols-3 gap-4">
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Device Peer ID
                  </label>
                  <p className="font-mono text-xs text-on-surface bg-surface-container-low p-2 rounded truncate">
                    {linkStatus?.peer_id
                      ? truncAddr(linkStatus.peer_id)
                      : "..."}
                  </p>
                </div>
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Hostname
                  </label>
                  <p className="text-xs text-on-surface bg-surface-container-low p-2 rounded">
                    {thisDevice?.name ?? "..."}
                  </p>
                </div>
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Authorized Devices
                  </label>
                  <p className="text-xs text-on-surface bg-surface-container-low p-2 rounded">
                    {idInfo?.device_count ?? "..."}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        {linkStatus && (
          <section className="col-span-12 lg:col-span-4 bg-primary text-on-primary rounded-xl p-8 flex flex-col gap-8 relative overflow-hidden">
            <div className="relative z-10 space-y-2">
              <h3 className="text-xl font-bold flex items-center gap-2">
                <Icon name="wifi_tethering" />
                Skylink Mode
              </h3>
              <p className="text-xs text-primary-fixed-dim">
                Control how this vault interacts with the decentralized cloud.
              </p>
            </div>
            <div
              className="relative z-10 flex bg-on-primary-fixed-variant/40 p-1 rounded-full"
              title="Mode is set at daemon startup via sky10 serve flags"
            >
              <div
                className={`flex-1 py-2 text-xs font-bold rounded-full text-center ${linkStatus.mode === "private" ? "bg-on-primary text-primary" : "text-primary-fixed-dim"}`}
              >
                Private
              </div>
              <div
                className={`flex-1 py-2 text-xs font-bold rounded-full text-center ${linkStatus.mode === "network" ? "bg-on-primary text-primary" : "text-primary-fixed-dim"}`}
              >
                Network
              </div>
            </div>
            <div className="relative z-10 space-y-4">
              <div className="space-y-1">
                <p className="text-[10px] uppercase tracking-wider font-bold opacity-70">
                  Connected Peers
                </p>
                <p className="text-2xl font-bold">{linkStatus.peers}</p>
              </div>
              <div className="space-y-2">
                <p className="text-[10px] uppercase tracking-wider font-bold opacity-70">
                  Listen Addresses
                </p>
                <div className="bg-on-primary/10 rounded p-2 font-mono text-[10px] space-y-1">
                  {linkStatus.addrs.map((addr) => (
                    <p key={addr}>{addr}</p>
                  ))}
                </div>
              </div>
            </div>
          </section>
        )}

        <section
          className="col-span-12 lg:col-span-4 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-6 flex flex-col scroll-mt-24"
          id="wallet"
        >
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-1">
              <h3 className="text-xl font-semibold flex items-center gap-2">
                <Icon name="account_balance_wallet" className="text-tertiary" />
                Wallet
              </h3>
              <p className="text-sm text-secondary">
                Agent payments powered by{" "}
                <a
                  href="https://openwallet.sh"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline hover:text-on-surface transition-colors"
                >
                  OWS
                </a>
                .
              </p>
            </div>
            <Link
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-3 py-2 text-xs font-semibold text-secondary transition-colors hover:text-on-surface"
              to="/wallet"
            >
              Open
              <Icon className="text-sm" name="arrow_forward" />
            </Link>
          </div>

          {((walletStatus && !walletStatus.installed) || walletError) &&
            !installing && (
              <div className="flex-1 flex flex-col items-center justify-center gap-4 py-4">
                <div className="w-12 h-12 rounded-full bg-tertiary/10 flex items-center justify-center">
                  <Icon name="download" className="text-tertiary text-2xl" />
                </div>
                <p className="text-sm text-secondary text-center">
                  Install the Open Wallet Standard to enable wallet access for
                  Solana and Base.
                </p>
                <button
                  onClick={handleInstall}
                  className="bg-tertiary text-on-tertiary px-6 py-2.5 rounded-full text-sm font-semibold shadow-lg hover:shadow-xl transition-all active:scale-95"
                >
                  Install Wallet
                </button>
                {installError && (
                  <p className="text-xs text-error text-center">
                    {installError}
                  </p>
                )}
              </div>
            )}

          {installing && (
            <div className="flex-1 flex flex-col items-center justify-center gap-4 py-4">
              <div className="w-12 h-12 rounded-full bg-tertiary/10 flex items-center justify-center">
                <Icon
                  name="downloading"
                  className="text-tertiary text-2xl animate-pulse"
                />
              </div>
              <p className="text-sm text-secondary">Installing OWS...</p>
              <div className="w-full bg-surface-container rounded-full h-2 overflow-hidden">
                <div
                  className="h-full bg-tertiary rounded-full transition-all duration-300"
                  style={{
                    width:
                      installProgress && installProgress.total > 0
                        ? `${Math.round((installProgress.downloaded / installProgress.total) * 100)}%`
                        : "0%",
                  }}
                />
              </div>
              {installProgress && installProgress.total > 0 && (
                <p className="text-[10px] text-secondary">
                  {Math.round(installProgress.downloaded / 1024 / 1024)}
                  {" / "}
                  {Math.round(installProgress.total / 1024 / 1024)} MB
                </p>
              )}
            </div>
          )}

          {walletStatus?.installed && !installing && !hasWallets && (
            <div className="flex-1 flex flex-col items-center justify-center gap-4 py-4">
              <div className="w-12 h-12 rounded-full bg-primary/10 flex items-center justify-center">
                <Icon name="add_card" className="text-primary text-2xl" />
              </div>
              <p className="text-sm text-secondary text-center">
                OWS installed. Create a wallet to get started.
              </p>
              {walletStatus.version && (
                <p className="text-[10px] text-secondary">
                  {walletStatus.version}
                </p>
              )}
              <button
                onClick={async () => {
                  try {
                    await wallet.create({ name: "default" });
                    refetchWallet();
                  } catch (e: unknown) {
                    setInstallError(
                      e instanceof Error
                        ? e.message
                        : "Failed to create wallet",
                    );
                  }
                }}
                className="bg-primary text-on-primary px-6 py-2.5 rounded-full text-sm font-semibold shadow-lg hover:shadow-xl transition-all active:scale-95"
              >
                Create Wallet
              </button>
              {installError && (
                <p className="text-xs text-error text-center">{installError}</p>
              )}
            </div>
          )}

          {walletStatus?.installed && !installing && hasWallets && (
            <div className="space-y-4 flex-1">
              <div className="flex items-center justify-between gap-3">
                <p className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                  Wallet View
                </p>
                <div
                  aria-label="Wallet chain selector"
                  className="inline-flex rounded-full bg-surface-container p-1"
                  role="tablist"
                >
                  <button
                    aria-controls="wallet-solana-panel"
                    aria-selected={activeWalletTab === "solana"}
                    className={`rounded-full px-4 py-2 text-sm font-semibold transition-colors ${
                      activeWalletTab === "solana"
                        ? "bg-surface-container-lowest text-on-surface shadow-sm"
                        : "text-secondary hover:text-on-surface"
                    }`}
                    id="wallet-solana-tab"
                    onClick={() => setActiveWalletTab("solana")}
                    role="tab"
                    type="button"
                  >
                    Solana
                  </button>
                  <button
                    aria-controls="wallet-base-panel"
                    aria-selected={activeWalletTab === "base"}
                    className={`rounded-full px-4 py-2 text-sm font-semibold transition-colors ${
                      activeWalletTab === "base"
                        ? "bg-surface-container-lowest text-on-surface shadow-sm"
                        : "text-secondary hover:text-on-surface"
                    }`}
                    id="wallet-base-tab"
                    onClick={() => setActiveWalletTab("base")}
                    role="tab"
                    type="button"
                  >
                    Base
                  </button>
                </div>
              </div>

              <div
                aria-labelledby="wallet-solana-tab"
                className={`space-y-4 ${activeWalletTab === "solana" ? "" : "hidden"}`}
                id="wallet-solana-panel"
                role="tabpanel"
              >
                {solanaWalletAddr?.address && (
                  <WalletAddressCard
                    address={solanaWalletAddr.address}
                    copied={copiedAddress === solanaWalletAddr.address}
                    explorerHref={walletExplorerHref(
                      "solana",
                      solanaWalletAddr.address,
                    )}
                    label="Solana Address"
                    onCopy={handleCopyWalletAddress}
                  />
                )}

                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Balances
                  </label>
                  {(() => {
                    const tokens =
                      solanaWalletBal?.tokens &&
                      solanaWalletBal.tokens.length > 0
                        ? solanaWalletBal.tokens
                        : [
                            { symbol: "SOL", balance: "0" },
                            { symbol: "USDC", balance: "0" },
                          ];
                    return (
                      <div className="space-y-2">
                        <WalletBalanceList tokens={tokens} />
                      </div>
                    );
                  })()}
                </div>

                {withdrawSuccess && (
                  <p className="text-xs text-primary font-medium animate-pulse">
                    Transfer sent successfully
                  </p>
                )}

                {activeWalletTab === "solana" && showWithdraw && (
                  <div className="space-y-3 bg-surface-container p-4 rounded-lg">
                    <input
                      type="text"
                      placeholder="Recipient address"
                      value={withdrawTo}
                      onChange={(e) => setWithdrawTo(e.target.value)}
                      className="w-full bg-surface-container-high text-sm rounded-lg px-3 py-2 outline-none focus:ring-1 focus:ring-primary font-mono"
                    />
                    <div className="flex gap-2 min-w-0">
                      <div className="min-w-0 flex-1 relative">
                        <input
                          type="text"
                          placeholder="Amount"
                          value={withdrawAmount}
                          onChange={(e) => setWithdrawAmount(e.target.value)}
                          className="w-full bg-surface-container-high text-sm rounded-lg px-3 py-2 pr-12 outline-none focus:ring-1 focus:ring-primary font-mono"
                        />
                        <button
                          type="button"
                          onMouseDown={async (e) => {
                            e.preventDefault();
                            if (!firstWallet) return;
                            if (withdrawToken === "SOL") {
                              try {
                                const result = await wallet.maxTransfer({
                                  wallet: firstWallet,
                                  chain: SOLANA_CHAIN,
                                });
                                setWithdrawAmount(result.max);
                                setFeeHint(
                                  `Balance minus ${result.fee} SOL gas`,
                                );
                                setTimeout(() => setFeeHint(null), 3000);
                              } catch {
                                const tok = solanaWalletBal?.tokens?.find(
                                  (t) => t.symbol === "SOL",
                                );
                                const bal = parseFloat(tok?.balance ?? "0");
                                if (bal > 0)
                                  setWithdrawAmount(
                                    String(Math.max(0, bal - 0.00001)),
                                  );
                              }
                            } else {
                              const tok = solanaWalletBal?.tokens?.find(
                                (t) => t.symbol === withdrawToken,
                              );
                              setWithdrawAmount(tok?.balance ?? "0");
                            }
                          }}
                          className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] font-bold text-primary hover:text-primary/70 transition-colors z-10 px-1"
                        >
                          ALL
                        </button>
                      </div>
                      <select
                        value={withdrawToken}
                        onChange={(e) => setWithdrawToken(e.target.value)}
                        className="w-20 shrink-0 bg-surface-container-high text-sm rounded-lg px-2 py-2 outline-none focus:ring-1 focus:ring-primary"
                      >
                        <option value="SOL">SOL</option>
                        <option value="USDC">USDC</option>
                      </select>
                    </div>
                    {feeHint && (
                      <p className="text-[10px] text-secondary animate-pulse">
                        {feeHint}
                      </p>
                    )}
                    {withdrawError && (
                      <p className="text-xs text-error">{withdrawError}</p>
                    )}
                    <div className="flex gap-2 justify-end">
                      <button
                        onClick={() => {
                          setShowWithdraw(false);
                          setWithdrawError(null);
                        }}
                        className="text-xs font-semibold text-secondary hover:text-on-surface px-3 py-1.5 rounded-full transition-colors"
                      >
                        Cancel
                      </button>
                      <button
                        disabled={withdrawing || !withdrawTo || !withdrawAmount}
                        onClick={async () => {
                          if (!firstWallet) return;
                          setWithdrawing(true);
                          setWithdrawError(null);
                          pauseSolanaBalance();
                          try {
                            const sentToken = withdrawToken;
                            const { fee: feeStr } = await wallet.maxTransfer({
                              wallet: firstWallet,
                              chain: SOLANA_CHAIN,
                            });
                            await wallet.transfer({
                              wallet: firstWallet,
                              chain: SOLANA_CHAIN,
                              to: withdrawTo,
                              amount: withdrawAmount,
                              token: withdrawToken,
                            });
                            setShowWithdraw(false);
                            setWithdrawTo("");
                            setWithdrawAmount("");
                            setWithdrawSuccess(true);
                            setTimeout(() => setWithdrawSuccess(false), 4000);
                            const isSol = !sentToken || sentToken === "SOL";
                            mutateSolanaBalance((prev) => {
                              if (!prev?.tokens) return prev;
                              const updated = prev.tokens.map((t) => {
                                const decimals = t.symbol === "SOL" ? 9 : 6;
                                if (t.symbol === "SOL") {
                                  const deduct = isSol
                                    ? addDecimal(
                                        withdrawAmount,
                                        feeStr,
                                        decimals,
                                      )
                                    : feeStr;
                                  return {
                                    ...t,
                                    balance: subDecimal(
                                      t.balance,
                                      deduct,
                                      decimals,
                                    ),
                                  };
                                }
                                if (!isSol && t.symbol === sentToken) {
                                  return {
                                    ...t,
                                    balance: subDecimal(
                                      t.balance,
                                      withdrawAmount,
                                      decimals,
                                    ),
                                  };
                                }
                                return t;
                              });
                              return { ...prev, tokens: updated };
                            });
                            setTimeout(resumeSolanaBalance, 15_000);
                          } catch (e: unknown) {
                            setWithdrawError(
                              e instanceof Error
                                ? e.message
                                : "Transfer failed",
                            );
                            resumeSolanaBalance();
                          } finally {
                            setWithdrawing(false);
                          }
                        }}
                        className="text-xs font-semibold bg-primary text-on-primary px-4 py-1.5 rounded-full disabled:opacity-40 transition-all active:scale-95"
                      >
                        {withdrawing ? "Sending..." : "Send"}
                      </button>
                    </div>
                  </div>
                )}

                <div className="flex items-center justify-between pt-2">
                  {walletStatus.version && (
                    <p className="text-[10px] text-secondary">
                      {walletStatus.version}
                    </p>
                  )}
                  <div className="flex items-center gap-3">
                    <button
                      onClick={() => setShowWithdraw(!showWithdraw)}
                      className="text-xs font-semibold text-primary hover:text-primary/80 transition-colors flex items-center gap-1"
                    >
                      <Icon name="send" className="text-sm" />
                      Send
                    </button>
                    <button
                      onClick={async () => {
                        if (!firstWallet) return;
                        try {
                          const result = await wallet.deposit({
                            wallet: firstWallet,
                            chain: SOLANA_CHAIN,
                          });
                          if (result.url) window.open(result.url, "_blank");
                        } catch {
                          // deposit may not return a URL on all platforms
                        }
                      }}
                      className="text-xs font-semibold text-primary hover:text-primary/80 transition-colors flex items-center gap-1"
                    >
                      <Icon name="add" className="text-sm" />
                      Fund
                    </button>
                  </div>
                </div>
              </div>

              <div
                aria-labelledby="wallet-base-tab"
                className={`space-y-4 ${activeWalletTab === "base" ? "" : "hidden"}`}
                id="wallet-base-panel"
                role="tabpanel"
              >
                {baseWalletAddr?.address ? (
                  <WalletAddressCard
                    address={baseWalletAddr.address}
                    copied={copiedAddress === baseWalletAddr.address}
                    explorerHref={walletExplorerHref(
                      "base",
                      baseWalletAddr.address,
                    )}
                    label="Base Address"
                    onCopy={handleCopyWalletAddress}
                  />
                ) : (
                  <div className="rounded-lg bg-surface-container p-4 text-sm text-secondary">
                    {baseWalletError ||
                      "No Base address is available for this wallet yet."}
                  </div>
                )}

                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Balances
                  </label>
                  {(() => {
                    const tokens =
                      baseWalletBal?.tokens && baseWalletBal.tokens.length > 0
                        ? baseWalletBal.tokens
                        : [
                            { symbol: "ETH", balance: "0" },
                            { symbol: "USDC", balance: "0" },
                          ];
                    return (
                      <div className="space-y-2">
                        <WalletBalanceList tokens={tokens} />
                      </div>
                    );
                  })()}
                </div>

                {baseWalletBalError && (
                  <p className="text-xs text-error">{baseWalletBalError}</p>
                )}

                {withdrawSuccess && (
                  <p className="text-xs text-primary font-medium animate-pulse">
                    Transfer sent successfully
                  </p>
                )}

                {activeWalletTab === "base" && showWithdraw && (
                  <div className="space-y-3 bg-surface-container p-4 rounded-lg">
                    <input
                      type="text"
                      placeholder="Recipient address"
                      value={withdrawTo}
                      onChange={(e) => setWithdrawTo(e.target.value)}
                      className="w-full bg-surface-container-high text-sm rounded-lg px-3 py-2 outline-none focus:ring-1 focus:ring-primary font-mono"
                    />
                    <div className="flex gap-2 min-w-0">
                      <div className="min-w-0 flex-1 relative">
                        <input
                          type="text"
                          placeholder="Amount"
                          value={withdrawAmount}
                          onChange={(e) => setWithdrawAmount(e.target.value)}
                          className="w-full bg-surface-container-high text-sm rounded-lg px-3 py-2 pr-12 outline-none focus:ring-1 focus:ring-primary font-mono"
                        />
                        <button
                          type="button"
                          onMouseDown={async (e) => {
                            e.preventDefault();
                            if (!firstWallet) return;
                            if (withdrawToken === "ETH") {
                              try {
                                const result = await wallet.maxTransfer({
                                  wallet: firstWallet,
                                  chain: BASE_CHAIN,
                                });
                                setWithdrawAmount(result.max);
                                setFeeHint(
                                  `Balance minus ${result.fee} ETH gas`,
                                );
                                setTimeout(() => setFeeHint(null), 3000);
                              } catch {
                                const tok = baseWalletBal?.tokens?.find(
                                  (t) => t.symbol === "ETH",
                                );
                                setWithdrawAmount(tok?.balance ?? "0");
                              }
                            } else {
                              const tok = baseWalletBal?.tokens?.find(
                                (t) => t.symbol === withdrawToken,
                              );
                              setWithdrawAmount(tok?.balance ?? "0");
                            }
                          }}
                          className="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] font-bold text-primary hover:text-primary/70 transition-colors z-10 px-1"
                        >
                          ALL
                        </button>
                      </div>
                      <select
                        value={withdrawToken}
                        onChange={(e) => setWithdrawToken(e.target.value)}
                        className="w-20 shrink-0 bg-surface-container-high text-sm rounded-lg px-2 py-2 outline-none focus:ring-1 focus:ring-primary"
                      >
                        <option value="ETH">ETH</option>
                        <option value="USDC">USDC</option>
                      </select>
                    </div>
                    {feeHint && (
                      <p className="text-[10px] text-secondary animate-pulse">
                        {feeHint}
                      </p>
                    )}
                    {withdrawError && (
                      <p className="text-xs text-error">{withdrawError}</p>
                    )}
                    <div className="flex gap-2 justify-end">
                      <button
                        onClick={() => {
                          setShowWithdraw(false);
                          setWithdrawError(null);
                        }}
                        className="text-xs font-semibold text-secondary hover:text-on-surface px-3 py-1.5 rounded-full transition-colors"
                      >
                        Cancel
                      </button>
                      <button
                        disabled={withdrawing || !withdrawTo || !withdrawAmount}
                        onClick={async () => {
                          if (!firstWallet) return;
                          setWithdrawing(true);
                          setWithdrawError(null);
                          pauseBaseBalance();
                          try {
                            const sentToken = withdrawToken;
                            const { fee: feeStr } = await wallet.maxTransfer({
                              wallet: firstWallet,
                              chain: BASE_CHAIN,
                            });
                            await wallet.transfer({
                              wallet: firstWallet,
                              chain: BASE_CHAIN,
                              to: withdrawTo,
                              amount: withdrawAmount,
                              token: withdrawToken,
                            });
                            setShowWithdraw(false);
                            setWithdrawTo("");
                            setWithdrawAmount("");
                            setWithdrawSuccess(true);
                            setTimeout(() => setWithdrawSuccess(false), 4000);
                            const isEth = !sentToken || sentToken === "ETH";
                            mutateBaseBalance((prev) => {
                              if (!prev?.tokens) return prev;
                              const updated = prev.tokens.map((t) => {
                                const decimals = t.symbol === "ETH" ? 18 : 6;
                                if (t.symbol === "ETH") {
                                  const deduct = isEth
                                    ? addDecimal(
                                        withdrawAmount,
                                        feeStr,
                                        decimals,
                                      )
                                    : feeStr;
                                  return {
                                    ...t,
                                    balance: subDecimal(
                                      t.balance,
                                      deduct,
                                      decimals,
                                    ),
                                  };
                                }
                                if (!isEth && t.symbol === sentToken) {
                                  return {
                                    ...t,
                                    balance: subDecimal(
                                      t.balance,
                                      withdrawAmount,
                                      decimals,
                                    ),
                                  };
                                }
                                return t;
                              });
                              return { ...prev, tokens: updated };
                            });
                            setTimeout(resumeBaseBalance, 15_000);
                          } catch (e: unknown) {
                            setWithdrawError(
                              e instanceof Error
                                ? e.message
                                : "Transfer failed",
                            );
                            resumeBaseBalance();
                          } finally {
                            setWithdrawing(false);
                          }
                        }}
                        className="text-xs font-semibold bg-primary text-on-primary px-4 py-1.5 rounded-full disabled:opacity-40 transition-all active:scale-95"
                      >
                        {withdrawing ? "Sending..." : "Send"}
                      </button>
                    </div>
                  </div>
                )}

                <div className="flex items-center justify-between pt-2">
                  {walletStatus.version && (
                    <p className="text-[10px] text-secondary">
                      {walletStatus.version}
                    </p>
                  )}
                  <div className="flex items-center gap-3">
                    <button
                      onClick={() => setShowWithdraw(!showWithdraw)}
                      className="text-xs font-semibold text-primary hover:text-primary/80 transition-colors flex items-center gap-1"
                    >
                      <Icon name="send" className="text-sm" />
                      Send
                    </button>
                    <button
                      onClick={async () => {
                        if (!firstWallet) return;
                        try {
                          const result = await wallet.deposit({
                            wallet: firstWallet,
                            chain: BASE_CHAIN,
                          });
                          if (result.url) window.open(result.url, "_blank");
                        } catch {
                          // deposit may not return a URL on all platforms
                        }
                      }}
                      className="text-xs font-semibold text-primary hover:text-primary/80 transition-colors flex items-center gap-1"
                    >
                      <Icon name="add" className="text-sm" />
                      Fund
                    </button>
                  </div>
                </div>
              </div>
            </div>
          )}

          {!walletStatus && !walletError && !installing && (
            <div className="flex-1 flex items-center justify-center py-4">
              <p className="text-sm text-secondary">Loading...</p>
            </div>
          )}
        </section>

        <section className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-6">
          <div className="space-y-1">
            <h3 className="text-xl font-semibold flex items-center gap-2">
              <Icon name="devices" className="text-tertiary" />
              Authorized Devices
            </h3>
            <p className="text-sm text-secondary">
              Devices signed into this identity&apos;s manifest.
            </p>
          </div>
          <div className="space-y-3">
            {(idDevices?.devices ?? []).map((dev) => (
              <div
                key={dev.public_key}
                className={`flex items-center justify-between p-4 rounded-lg ${
                  dev.current
                    ? "bg-primary/5 border border-primary/20"
                    : "bg-surface-container"
                }`}
              >
                <div className="flex items-center gap-3">
                  <Icon
                    name={dev.current ? "laptop_mac" : "devices_other"}
                    className={dev.current ? "text-primary" : "text-secondary"}
                  />
                  <div>
                    <p className="text-sm font-medium">
                      {dev.name}
                      {dev.current && (
                        <span className="ml-2 text-[10px] font-bold uppercase tracking-widest text-primary bg-primary/10 px-2 py-0.5 rounded-full">
                          This Device
                        </span>
                      )}
                    </p>
                    <p className="text-xs text-secondary font-mono">
                      {dev.public_key.slice(0, 16)}...
                    </p>
                  </div>
                </div>
                <p className="text-xs text-secondary">
                  Added {dev.added_at.split("T")[0]}
                </p>
              </div>
            ))}
            {(idDevices?.devices ?? []).length === 0 && (
              <p className="text-sm text-secondary py-4 text-center">
                Loading device manifest...
              </p>
            )}
          </div>
        </section>
      </div>
    </SettingsPage>
  );
}

function WalletBalanceList({
  tokens,
}: {
  tokens: Array<{ symbol: string; balance: string }>;
}) {
  return (
    <>
      {tokens.map((t) => (
        <div
          key={t.symbol}
          className="flex justify-between items-center bg-surface-container p-3 rounded-lg"
        >
          <span className="text-sm font-medium">{t.symbol}</span>
          <span className="text-sm font-semibold font-mono">{t.balance}</span>
        </div>
      ))}
    </>
  );
}

function WalletAddressCard({
  address,
  copied,
  explorerHref,
  label,
  onCopy,
}: {
  address: string;
  copied: boolean;
  explorerHref: string;
  label: string;
  onCopy: (address: string) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
        {label}
      </label>
      <div className="flex items-center gap-2 bg-surface-container p-3 rounded-lg">
        <code className="text-xs font-mono text-primary flex-1 truncate">
          {address}
        </code>
        <a
          href={explorerHref}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex h-8 w-8 items-center justify-center rounded-full text-secondary transition-colors hover:text-primary"
          title="Open in block explorer"
        >
          <Icon name="link" className="text-sm" />
        </a>
        <button
          type="button"
          onClick={() => onCopy(address)}
          className="inline-flex h-8 w-8 items-center justify-center rounded-full text-secondary transition-colors hover:text-primary"
          title="Copy address"
        >
          {copied ? (
            <Icon name="check" className="text-primary text-sm" />
          ) : (
            <Icon name="content_copy" className="text-sm" />
          )}
        </button>
      </div>
    </div>
  );
}
