---
created: 2026-04-15
model: gpt-5.4
---

# Base Wallet Support

This entry covers the Base wallet work that landed in `26c96ac`
(`feat(wallet): show base balances in settings`), `c696ecc`
(`feat(wallet): support base send and explorer links`), and `c19ebae`
(`test(wallet): add signed base tx fixtures`).

sky10's wallet surface can now operate on Base as well as Solana. The
Settings wallet card can show a Base address, fetch ETH and USDC balances,
open Base explorer links, start a Base funding flow, and send ETH or USDC
from the same OWS-backed wallet already used for Solana.

## Why

The first wallet integration shipped real Solana support, but the broader
agent-payments direction already assumed low-fee multi-chain settlement.
That left a practical gap:

- the wallet UI still behaved as if it was Solana-only
- OWS output needed chain-aware EVM address parsing
- Base transfers needed local transaction construction, fee handling, and
  regression coverage comparable to the Solana path

Base was the next useful chain because it is already part of the payment
direction, it exercises a different transaction model than Solana, and it
keeps fees low enough for everyday agent flows.

## What Shipped

### 1. Settings gained a real Base wallet surface

The wallet card now has Solana and Base tabs. The Base tab can:

- show the wallet's Base address
- open `basescan.org` for that address
- show ETH and USDC balances
- send ETH or Base USDC
- launch the Base deposit/on-ramp flow
- compute "ALL" for ETH as balance minus estimated gas

The optimistic-update and paused-polling behavior from the Solana send flow
was extended to Base so background balance refreshes do not immediately
overwrite a just-submitted send.

### 2. Base balances use direct chain RPC

`pkg/wallet/base.go` adds Base JSON-RPC reads against
`https://mainnet.base.org`.

It fetches:

- native ETH via `eth_getBalance`
- Base USDC via `eth_call` against
  `0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913`

Those queries run in parallel and are returned through the existing
`BalanceResult` / `TokenBalance` wallet RPC shape.

### 3. Base sends use locally built EIP-1559 transactions

`pkg/wallet/evm.go` builds unsigned Base transactions in Go and passes them
to OWS for signing and broadcast.

Supported sends:

- native ETH transfer
- Base USDC ERC-20 transfer

The builder handles:

- nonce lookup with `eth_getTransactionCount`
- fee selection from `eth_gasPrice`,
  `eth_maxPriorityFeePerGas`, and the latest block `baseFeePerGas`
- gas estimation for USDC sends via `eth_estimateGas`, with a small safety
  bump
- ERC-20 calldata encoding for `transfer(address,uint256)`
- broadcast through `ows sign send-tx --chain base --rpc-url ... --json`

That keeps sky10 in charge of transaction construction and fee math while
continuing to use OWS as the custody and signing boundary.

### 4. The wallet RPC boundary now meaningfully supports Base

The wallet RPC methods already accepted a `chain` field. These commits made
that real for Base across:

- `wallet.address`
- `wallet.balance`
- `wallet.deposit`
- `wallet.transfer`
- `wallet.maxTransfer`

One important compatibility detail is address parsing. OWS may expose only a
generic EVM address line, so Base address resolution falls back from
`eip155:8453` to any `eip155:` address for the selected wallet.

### 5. Regression coverage landed with deterministic fixtures

The Base work shipped with byte-level tests, not just surface assertions.

Coverage includes:

- EVM amount parsing and unit formatting
- ERC-20 `balanceOf` and `transfer` calldata encoding
- unsigned EIP-1559 transaction encoding
- expected Base RPC method sequences for ETH and USDC sends
- broadcast-result parsing across multiple JSON shapes
- deterministic signed fixture checks for both ETH and USDC Base transactions
- Base-address derivation from the signing-key fixture
- OWS chain-argument handling and Base-address fallback parsing

The signed fixture tests matter because they lock down the exact output of
the new EVM transaction builder instead of only checking a few decoded fields.

## User-Facing Outcome

A user with OWS installed can now use the existing Settings wallet card for
both Solana and Base.

That means:

- one wallet install flow
- one RPC surface
- one Settings card
- two chains with chain-specific balance, explorer, funding, fee, and send
  behavior

This is the first concrete EVM-chain support in the wallet layer. It moves
the wallet product closer to the multi-chain payment direction described in
`docs/work/current/agent-payments-and-state.md`, while still keeping the
current implementation intentionally Base-specific rather than pretending to
be a generic EVM framework.
