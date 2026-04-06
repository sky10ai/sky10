---
created: 2026-04-05
model: claude-opus-4-6
---

# Agent Wallet & Payments

sky10 agents can now hold funds and pay each other on Solana. Wallets
are managed by [OWS](https://openwallet.sh) (Open Wallet Standard), a
local-first key vault by MoonPay. sky10 wraps OWS for key management
and signing, but builds and broadcasts Solana transactions directly
rather than depending on OWS's limited transfer commands.

## Why This Matters

Agents need money. An AI agent that can browse the web, call APIs, and
coordinate with other agents is fundamentally limited if it can't pay
for services. The x402 payment protocol lets APIs charge per-request
via Solana micropayments, and agent-to-agent commerce requires wallets
on both sides.

Without native wallet support, every agent deployment requires manual
wallet setup, key management, and custom payment code. With this
feature, a sky10 agent gets a funded wallet out of the box and can
send SOL or USDC to any Solana address.

## Architecture

```
Settings UI (React)         sky10 daemon (Go)           Solana
+------------------+       +-------------------+       +--------+
| Wallet card      |       | wallet.* RPC      |       |        |
|  balance display |<----->| Balance: RPC query |------>| mainnet|
|  send form       |       | Transfer: build tx |       |  RPC   |
|  fund button     |       |   + ows sign send  |       |        |
+------------------+       +------|------------+       +--------+
                                  |
                           +------v------+
                           | OWS binary  |
                           | (~/.sky10/) |
                           | sign & send |
                           +-------------+
```

OWS handles key storage and signing. sky10 handles everything else:
balance queries, transaction construction, fee estimation, and the
UI. This split exists because OWS (as of 1.2.4) has no high-level
transfer command or native SOL balance query.

## What Was Built

### Backend (`pkg/wallet/`)

**OWS CLI wrapper** (`ows.go`) — shell-out to the `ows` binary for
wallet operations. All output is plain text (OWS has no JSON mode for
most commands), so we parse structured data from text:

- `CreateWallet` parses "Wallet created: <id>"
- `ListWallets` parses `ID:` / `Name:` line pairs
- `Address` finds `solana: ... -> <address>` in wallet list output
- `Transfer` builds a raw Solana transaction, hex-encodes it, and
  passes it to `ows sign send-tx`

**Solana RPC client** (`solana.go`) — direct JSON-RPC calls to
`api.mainnet-beta.solana.com` for balance and transaction building:

- `solanaBalances` fetches native SOL (`getBalance`) and SPL tokens
  (`getTokenAccountsByOwner`) in parallel
- `buildSOLTransferTx` constructs a SystemProgram.Transfer transaction
- `buildSPLTransferTx` constructs an SPL Token.Transfer transaction,
  auto-creating the recipient's Associated Token Account if needed
- `maxSOLTransfer` uses `getFeeForMessage` for exact fee calculation
- PDA derivation (`findProgramAddress`) with ed25519 curve check for
  computing ATA addresses
- Base58 encode/decode, compact-u16, amount parsing (SOL 9 decimals,
  USDC 6 decimals)

**Install flow** (`install.go`) — downloads the OWS binary from GitHub
releases with progress callbacks, atomic rename, platform-specific
asset naming (`arm64` -> `aarch64`).

**RPC handler** (`rpc.go`) — dispatches `wallet.*` JSON-RPC methods:
status, install, checkUpdate, create, list, address, balance, deposit,
transfer, maxTransfer, pay.

### Frontend (`web/src/pages/Settings.tsx`)

Wallet card in the settings bento grid with four states:

1. **Not installed** — install button with progress bar
2. **Installing** — progress bar with SSE events
3. **No wallets** — create wallet button
4. **Active** — address (with copy), SOL + USDC balances, Send/Fund

**Send form** — inline form with recipient address, amount input with
ALL button (queries exact fee via `maxTransfer` RPC), SOL/USDC
selector. Optimistic balance update uses BigInt arithmetic to avoid
float precision artifacts. Balance polling pauses during send to
prevent the pre-confirmation balance from reverting the optimistic
update.

**Fund button** — triggers `ows fund deposit` which opens a MoonPay
on-ramp URL.

### Transaction Building

We build raw Solana transactions from scratch in Go (no SDK
dependency). The wire format:

```
Transaction = compact(num_sigs) + [sig_placeholder; 64] + Message

Message = Header + compact(num_keys) + [Pubkey; 32] + Blockhash
        + compact(num_instructions) + [Instruction]

Instruction = program_id_index + compact(num_accounts) + [account_idx]
            + compact(data_len) + data
```

**SOL transfer**: 3 accounts (sender, recipient, system program),
1 instruction (SystemProgram.Transfer: `[u32=2, u64=lamports]`).

**USDC transfer**: If recipient ATA exists: 4 accounts, 1 instruction
(Token.Transfer: `[u8=3, u64=amount]`). If recipient ATA doesn't
exist: 8 accounts, 2 instructions (CreateAssociatedTokenAccount +
Token.Transfer).

### Tests (`solana_test.go`)

- Amount parsing (SOL 9 decimals, USDC 6 decimals)
- Base58 encode/decode round-trip
- Compact-u16 encoding
- PDA derivation (ATA address computation, ed25519 curve check)
- SOL transaction structure (header, accounts, instruction data)
- SPL transaction structure — transfer-only path (4 accounts)
- SPL transaction structure — CreateATA path (8 accounts, 2 instructions)
- OWS signing integration (sign-only, skipped when OWS not installed)
  for both SOL and USDC transactions

## Key Decisions

**Direct Solana RPC over OWS balance**: `ows fund balance` only reports
SPL tokens, not native SOL. We query the Solana RPC directly for both.

**Raw transaction building over OWS send**: OWS has no `fund send`
command. `ows sign send-tx` accepts hex-encoded unsigned transactions,
so we build the raw bytes and let OWS sign + broadcast.

**BigInt for optimistic updates**: JavaScript `parseFloat` produces
artifacts like `0.12000000000000001`. The `subDecimal` helper converts
balance strings to BigInt smallest-units, does integer subtraction,
and formats back.

**Pause polling during send**: The 30-second balance poll can fire
between transaction broadcast and chain confirmation, reverting the
optimistic update. Polling pauses before send and resumes 15 seconds
after (enough for Solana finality).

## Files Changed

```
pkg/wallet/ows.go          — OWS CLI wrapper + Transfer via sign send-tx
pkg/wallet/solana.go        — Solana RPC, tx building, PDA derivation
pkg/wallet/rpc.go           — wallet.* RPC dispatch
pkg/wallet/install.go       — OWS binary download + install
pkg/wallet/solana_test.go   — Solana tx + signing tests
pkg/wallet/ows_test.go      — OWS CLI wrapper tests
commands/serve.go           — Wallet handler wiring
web/src/lib/rpc.ts          — wallet RPC client + types
web/src/lib/events.ts       — wallet install SSE events
web/src/lib/useRPC.ts       — pause/resume for polling control
web/src/pages/Settings.tsx  — Wallet card UI
```
