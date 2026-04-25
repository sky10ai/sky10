import { useCallback, useEffect, useState } from "react";
import { QRCodeSVG } from "qrcode.react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { RelativeTime } from "../components/RelativeTime";
import { StatusBadge } from "../components/StatusBadge";
import { WALLET_EVENT_TYPES, subscribe } from "../lib/events";
import { wallet, type WalletBalance } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";
import {
  WALLET_CHAIN_OPTIONS,
  WalletChainLogo,
  getWalletChain,
  getWalletChainForActivity,
  type WalletChainId,
} from "../lib/walletChains";
import {
  MAX_WALLET_ACTIVITY_ITEMS,
  type WalletActivityChain,
  type WalletActivityEntry,
} from "../lib/walletActivity";

type WalletTab = WalletChainId;
type ActivityChainFilter = "all" | WalletActivityChain;
type ActivityTimeRange = "24h" | "30d" | "7d" | "all";
type WalletPane = "receive" | "send";

interface AssetCard {
  balance: string;
  chain: WalletTab;
  key: string;
  symbol: string;
}

const RECEIVE_QR_SIZE = 224;
const RECEIVE_QR_WORDMARK_HEIGHT = 32;
const RECEIVE_QR_WORDMARK_WIDTH = 88;
const RECEIVE_QR_WORDMARK_SRC = `data:image/svg+xml;utf8,${encodeURIComponent(`
<svg xmlns="http://www.w3.org/2000/svg" width="176" height="64" viewBox="0 0 176 64">
  <text x="88" y="43" text-anchor="middle" fill="#2563eb" font-family="Verdana, sans-serif" font-size="38" font-weight="700" letter-spacing="-1.5">sky10</text>
</svg>
`)}`;
const STABLECOIN_SYMBOLS = new Set(["DAI", "FDUSD", "PYUSD", "USDC", "USDT"]);
const usdFormatter = new Intl.NumberFormat("en-US", {
  currency: "USD",
  maximumFractionDigits: 2,
  minimumFractionDigits: 2,
  style: "currency",
});

function subDecimal(balance: string, amount: string, decimals: number): string {
  const scale = 10n ** BigInt(decimals);
  const toUnits = (s: string): bigint => {
    const [whole = "0", frac = ""] = s.split(".");
    const padded = frac.padEnd(decimals, "0").slice(0, decimals);
    return BigInt(whole || "0") * scale + BigInt(padded || "0");
  };
  const result = toUnits(balance) - toUnits(amount);
  if (result <= 0n) return "0";
  const whole = result / scale;
  const frac = result % scale;
  if (frac === 0n) return whole.toString();
  const fracString = frac.toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${whole}.${fracString}`;
}

function addDecimal(a: string, b: string, decimals: number): string {
  const scale = 10n ** BigInt(decimals);
  const toUnits = (s: string): bigint => {
    const [whole = "0", frac = ""] = s.split(".");
    const padded = frac.padEnd(decimals, "0").slice(0, decimals);
    return BigInt(whole || "0") * scale + BigInt(padded || "0");
  };
  const result = toUnits(a) + toUnits(b);
  const whole = result / scale;
  const frac = result % scale;
  if (frac === 0n) return whole.toString();
  const fracString = frac.toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${whole}.${fracString}`;
}

function ReceiveQRCode({ address, chain }: { address: string; chain: WalletTab }) {
  const walletChain = getWalletChain(chain);

  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container p-4">
      <div className="mx-auto w-fit rounded-[1.75rem] bg-white p-3 shadow-inner ring-1 ring-black/5">
        <QRCodeSVG
          bgColor="#ffffff"
          fgColor="#111827"
          imageSettings={{
            excavate: true,
            height: RECEIVE_QR_WORDMARK_HEIGHT,
            src: RECEIVE_QR_WORDMARK_SRC,
            width: RECEIVE_QR_WORDMARK_WIDTH,
          }}
          level="H"
          marginSize={4}
          size={RECEIVE_QR_SIZE}
          title={`${walletChain.label} receive address QR code`}
          value={walletChain.receivePayload(address)}
        />
      </div>
    </div>
  );
}

function fallbackTokensForChain(chain: WalletTab) {
  return getWalletChain(chain).fallbackTokens;
}

function assetSortWeight(symbol: string) {
  const upper = symbol.toUpperCase();
  if (STABLECOIN_SYMBOLS.has(upper)) return 0;
  if (upper === "SOL" || upper === "ETH") return 1;
  return 2;
}

function buildAssetCards(walletBalances: Record<WalletChainId, WalletBalance | null>): AssetCard[] {
  const cards = new Map<string, AssetCard>();

  WALLET_CHAIN_OPTIONS.forEach((walletChain) => {
    const chain = walletChain.id;
    const tokens = walletBalances[chain]?.tokens ?? walletChain.fallbackTokens;

    tokens.forEach((token) => {
      const symbol = token.symbol.toUpperCase();
      const key = `${chain}:${symbol}`;
      if (cards.has(key)) return;

      cards.set(key, {
        balance: token.balance,
        chain,
        key,
        symbol,
      });
    });
  });

  return Array.from(cards.values()).sort((left, right) => {
    const weight = assetSortWeight(left.symbol) - assetSortWeight(right.symbol);
    if (weight !== 0) return weight;
    if (left.chain !== right.chain) {
      return getWalletChain(left.chain).order - getWalletChain(right.chain).order;
    }
    return left.symbol.localeCompare(right.symbol);
  });
}

