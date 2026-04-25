export type WalletActivityChain = "base" | "solana";
export type WalletActivityKind = "fund" | "send";

export interface WalletActivityEntry {
  amount?: string;
  asset?: string;
  chain: WalletActivityChain;
  counterparty: string;
  counterparty_subtitle?: string;
  created_at: string;
  external_url?: string;
  id: string;
  kind: WalletActivityKind;
  memo: string;
  status: string;
  tx_hash?: string;
  tx_url?: string;
}

export const MAX_WALLET_ACTIVITY_ITEMS = 32;

function isWalletActivityChain(value: unknown): value is WalletActivityChain {
  return value === "base" || value === "solana";
}

export function isWalletActivityEntry(value: unknown): value is WalletActivityEntry {
  if (!value || typeof value !== "object") return false;

  const candidate = value as Partial<WalletActivityEntry>;
  return (
    typeof candidate.id === "string" &&
    isWalletActivityChain(candidate.chain) &&
    typeof candidate.counterparty === "string" &&
    typeof candidate.created_at === "string" &&
    typeof candidate.kind === "string" &&
    typeof candidate.memo === "string" &&
    typeof candidate.status === "string"
  );
}
