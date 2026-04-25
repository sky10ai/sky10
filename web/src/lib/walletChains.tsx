import { useId, type ReactElement } from "react";
import type { WalletActivityChain } from "./walletActivity";

export const WALLET_CHAIN_IDS = ["solana", "base"] as const;

export type WalletChainId = (typeof WALLET_CHAIN_IDS)[number];

export interface WalletTokenBalance {
  balance: string;
  symbol: string;
}

export interface WalletChainLogoProps {
  className?: string;
}

export interface WalletChainConfig {
  activityChain: WalletActivityChain;
  fallbackTokens: WalletTokenBalance[];
  explorerAddressHref: (address: string) => string;
  explorerTxHref: (txHash: string) => string;
  id: WalletChainId;
  label: string;
  Logo: (props: WalletChainLogoProps) => ReactElement;
  nativeMaxReserve?: number;
  nativeSymbol: string;
  order: number;
  receivePayload: (address: string) => string;
  rpcChain: string;
  tokenDecimals: (symbol: string) => number;
}

function SolanaLogo({ className = "h-4 w-5" }: WalletChainLogoProps) {
  const gradientID = useId().replaceAll(":", "");

  return (
    <svg aria-hidden="true" className={className} viewBox="0 0 160 120">
      <defs>
        <linearGradient id={gradientID} x1="14" x2="144" y1="30" y2="30">
          <stop offset="0%" stopColor="#9945FF" />
          <stop offset="100%" stopColor="#14F195" />
        </linearGradient>
      </defs>
      <path d="M32 21h112l-18 18H14z" fill={`url(#${gradientID})`} />
      <path d="M14 51h112l18 18H32z" fill={`url(#${gradientID})`} />
      <path d="M32 81h112l-18 18H14z" fill={`url(#${gradientID})`} />
    </svg>
  );
}

function BaseLogo({ className = "h-4 w-4" }: WalletChainLogoProps) {
  return (
    <svg aria-hidden="true" className={className} viewBox="0 0 120 120">
      <circle cx="60" cy="60" fill="#0052FF" r="50" />
      <path
        d="M60 92c-17.7 0-32-14.3-32-32s14.3-32 32-32c15.4 0 28.3 10.8 31.4 25.2H60v13.6h31.4C88.3 81.2 75.4 92 60 92z"
        fill="#ffffff"
      />
    </svg>
  );
}

function tokenDecimalsFor(nativeSymbol: string, nativeDecimals: number) {
  return (symbol: string) => (symbol.toUpperCase() === nativeSymbol ? nativeDecimals : 6);
}

export const WALLET_CHAINS: Record<WalletChainId, WalletChainConfig> = {
  solana: {
    activityChain: "solana",
    explorerAddressHref: (address) =>
      `https://explorer.solana.com/address/${encodeURIComponent(address)}`,
    explorerTxHref: (txHash) =>
      `https://explorer.solana.com/tx/${encodeURIComponent(txHash)}`,
    fallbackTokens: [
      { balance: "0", symbol: "SOL" },
      { balance: "0", symbol: "USDC" },
    ],
    id: "solana",
    label: "Solana",
    Logo: SolanaLogo,
    nativeMaxReserve: 0.00001,
    nativeSymbol: "SOL",
    order: 0,
    receivePayload: (address) => address,
    rpcChain: "solana",
    tokenDecimals: tokenDecimalsFor("SOL", 9),
  },
  base: {
    activityChain: "base",
    explorerAddressHref: (address) =>
      `https://basescan.org/address/${encodeURIComponent(address)}`,
    explorerTxHref: (txHash) => `https://basescan.org/tx/${encodeURIComponent(txHash)}`,
    fallbackTokens: [
      { balance: "0", symbol: "ETH" },
      { balance: "0", symbol: "USDC" },
    ],
    id: "base",
    label: "Base",
    Logo: BaseLogo,
    nativeSymbol: "ETH",
    order: 1,
    receivePayload: (address) => address,
    rpcChain: "eip155:8453",
    tokenDecimals: tokenDecimalsFor("ETH", 18),
  },
};

export const WALLET_CHAIN_OPTIONS = WALLET_CHAIN_IDS.map((id) => WALLET_CHAINS[id]);

export function getWalletChain(chain: WalletChainId): WalletChainConfig {
  return WALLET_CHAINS[chain];
}

export function getWalletChainForActivity(chain: WalletActivityChain): WalletChainConfig {
  return WALLET_CHAINS[chain];
}

export function WalletChainLogo({
  chain,
  className,
}: WalletChainLogoProps & { chain: WalletChainId }) {
  const Logo = getWalletChain(chain).Logo;
  return <Logo className={className} />;
}
