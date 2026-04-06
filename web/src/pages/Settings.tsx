import { useCallback, useEffect, useState } from "react";

/** Subtract two decimal strings using BigInt to avoid float precision issues. */
function subDecimal(balance: string, amount: string, decimals: number): string {
  const toUnits = (s: string): bigint => {
    const [whole = "0", frac = ""] = s.split(".");
    const padded = frac.padEnd(decimals, "0").slice(0, decimals);
    return BigInt(whole) * BigInt(10 ** decimals) + BigInt(padded);
  };
  const result = toUnits(balance) - toUnits(amount);
  if (result <= 0n) return "0";
  const w = result / BigInt(10 ** decimals);
  const f = result % BigInt(10 ** decimals);
  if (f === 0n) return w.toString();
  const fs = f.toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${w}.${fs}`;
}
import { Icon } from "../components/Icon";
import { STORAGE_EVENT_TYPES, WALLET_EVENT_TYPES, subscribe } from "../lib/events";
import { skyfs, skylink, identity, wallet } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

export default function Settings() {
  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
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
    (d) => d.id === deviceData?.this_device
  );

  const version = health?.version ?? "";
  const versionParts = version.match(
    /^(v[\d.]+(?:-\w+)?)\s+\((\w+)\)\s+built\s+(.+)$/
  );

  // -- Wallet --
  const {
    data: walletStatus,
    error: walletError,
    refetch: refetchWallet,
  } = useRPC(() => wallet.status(), [], {
    live: WALLET_EVENT_TYPES,
    refreshIntervalMs: 30_000,
  });

  const [copied, setCopied] = useState(false);
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

  // Subscribe to wallet install progress events.
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

  // Fetch wallet details once installed and wallets exist.
  const hasWallets = walletStatus?.installed && (walletStatus.wallets ?? 0) > 0;
  const { data: walletList } = useRPC(
    () => (hasWallets ? wallet.list() : Promise.resolve(null)),
    [hasWallets],
  );
  const firstWallet = walletList?.wallets?.[0]?.name;
  const { data: walletAddr } = useRPC(
    () => (firstWallet ? wallet.address({ wallet: firstWallet }) : Promise.resolve(null)),
    [firstWallet],
  );
  const { data: walletBal, mutate: mutateBalance, pause: pauseBalance, resume: resumeBalance } = useRPC(
    () => (firstWallet ? wallet.balance({ wallet: firstWallet }) : Promise.resolve(null)),
    [firstWallet],
    { refreshIntervalMs: 30_000 },
  );

  return (
    <div className="p-12 max-w-6xl mx-auto space-y-12">
      {/* Hero title */}
      <div className="flex flex-col gap-2">
        <h2 className="text-5xl font-bold tracking-tight text-on-surface">
          Settings
        </h2>
        <p className="text-secondary max-w-md">
          Configure your vault identity, storage parameters, and network
          visibility.
        </p>
      </div>

      {/* Bento grid */}
      <div className="grid grid-cols-12 gap-6">
        {/* Identity */}
        <section className="col-span-12 lg:col-span-7 bg-surface-container-lowest rounded-xl p-8 flex flex-col justify-between group hover:shadow-xl transition-all duration-500 border border-transparent">
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

        {/* About */}
        <section className="col-span-12 lg:col-span-5 bg-surface-container-high rounded-xl p-8 flex flex-col justify-between border border-transparent">
          <div className="space-y-6">
            <div className="space-y-1">
              <h3 className="text-xl font-semibold flex items-center gap-2">
                <Icon name="info" className="text-secondary" />
                About
              </h3>
              <p className="text-sm text-secondary">
                System core information.
              </p>
            </div>
            <div className="space-y-4">
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Version</span>
                <span className="text-sm font-semibold">
                  {versionParts?.[1] ?? version}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Commit</span>
                <span className="text-xs font-mono bg-surface-container-lowest px-2 py-0.5 rounded">
                  {versionParts?.[2] ?? ""}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Build Date</span>
                <span className="text-sm">
                  {versionParts?.[3]?.split("T")[0] ?? ""}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Uptime</span>
                <span className="text-sm font-semibold">
                  {health?.uptime ?? "..."}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">RPC Clients</span>
                <span className="text-sm font-semibold">
                  {health?.rpc_clients ?? 0}
                </span>
              </div>
            </div>
          </div>
        </section>

        {/* Skylink mode */}
        {linkStatus && (
          <section className="col-span-12 lg:col-span-4 bg-primary text-white rounded-xl p-8 flex flex-col gap-8 relative overflow-hidden">
            <div className="relative z-10 space-y-2">
              <h3 className="text-xl font-bold flex items-center gap-2">
                <Icon name="wifi_tethering" />
                Skylink Mode
              </h3>
              <p className="text-xs text-primary-fixed-dim">
                Control how this vault interacts with the decentralized cloud.
              </p>
            </div>
            <div className="relative z-10 flex bg-on-primary-fixed-variant/40 p-1 rounded-full" title="Mode is set at daemon startup via sky10 serve flags">
              <div
                className={`flex-1 py-2 text-xs font-bold rounded-full text-center ${linkStatus.mode === "private" ? "bg-white text-primary" : "text-primary-fixed-dim"}`}
              >
                Private
              </div>
              <div
                className={`flex-1 py-2 text-xs font-bold rounded-full text-center ${linkStatus.mode === "network" ? "bg-white text-primary" : "text-primary-fixed-dim"}`}
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
                <div className="bg-white/10 rounded p-2 font-mono text-[10px] space-y-1">
                  {linkStatus.addrs.map((addr) => (
                    <p key={addr}>{addr}</p>
                  ))}
                </div>
              </div>
            </div>
          </section>
        )}

        {/* Wallet */}
        <section className="col-span-12 lg:col-span-4 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-6 flex flex-col">
          <div className="space-y-1">
            <h3 className="text-xl font-semibold flex items-center gap-2">
              <Icon name="account_balance_wallet" className="text-tertiary" />
              Wallet
            </h3>
            <p className="text-sm text-secondary">
              Agent payments powered by <a href="https://openwallet.sh" target="_blank" rel="noopener noreferrer" className="underline hover:text-on-surface transition-colors">OWS</a>.
            </p>
          </div>

          {/* Not installed (or RPC unavailable) */}
          {((walletStatus && !walletStatus.installed) || walletError) && !installing && (
            <div className="flex-1 flex flex-col items-center justify-center gap-4 py-4">
              <div className="w-12 h-12 rounded-full bg-tertiary/10 flex items-center justify-center">
                <Icon name="download" className="text-tertiary text-2xl" />
              </div>
              <p className="text-sm text-secondary text-center">
                Install the Open Wallet Standard to enable agent-to-agent payments on Solana.
              </p>
              <button
                onClick={handleInstall}
                className="bg-tertiary text-on-tertiary px-6 py-2.5 rounded-full text-sm font-semibold shadow-lg hover:shadow-xl transition-all active:scale-95"
              >
                Install Wallet
              </button>
              {installError && (
                <p className="text-xs text-error text-center">{installError}</p>
              )}
            </div>
          )}

          {/* Installing — progress bar */}
          {installing && (
            <div className="flex-1 flex flex-col items-center justify-center gap-4 py-4">
              <div className="w-12 h-12 rounded-full bg-tertiary/10 flex items-center justify-center">
                <Icon name="downloading" className="text-tertiary text-2xl animate-pulse" />
              </div>
              <p className="text-sm text-secondary">Installing OWS...</p>
              <div className="w-full bg-surface-container rounded-full h-2 overflow-hidden">
                <div
                  className="h-full bg-tertiary rounded-full transition-all duration-300"
                  style={{
                    width: installProgress && installProgress.total > 0
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

          {/* Installed but no wallets */}
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
                    setInstallError(e instanceof Error ? e.message : "Failed to create wallet");
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

          {/* Installed with wallets — show info */}
          {walletStatus?.installed && !installing && hasWallets && (
            <div className="space-y-4 flex-1">
              {/* Address */}
              {walletAddr?.address && (
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Solana Address
                  </label>
                  <div className="flex items-center gap-2 bg-surface-container p-3 rounded-lg group/addr cursor-pointer"
                    onClick={() => {
                      navigator.clipboard.writeText(walletAddr.address);
                      setCopied(true);
                      setTimeout(() => setCopied(false), 2000);
                    }}>
                    <code className="text-xs font-mono text-primary flex-1 truncate">
                      {walletAddr.address}
                    </code>
                    {copied ? (
                      <Icon name="check" className="text-primary text-sm" />
                    ) : (
                      <Icon name="content_copy" className="text-secondary group-hover/addr:text-primary transition-colors text-sm" />
                    )}
                  </div>
                </div>
              )}

              {/* Balances */}
              <div className="space-y-2">
                <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                  Balances
                </label>
                {(() => {
                  const tokens = walletBal?.tokens && walletBal.tokens.length > 0
                    ? walletBal.tokens
                    : [{ symbol: "SOL", balance: "0" }, { symbol: "USDC", balance: "0" }];
                  return (
                    <div className="space-y-2">
                      {tokens.map((t) => (
                        <div key={t.symbol} className="flex justify-between items-center bg-surface-container p-3 rounded-lg">
                          <span className="text-sm font-medium">{t.symbol}</span>
                          <span className="text-sm font-semibold font-mono">{t.balance}</span>
                        </div>
                      ))}
                    </div>
                  );
                })()}
              </div>

              {withdrawSuccess && (
                <p className="text-xs text-primary font-medium animate-pulse">
                  Transfer sent successfully
                </p>
              )}

              {/* Withdraw form */}
              {showWithdraw && (
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
                              const result = await wallet.maxTransfer({ wallet: firstWallet });
                              setWithdrawAmount(result.max);
                              setFeeHint(`Balance minus ${result.fee} SOL gas`);
                              setTimeout(() => setFeeHint(null), 3000);
                            } catch {
                              // Fallback to client-side estimate.
                              const tok = walletBal?.tokens?.find((t) => t.symbol === "SOL");
                              const bal = parseFloat(tok?.balance ?? "0");
                              if (bal > 0) setWithdrawAmount(String(Math.max(0, bal - 0.00001)));
                            }
                          } else {
                            const tok = walletBal?.tokens?.find((t) => t.symbol === withdrawToken);
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
                      onClick={() => { setShowWithdraw(false); setWithdrawError(null); }}
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
                        pauseBalance();
                        try {
                          const sentToken = withdrawToken;
                          // Fetch actual gas fee before sending.
                          const { fee: feeStr } = await wallet.maxTransfer({ wallet: firstWallet });
                          await wallet.transfer({
                            wallet: firstWallet,
                            to: withdrawTo,
                            amount: withdrawAmount,
                            token: withdrawToken,
                          });
                          setShowWithdraw(false);
                          setWithdrawTo("");
                          setWithdrawAmount("");
                          setWithdrawSuccess(true);
                          setTimeout(() => setWithdrawSuccess(false), 4000);
                          // Optimistic balance update with BigInt to avoid float artifacts.
                          const isSol = !sentToken || sentToken === "SOL";
                          mutateBalance((prev) => {
                            if (!prev?.tokens) return prev;
                            const updated = prev.tokens.map((t) => {
                              const decimals = t.symbol === "SOL" ? 9 : 6;
                              if (t.symbol === "SOL") {
                                const deduct = isSol ? withdrawAmount : feeStr;
                                return { ...t, balance: subDecimal(t.balance, deduct, decimals) };
                              }
                              if (!isSol && t.symbol === sentToken) {
                                return { ...t, balance: subDecimal(t.balance, withdrawAmount, decimals) };
                              }
                              return t;
                            });
                            return { ...prev, tokens: updated };
                          });
                          // Resume polling after chain confirmation window.
                          setTimeout(resumeBalance, 15_000);
                        } catch (e: unknown) {
                          setWithdrawError(e instanceof Error ? e.message : "Transfer failed");
                          resumeBalance();
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

              {/* Version + actions */}
              <div className="flex items-center justify-between pt-2">
                {walletStatus.version && (
                  <p className="text-[10px] text-secondary">{walletStatus.version}</p>
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
                        const result = await wallet.deposit({ wallet: firstWallet });
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
          )}

          {/* Loading state */}
          {!walletStatus && !walletError && !installing && (
            <div className="flex-1 flex items-center justify-center py-4">
              <p className="text-sm text-secondary">Loading...</p>
            </div>
          )}
        </section>

        {/* Authorized devices (manifest) */}
        <section className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-6">
          <div className="space-y-1">
            <h3 className="text-xl font-semibold flex items-center gap-2">
              <Icon name="devices" className="text-tertiary" />
              Authorized Devices
            </h3>
            <p className="text-sm text-secondary">
              Devices signed into this identity's manifest.
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
                    className={
                      dev.current ? "text-primary" : "text-secondary"
                    }
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
    </div>
  );
}