function parseAmount(value: string) {
  const parsed = Number.parseFloat(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatCurrency(value: number) {
  return usdFormatter.format(value);
}

function formatTokenBalance(value: string) {
  const parsed = Number.parseFloat(value);
  if (!Number.isFinite(parsed)) return value;

  return new Intl.NumberFormat("en-US", {
    maximumFractionDigits: parsed >= 1000 ? 2 : 6,
  }).format(parsed);
}

function approximateStablecoinTotal(cards: AssetCard[]) {
  return cards.reduce((total, card) => {
    if (!STABLECOIN_SYMBOLS.has(card.symbol.toUpperCase())) return total;
    return total + parseAmount(card.balance);
  }, 0);
}

function absoluteDateTime(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
}

function activityWithinRange(value: string, range: ActivityTimeRange) {
  if (range === "all") return true;

  const createdAt = new Date(value).getTime();
  if (Number.isNaN(createdAt)) return false;

  const elapsed = Date.now() - createdAt;
  const maxAgeMs =
    range === "24h"
      ? 24 * 60 * 60 * 1000
      : range === "7d"
        ? 7 * 24 * 60 * 60 * 1000
        : 30 * 24 * 60 * 60 * 1000;

  return elapsed <= maxAgeMs;
}

function activityStatusTone(status: string) {
  const normalized = status.toLowerCase();
  if (normalized.includes("fail")) return "danger" as const;
  if (normalized.includes("open") || normalized.includes("submit")) return "processing" as const;
  return "success" as const;
}

function activityIcon(kind: WalletActivityEntry["kind"]) {
  return kind === "fund" ? "add_card" : "north_east";
}

function createActivityID(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export default function Wallet() {
  const {
    data: walletStatus,
    error: walletError,
    loading: walletStatusLoading,
    refetch: refetchWallet,
  } = useRPC(() => wallet.status(), [], {
    live: WALLET_EVENT_TYPES,
    refreshIntervalMs: 30_000,
  });

  const [activeWalletTab, setActiveWalletTab] = useState<WalletTab>("solana");
  const [copiedAddress, setCopiedAddress] = useState<string | null>(null);
  const [activePane, setActivePane] = useState<WalletPane | null>(null);
  const [sendTo, setSendTo] = useState("");
  const [sendAmount, setSendAmount] = useState("");
  const [sendToken, setSendToken] = useState("SOL");
  const [sending, setSending] = useState(false);
  const [sendError, setSendError] = useState<string | null>(null);
  const [feeHint, setFeeHint] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const [activitySearch, setActivitySearch] = useState("");
  const [activityChainFilter, setActivityChainFilter] = useState<ActivityChainFilter>("all");
  const [activityTimeRange, setActivityTimeRange] = useState<ActivityTimeRange>("all");

  const [installProgress, setInstallProgress] = useState<{
    downloaded: number;
    total: number;
  } | null>(null);
  const [installError, setInstallError] = useState<string | null>(null);
  const [installing, setInstalling] = useState(false);

  useEffect(() => {
    return subscribe((event, data) => {
      if (event === "wallet:install:progress") {
        const progress = data as { downloaded: number; total: number };
        setInstallProgress(progress);
        return;
      }

      if (event === "wallet:install:complete") {
        setInstalling(false);
        setInstallProgress(null);
        refetchWallet();
        return;
      }

      if (event === "wallet:install:error") {
        const payload = data as { message: string };
        setInstalling(false);
        setInstallProgress(null);
        setInstallError(payload.message);
      }
    });
  }, [refetchWallet]);

  useEffect(() => {
    setSendTo("");
    setSendAmount("");
    setSendError(null);
    setFeeHint(null);
    setSendToken(getWalletChain(activeWalletTab).nativeSymbol);
  }, [activeWalletTab]);

  useEffect(() => {
    if (!activePane) return;

    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setActivePane(null);
        setSendError(null);
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [activePane]);

  const hasWallets = walletStatus?.installed && (walletStatus.wallets ?? 0) > 0;

  const { data: walletList } = useRPC(
    () => (hasWallets ? wallet.list() : Promise.resolve(null)),
    [hasWallets]
  );
  const firstWallet = walletList?.wallets?.[0]?.name;

  const { data: walletTransactions, mutate: mutateWalletTransactions } = useRPC(
    () =>
      firstWallet
        ? wallet.transactionList({
            limit: MAX_WALLET_ACTIVITY_ITEMS,
            wallet: firstWallet,
          })
        : Promise.resolve(null),
    [firstWallet],
    { refreshIntervalMs: 30_000 }
  );

  const { data: solanaWalletAddr, error: solanaWalletError } = useRPC(
    () =>
      firstWallet
        ? wallet.address({ chain: getWalletChain("solana").rpcChain, wallet: firstWallet })
        : Promise.resolve(null),
    [firstWallet]
  );
  const { data: baseWalletAddr, error: baseWalletError } = useRPC(
    () =>
      firstWallet
        ? wallet.address({ chain: getWalletChain("base").rpcChain, wallet: firstWallet })
        : Promise.resolve(null),
    [firstWallet]
  );
  const {
    data: solanaWalletBal,
    error: solanaBalanceError,
    mutate: mutateSolanaBalance,
    pause: pauseSolanaBalance,
    resume: resumeSolanaBalance,
  } = useRPC(
    () =>
      firstWallet
        ? wallet.balance({ chain: getWalletChain("solana").rpcChain, wallet: firstWallet })
        : Promise.resolve(null),
    [firstWallet],
    { refreshIntervalMs: 30_000 }
  );
  const {
    data: baseWalletBal,
    error: baseBalanceError,
    mutate: mutateBaseBalance,
    pause: pauseBaseBalance,
    resume: resumeBaseBalance,
  } = useRPC(
    () =>
      firstWallet
        ? wallet.balance({ chain: getWalletChain("base").rpcChain, wallet: firstWallet })
        : Promise.resolve(null),
    [firstWallet],
    { refreshIntervalMs: 30_000 }
  );

  const currentChain = getWalletChain(activeWalletTab);
  const walletAddresses = {
    solana: { address: solanaWalletAddr?.address, error: solanaWalletError },
    base: { address: baseWalletAddr?.address, error: baseWalletError },
  } satisfies Record<WalletChainId, { address?: string; error: string | null }>;
  const walletBalances = {
    solana: solanaWalletBal,
    base: baseWalletBal,
  } satisfies Record<WalletChainId, WalletBalance | null>;
  const walletBalanceErrors = {
    solana: solanaBalanceError,
    base: baseBalanceError,
  } satisfies Record<WalletChainId, string | null>;
  const walletBalanceControls = {
    solana: {
      mutate: mutateSolanaBalance,
      pause: pauseSolanaBalance,
      resume: resumeSolanaBalance,
    },
    base: {
      mutate: mutateBaseBalance,
      pause: pauseBaseBalance,
      resume: resumeBaseBalance,
    },
  } satisfies Record<
    WalletChainId,
    {
      mutate: typeof mutateSolanaBalance;
      pause: () => void;
      resume: () => void;
    }
  >;
  const currentAddress = walletAddresses[activeWalletTab].address;
  const currentAddressError = walletAddresses[activeWalletTab].error;
  const currentBalance = walletBalances[activeWalletTab];
  const currentBalanceError = walletBalanceErrors[activeWalletTab];
  const currentTokens =
    currentBalance?.tokens && currentBalance.tokens.length > 0
      ? currentBalance.tokens
      : fallbackTokensForChain(activeWalletTab);

  const stablecoinTotal = approximateStablecoinTotal(buildAssetCards(walletBalances));
  const activityEntries = walletTransactions?.entries ?? [];
  const query = activitySearch.trim().toLowerCase();
  const filteredActivityEntries = [...activityEntries]
    .sort((left, right) => {
      return new Date(right.created_at).getTime() - new Date(left.created_at).getTime();
    })
    .filter((entry) => {
      if (activityChainFilter !== "all" && entry.chain !== activityChainFilter) {
        return false;
      }

      if (!activityWithinRange(entry.created_at, activityTimeRange)) {
        return false;
      }

      if (!query) return true;

      const haystack = [
        entry.asset,
        entry.counterparty,
        entry.counterparty_subtitle,
        entry.memo,
        entry.status,
        entry.tx_hash,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();

      return haystack.includes(query);
    });

  const appendActivity = useCallback(
    async (entry: WalletActivityEntry) => {
      if (!firstWallet) return false;

      mutateWalletTransactions((previous) => {
        const current = previous?.entries ?? [];
        const entries = [entry, ...current.filter((item) => item.id !== entry.id)].slice(
          0,
          MAX_WALLET_ACTIVITY_ITEMS
        );

        return {
          count: entries.length,
          entries,
          wallet: previous?.wallet ?? firstWallet,
        };
      });

      try {
        await wallet.transactionAppend({ entry, wallet: firstWallet });
        return true;
      } catch {
        mutateWalletTransactions((previous) => {
          if (!previous) return previous;
          const entries = previous.entries.filter((item) => item.id !== entry.id);
          return { ...previous, count: entries.length, entries };
        });
        return false;
      }
    },
    [firstWallet, mutateWalletTransactions]
  );

  const flashActionMessage = useCallback((message: string) => {
    setActionMessage(message);
    window.setTimeout(() => {
      setActionMessage((current) => (current === message ? null : current));
    }, 4000);
  }, []);

  const handleInstall = useCallback(async () => {
    setInstalling(true);
    setInstallError(null);
    setInstallProgress(null);
    try {
      await wallet.install();
    } catch (error: unknown) {
      setInstalling(false);
      setInstallError(error instanceof Error ? error.message : "Install failed");
    }
  }, []);

  const handleCopyWalletAddress = useCallback(
    async (address: string) => {
      try {
        await navigator.clipboard.writeText(address);
        setCopiedAddress(address);
        flashActionMessage("Address copied.");
        window.setTimeout(() => {
          setCopiedAddress((current) => (current === address ? null : current));
        }, 2000);
      } catch {
        flashActionMessage("Could not copy the address.");
      }
    },
    [flashActionMessage]
  );

  const handleFillMaxAmount = useCallback(async () => {
    if (!firstWallet) return;

    const chain = getWalletChain(activeWalletTab);
    const nativeSymbol = chain.nativeSymbol;
    if (sendToken === nativeSymbol) {
      try {
        const result = await wallet.maxTransfer({
          chain: chain.rpcChain,
          wallet: firstWallet,
        });
        setSendAmount(result.max);
        setFeeHint(`Estimated fee: ${result.fee} ${nativeSymbol}`);
        window.setTimeout(() => {
          setFeeHint((current) =>
            current === `Estimated fee: ${result.fee} ${nativeSymbol}` ? null : current
          );
        }, 4000);
        return;
      } catch {
        const nativeBalance = currentTokens.find((token) => token.symbol === nativeSymbol);
        const fallbackAmount =
          chain.nativeMaxReserve !== undefined
            ? String(
                Math.max(
                  0,
                  parseAmount(nativeBalance?.balance ?? "0") - chain.nativeMaxReserve
                )
              )
            : nativeBalance?.balance ?? "0";
        setSendAmount(fallbackAmount);
        return;
      }
    }

    const token = currentTokens.find((candidate) => candidate.symbol === sendToken);
    setSendAmount(token?.balance ?? "0");
  }, [activeWalletTab, currentTokens, firstWallet, sendToken]);

  const handleSend = useCallback(async () => {
    if (!firstWallet) return;

    setSending(true);
    setSendError(null);

    const chain = getWalletChain(activeWalletTab);
    const balanceControl = walletBalanceControls[chain.id];
    const nativeSymbol = chain.nativeSymbol;
    const isNativeTransfer = sendToken === nativeSymbol;

    balanceControl.pause();

    try {
      let fee = "0";
      try {
        const estimate = await wallet.maxTransfer({
          chain: chain.rpcChain,
          wallet: firstWallet,
        });
        fee = estimate.fee;
      } catch {
        fee = "0";
      }

      const result = await wallet.transfer({
        amount: sendAmount,
        chain: chain.rpcChain,
        to: sendTo,
        token: sendToken,
        wallet: firstWallet,
      });

      const txHash = result.transaction_hash;
      const activityStored = await appendActivity({
        amount: `-${sendAmount}`,
        asset: sendToken,
        chain: chain.activityChain,
        counterparty: sendTo,
        counterparty_subtitle: `${chain.label} transfer`,
        created_at: new Date().toISOString(),
        id: createActivityID("send"),
        kind: "send",
        memo: txHash ? `Broadcast ${truncAddr(txHash)}` : "Submitted from sky10",
        status: txHash ? "Broadcast" : "Submitted",
        tx_hash: txHash,
        tx_url: txHash ? chain.explorerTxHref(txHash) : undefined,
      });

      balanceControl.mutate((previous) => {
        if (!previous?.tokens) return previous;

        const updatedTokens = previous.tokens.map((token) => {
          const decimals = chain.tokenDecimals(token.symbol);

          if (token.symbol === nativeSymbol) {
            const deduction = isNativeTransfer ? addDecimal(sendAmount, fee, decimals) : fee;
            return { ...token, balance: subDecimal(token.balance, deduction, decimals) };
          }

          if (!isNativeTransfer && token.symbol === sendToken) {
            return {
              ...token,
              balance: subDecimal(token.balance, sendAmount, decimals),
            };
          }

          return token;
        });

        return { ...previous, tokens: updatedTokens };
      });
      window.setTimeout(balanceControl.resume, 15_000);

      setActivePane(null);
      setSendTo("");
      setSendAmount("");
      setFeeHint(null);
      flashActionMessage(
        activityStored ? "Transfer submitted." : "Transfer submitted. Activity was not saved."
      );
    } catch (error: unknown) {
      setSendError(error instanceof Error ? error.message : "Transfer failed");
      balanceControl.resume();
    } finally {
      setSending(false);
    }
  }, [
    activeWalletTab,
    appendActivity,
    firstWallet,
    flashActionMessage,
    sendAmount,
    sendTo,
    sendToken,
  ]);

  const handleFund = useCallback(async () => {
    if (!firstWallet) return;

    const chain = getWalletChain(activeWalletTab);

    try {
      const result = await wallet.deposit({
        chain: chain.rpcChain,
        wallet: firstWallet,
      });

      const activityStored = await appendActivity({
        asset: chain.nativeSymbol,
        chain: chain.activityChain,
        counterparty: "Funding flow",
        counterparty_subtitle: chain.label,
        created_at: new Date().toISOString(),
        external_url: result.url,
        id: createActivityID("fund"),
        kind: "fund",
        memo: result.url ? "Opened external deposit flow" : result.status,
        status: result.url ? "Opened" : "Ready",
      });

      if (result.url) {
        window.open(result.url, "_blank", "noopener,noreferrer");
        flashActionMessage(
          activityStored
            ? "Funding flow opened in a new tab."
            : "Funding flow opened. Activity was not saved."
        );
      } else {
        flashActionMessage(
          activityStored ? "Deposit flow is ready." : "Deposit flow is ready. Activity was not saved."
        );
      }
    } catch (error: unknown) {
      setSendError(error instanceof Error ? error.message : "Funding failed");
    }
  }, [activeWalletTab, appendActivity, firstWallet, flashActionMessage]);

  if (walletStatusLoading && !walletStatus && !walletError && !installing) {
    return (
      <div className="px-6 py-8 sm:px-8">
        <div className="mx-auto w-full max-w-7xl space-y-6">
          <PageHeader
            title="Wallet"
            description="Send funds, copy receive addresses, and keep track of wallet activity started from sky10."
          />
          <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
            <div className="flex items-center gap-3 text-sm text-secondary">
              <Icon className="animate-spin text-lg text-primary" name="sync" />
              Loading wallet status...
            </div>
          </section>
        </div>
      </div>
    );
  }

  return (
    <div className="px-6 py-8 sm:px-8">
      <div className="mx-auto w-full max-w-7xl space-y-6">
        <PageHeader
          title="Wallet"
          description="Balances, receive addresses, transfers, and wallet activity."
          actions={(
            <>
              <Link
                className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                to="/settings/apps"
              >
                <Icon className="text-base" name="download" />
                Manage OWS
              </Link>
              <Link
                className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                to="/settings"
              >
                <Icon className="text-base" name="settings" />
                Settings
              </Link>
            </>
          )}
        />

        {((walletStatus && !walletStatus.installed) || walletError) && !installing && (
          <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
            <div className="grid gap-8 lg:grid-cols-[minmax(0,1.1fr)_minmax(280px,0.9fr)]">
              <div className="space-y-5">
                <StatusBadge icon="download" tone="neutral">
                  Wallet not ready
                </StatusBadge>
                <div className="space-y-3">
                  <h2 className="text-3xl font-semibold tracking-tight text-on-surface">
                    Install Open Wallet Standard
                  </h2>
                  <p className="max-w-xl text-sm text-secondary sm:text-base">
                    The dedicated wallet workspace is ready, but the OWS binary is not installed
                    yet. Install it once to enable Solana and Base addresses, balances, funding,
                    and transfers.
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                  <button
                    className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary transition-transform active:scale-95"
                    onClick={handleInstall}
                    type="button"
                  >
                    <Icon className="text-base" name="download" />
                    Install Wallet
                  </button>
                  <Link
                    className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2.5 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                    to="/settings/apps"
                  >
                    <Icon className="text-base" name="open_in_new" />
                    Open Managed Apps
                  </Link>
                </div>
                {(installError || walletError) && (
                  <p className="text-sm text-error">{installError ?? walletError}</p>
                )}
              </div>

              <div className="rounded-[1.75rem] border border-primary/10 bg-primary/5 p-6">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  What you get
                </p>
                <div className="mt-4 space-y-4 text-sm text-secondary">
                  <div className="flex items-start gap-3">
                    <div className="mt-0.5 flex h-8 w-8 items-center justify-center rounded-full bg-surface-container text-primary">
                      <Icon name="account_balance_wallet" />
                    </div>
                    <p>Dedicated receive addresses for Solana and Base inside one workspace.</p>
                  </div>
                  <div className="flex items-start gap-3">
                    <div className="mt-0.5 flex h-8 w-8 items-center justify-center rounded-full bg-surface-container text-primary">
                      <Icon name="send" />
                    </div>
                    <p>Quick send and fund flows without staying inside Settings.</p>
                  </div>
                  <div className="flex items-start gap-3">
                    <div className="mt-0.5 flex h-8 w-8 items-center justify-center rounded-full bg-surface-container text-primary">
                      <Icon name="table_rows" />
                    </div>
                    <p>A wide activity table for wallet actions initiated from sky10.</p>
                  </div>
                </div>
              </div>
            </div>
          </section>
        )}

        {installing && (
          <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
            <div className="space-y-5">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                  <Icon className="animate-pulse text-2xl" name="downloading" />
                </div>
                <div>
                  <h2 className="text-2xl font-semibold text-on-surface">Installing wallet</h2>
                  <p className="text-sm text-secondary">
                    Downloading OWS so the wallet workspace can come online.
                  </p>
                </div>
              </div>
              <div className="h-2.5 overflow-hidden rounded-full bg-surface-container">
                <div
                  className="h-full rounded-full bg-primary transition-all duration-300"
                  style={{
                    width:
                      installProgress && installProgress.total > 0
                        ? `${Math.round(
                            (installProgress.downloaded / installProgress.total) * 100
                          )}%`
                        : "0%",
                  }}
                />
              </div>
              {installProgress && installProgress.total > 0 && (
                <p className="text-sm text-secondary">
                  {Math.round(installProgress.downloaded / 1024 / 1024)}
                  {" / "}
                  {Math.round(installProgress.total / 1024 / 1024)} MB
                </p>
              )}
            </div>
          </section>
        )}

        {walletStatus?.installed && !installing && !hasWallets && (
          <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
            <div className="grid gap-8 lg:grid-cols-[minmax(0,1.1fr)_minmax(280px,0.9fr)]">
              <div className="space-y-5">
                <StatusBadge icon="check_circle" tone="success">
                  OWS ready
                </StatusBadge>
                <div className="space-y-3">
                  <h2 className="text-3xl font-semibold tracking-tight text-on-surface">
                    Create your first wallet
                  </h2>
                  <p className="max-w-xl text-sm text-secondary sm:text-base">
                    The binary is installed, but there is no wallet yet. Create the default wallet
                    to unlock receive addresses, token balances, transfers, and the new activity
                    table.
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                  <button
                    className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary transition-transform active:scale-95"
                    onClick={async () => {
                      try {
                        await wallet.create({ name: "default" });
                        refetchWallet();
                        flashActionMessage("Wallet created.");
                      } catch (error: unknown) {
                        setInstallError(
                          error instanceof Error ? error.message : "Failed to create wallet"
                        );
                      }
                    }}
                    type="button"
                  >
                    <Icon className="text-base" name="add_card" />
                    Create Wallet
                  </button>
                </div>
                {installError && <p className="text-sm text-error">{installError}</p>}
              </div>

              <div className="rounded-[1.75rem] border border-outline-variant/10 bg-surface-container p-6">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Next
                </p>
                <div className="mt-4 space-y-4 text-sm text-secondary">
                  <p>sky10 will use the first wallet it finds as the default workspace wallet.</p>
                  <p>Once created, this page shows Solana and Base receive addresses side by side.</p>
                  <p>The activity table starts filling in as soon as you send or fund from here.</p>
                </div>
              </div>
            </div>
          </section>
        )}

        {walletStatus?.installed && !installing && hasWallets && (
          <>
            <section className="rounded-xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
              <div className="space-y-6 py-1">
                <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
                  <div className="space-y-2">
                    <h2 className="text-5xl font-semibold tracking-tight text-on-surface">
                      {formatCurrency(stablecoinTotal)}
                    </h2>
                  </div>
                  <div className="hidden">
                    {/* Bring this wallet label back when the UI supports switching multiple wallets. */}
                    <StatusBadge icon="account_balance_wallet" tone="success">
                      {firstWallet || "default"}
                    </StatusBadge>
                  </div>
                </div>

                <div className="flex flex-wrap items-center gap-3">
                  <button
                    className="inline-flex items-center gap-2 rounded-lg bg-on-surface px-4 py-2.5 text-sm font-semibold text-surface transition-transform active:scale-95"
                    onClick={() => setActivePane("send")}
                    type="button"
                  >
                    <Icon className="text-base" name="north_east" />
                    Send
                  </button>
                  <button
                    className="inline-flex items-center gap-2 rounded-lg border border-outline-variant/20 px-4 py-2.5 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                    onClick={() => setActivePane("receive")}
                    type="button"
                  >
                    <Icon className="text-base" name="south_west" />
                    Receive
                  </button>
                  <button
                    className="inline-flex items-center gap-2 rounded-lg border border-outline-variant/20 px-4 py-2.5 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                    onClick={() => {
                      void handleFund();
                    }}
                    type="button"
                  >
                    <Icon className="text-base" name="add_card" />
                    Fund
                  </button>
                </div>
              </div>
            </section>

            <section className="rounded-xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
              <div className="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
                <div className="space-y-1">
                  <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                    Recent activity
                  </p>
                  <h3 className="text-xl font-semibold text-on-surface">
                    Recent transactions
                  </h3>
                </div>

                <div className="flex flex-col gap-3 md:flex-row">
                  <label className="relative min-w-[260px]">
                    <Icon className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-base text-outline" name="search" />
                    <input
                      className="w-full rounded-lg border border-outline-variant/10 bg-surface-container py-2.5 pl-11 pr-4 text-sm outline-none transition-colors focus:border-primary/30"
                      onChange={(event) => setActivitySearch(event.target.value)}
                      placeholder="Search transactions..."
                      type="search"
                      value={activitySearch}
                    />
                  </label>

                  <select
                    className="rounded-lg border border-outline-variant/10 bg-surface-container px-3 py-2.5 text-sm outline-none transition-colors focus:border-primary/30"
                    onChange={(event) =>
                      setActivityChainFilter(event.target.value as ActivityChainFilter)
                    }
                    value={activityChainFilter}
                  >
                    <option value="all">Networks</option>
                    {WALLET_CHAIN_OPTIONS.map((chain) => (
                      <option key={chain.id} value={chain.activityChain}>
                        {chain.label}
                      </option>
                    ))}
                  </select>

                  <select
                    className="rounded-lg border border-outline-variant/10 bg-surface-container px-3 py-2.5 text-sm outline-none transition-colors focus:border-primary/30"
                    onChange={(event) =>
                      setActivityTimeRange(event.target.value as ActivityTimeRange)
                    }
                    value={activityTimeRange}
                  >
                    <option value="all">All time</option>
                    <option value="24h">Last 24 hours</option>
                    <option value="7d">Last 7 days</option>
                    <option value="30d">Last 30 days</option>
                  </select>
                </div>
              </div>

              {filteredActivityEntries.length > 0 ? (
                <div className="mt-6 overflow-x-auto">
                  <table className="min-w-full border-separate border-spacing-0">
                    <thead>
                      <tr className="text-left">
                        <th className="border-b border-outline-variant/10 px-4 py-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          When
                        </th>
                        <th className="border-b border-outline-variant/10 px-4 py-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          Counterparty
                        </th>
                        <th className="border-b border-outline-variant/10 px-4 py-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          Memo
                        </th>
                        <th className="border-b border-outline-variant/10 px-4 py-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          Amount
                        </th>
                        <th className="border-b border-outline-variant/10 px-4 py-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          Asset
                        </th>
                        <th className="border-b border-outline-variant/10 px-4 py-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          Status
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {filteredActivityEntries.map((entry) => (
                        <tr className="group" key={entry.id}>
                          <td className="border-b border-outline-variant/10 px-4 py-4 align-top">
                            <div className="flex items-start gap-3">
                              <div className="mt-0.5 flex h-9 w-9 items-center justify-center rounded-full border border-outline-variant/10 bg-surface-container text-secondary">
                                <Icon className="text-base" name={activityIcon(entry.kind)} />
                              </div>
                              <div className="space-y-1">
                                <p className="text-sm font-medium text-on-surface">
                                  <RelativeTime value={entry.created_at} />
                                </p>
                                <p className="text-xs text-secondary" title={absoluteDateTime(entry.created_at)}>
                                  {absoluteDateTime(entry.created_at)}
                                </p>
                              </div>
                            </div>
                          </td>
                          <td className="border-b border-outline-variant/10 px-4 py-4 align-top">
                            <div className="space-y-1">
                              <p className="text-sm font-medium text-on-surface">
                                {truncAddr(entry.counterparty)}
                              </p>
                              <p className="text-xs text-secondary">
                                {entry.counterparty_subtitle ||
                                  getWalletChainForActivity(entry.chain).label}
                              </p>
                            </div>
                          </td>
                          <td className="border-b border-outline-variant/10 px-4 py-4 align-top">
                            <div className="space-y-1">
                              <p className="text-sm text-on-surface">{entry.memo}</p>
                              {entry.tx_hash && (
                                <p className="font-mono text-xs text-secondary">
                                  {truncAddr(entry.tx_hash)}
                                </p>
                              )}
                            </div>
                          </td>
                          <td className="border-b border-outline-variant/10 px-4 py-4 align-top">
                            {entry.amount ? (
                              <div className="space-y-1">
                                <p className="text-sm font-semibold text-error">{entry.amount}</p>
                                <p className="text-xs text-secondary">
                                  {entry.amount} {entry.asset}
                                </p>
                              </div>
                            ) : (
                              <span className="text-sm text-secondary">--</span>
                            )}
                          </td>
                          <td className="border-b border-outline-variant/10 px-4 py-4 align-top">
                            <div className="space-y-1">
                              <p className="text-sm font-medium text-on-surface">
                                {entry.asset || "--"}
                              </p>
                              <p className="text-xs text-secondary">
                                {getWalletChainForActivity(entry.chain).label}
                              </p>
                            </div>
                          </td>
                          <td className="border-b border-outline-variant/10 px-4 py-4 align-top">
                            <div className="flex items-center gap-2">
                              <StatusBadge tone={activityStatusTone(entry.status)}>
                                {entry.status}
                              </StatusBadge>
                              {(entry.tx_url || entry.external_url) && (
                                <a
                                  className="inline-flex h-9 w-9 items-center justify-center rounded-full border border-outline-variant/10 text-secondary transition-colors hover:text-primary"
                                  href={entry.tx_url || entry.external_url}
                                  rel="noopener noreferrer"
                                  target="_blank"
                                  title="Open details"
                                >
                                  <Icon className="text-base" name="open_in_new" />
                                </a>
                              )}
                            </div>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="mt-6 rounded-[1.5rem] border border-dashed border-outline-variant/20 bg-surface-container p-8 text-center">
                  <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 text-primary">
                    <Icon className="text-2xl" name="table_rows" />
                  </div>
                  <h4 className="mt-4 text-lg font-semibold text-on-surface">
                    No recent wallet activity yet
                  </h4>
                  <p className="mx-auto mt-2 max-w-xl text-sm text-secondary">
                    Send or fund from this page and sky10 will store the resulting transaction rows
                    locally.
                  </p>
                </div>
              )}

              <div className="mt-5 flex flex-col gap-2 border-t border-outline-variant/10 pt-4 text-sm text-secondary sm:flex-row sm:items-center sm:justify-between">
                <p>
                  Showing {filteredActivityEntries.length} of {activityEntries.length} stored
                  transaction row{activityEntries.length === 1 ? "" : "s"}.
                </p>
              </div>
            </section>

            {activePane && (
              <div
                className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4 backdrop-blur-sm"
                onMouseDown={() => {
                  setActivePane(null);
                  setSendError(null);
                }}
                role="presentation"
              >
                <section
                  aria-label={activePane === "send" ? "Send funds" : "Receive funds"}
                  aria-modal="true"
                  className="flex max-h-[calc(100vh-2rem)] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-outline-variant/15 bg-surface p-6 shadow-2xl"
                  onMouseDown={(event) => event.stopPropagation()}
                  role="dialog"
                >
                  <div className="flex items-start justify-between gap-4 border-b border-outline-variant/10 pb-5">
                    <div className="flex flex-wrap items-center gap-3">
                      <h3 className="text-2xl font-semibold text-on-surface">
                        {activePane === "send" ? "Send" : "Receive"}
                      </h3>
                      <label className="inline-flex items-center gap-2 rounded-lg border border-outline-variant/15 bg-transparent px-2.5 py-1.5 text-sm text-secondary">
                        <WalletChainLogo chain={activeWalletTab} />
                        <select
                          className="bg-transparent font-semibold text-on-surface outline-none"
                          onChange={(event) => setActiveWalletTab(event.target.value as WalletTab)}
                          value={activeWalletTab}
                        >
                          {WALLET_CHAIN_OPTIONS.map((chain) => (
                            <option key={chain.id} value={chain.id}>
                              {chain.label}
                            </option>
                          ))}
                        </select>
                      </label>
                    </div>
                    <button
                      className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-outline-variant/20 text-secondary transition-colors hover:text-on-surface"
                      onClick={() => {
                        setActivePane(null);
                        setSendError(null);
                      }}
                      title="Close"
                      type="button"
                    >
                      <Icon className="text-base" name="close" />
                    </button>
                  </div>

                  {activePane === "receive" ? (
                    <div className="flex-1 space-y-5 overflow-y-auto py-5">
                      {currentAddress ? (
                        <>
                          <ReceiveQRCode address={currentAddress} chain={activeWalletTab} />
                          <div className="rounded-lg border border-outline-variant/10 bg-surface-container p-4">
                            <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
                              Address
                            </p>
                            <code className="mt-3 block break-all font-mono text-sm text-primary">
                              {currentAddress}
                            </code>
                          </div>
                          <div className="grid gap-3 sm:grid-cols-2">
                            <button
                              className="inline-flex items-center justify-center gap-2 rounded-lg bg-primary px-4 py-2.5 text-sm font-semibold text-on-primary transition-transform active:scale-95"
                              onClick={() => {
                                void handleCopyWalletAddress(currentAddress);
                              }}
                              type="button"
                            >
                              <Icon className="text-base" name={copiedAddress === currentAddress ? "check" : "content_copy"} />
                              Copy
                            </button>
                            <a
                              className="inline-flex items-center justify-center gap-2 rounded-lg border border-outline-variant/20 px-4 py-2.5 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                              href={currentChain.explorerAddressHref(currentAddress)}
                              rel="noopener noreferrer"
                              target="_blank"
                            >
                              <Icon className="text-base" name="open_in_new" />
                              Explorer
                            </a>
                          </div>
                        </>
                      ) : (
                        <div className="rounded-lg border border-outline-variant/10 bg-surface-container p-4 text-sm text-secondary">
                          {currentAddressError || "No address is available for this chain yet."}
                        </div>
                      )}
                      {actionMessage && <p className="text-sm text-primary">{actionMessage}</p>}
                    </div>
                  ) : (
                    <div className="flex-1 space-y-5 overflow-y-auto py-5">
                      <div className="overflow-hidden rounded-lg border border-outline-variant/10">
                        {currentTokens.map((token) => (
                          <div
                            className="flex items-center justify-between border-b border-outline-variant/10 px-3 py-2.5 last:border-b-0"
                            key={`${activeWalletTab}:${token.symbol}`}
                          >
                            <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
                              {token.symbol}
                            </p>
                            <p className="font-mono text-sm font-semibold text-on-surface">
                              {formatTokenBalance(token.balance)}
                            </p>
                          </div>
                        ))}
                      </div>

                      <div className="space-y-2">
                        <label className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                          Recipient
                        </label>
                        <input
                          className="w-full rounded-lg border border-outline-variant/10 bg-surface-container px-3 py-2.5 text-sm outline-none transition-colors focus:border-primary/30"
                          onChange={(event) => setSendTo(event.target.value)}
                          placeholder="Recipient address"
                          type="text"
                          value={sendTo}
                        />
                      </div>

                      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_128px]">
                        <div className="space-y-2">
                          <label className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                            Amount
                          </label>
                          <div className="relative">
                            <input
                              className="w-full rounded-lg border border-outline-variant/10 bg-surface-container px-3 py-2.5 pr-16 text-sm outline-none transition-colors focus:border-primary/30"
                              onChange={(event) => setSendAmount(event.target.value)}
                              placeholder="0.00"
                              type="text"
                              value={sendAmount}
                            />
                            <button
                              className="absolute right-3 top-1/2 -translate-y-1/2 text-[10px] font-bold uppercase tracking-[0.16em] text-primary transition-colors hover:text-on-surface"
                              onClick={() => {
                                void handleFillMaxAmount();
                              }}
                              type="button"
                            >
                              All
                            </button>
                          </div>
                        </div>

                        <div className="space-y-2">
                          <label className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                            Asset
                          </label>
                          <select
                            className="w-full rounded-lg border border-outline-variant/10 bg-surface-container px-3 py-2.5 text-sm outline-none transition-colors focus:border-primary/30"
                            onChange={(event) => setSendToken(event.target.value)}
                            value={sendToken}
                          >
                            <option value={currentChain.nativeSymbol}>
                              {currentChain.nativeSymbol}
                            </option>
                            <option value="USDC">USDC</option>
                          </select>
                        </div>
                      </div>

                      {feeHint && <p className="text-sm text-secondary">{feeHint}</p>}
                      {sendError && <p className="text-sm text-error">{sendError}</p>}
                      {actionMessage && <p className="text-sm text-primary">{actionMessage}</p>}
                      {currentBalanceError && <p className="text-sm text-error">{currentBalanceError}</p>}

                      <div className="flex flex-wrap items-center justify-end gap-3 border-t border-outline-variant/10 pt-4">
                        <button
                          className="inline-flex items-center gap-2 rounded-lg border border-outline-variant/20 px-3 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
                          onClick={() => {
                            setActivePane(null);
                            setSendError(null);
                          }}
                          type="button"
                        >
                          Cancel
                        </button>
                        <button
                          className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-on-primary transition-transform disabled:opacity-50 active:scale-95"
                          disabled={sending || !sendTo || !sendAmount}
                          onClick={() => {
                            void handleSend();
                          }}
                          type="button"
                        >
                          <Icon className="text-base" name={sending ? "sync" : "north_east"} />
                          {sending ? "Sending..." : "Send"}
                        </button>
                      </div>
                    </div>
                  )}
                </section>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
