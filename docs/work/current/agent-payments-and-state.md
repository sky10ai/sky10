---
created: 2026-04-03
model: claude-opus-4-6
---

# Agent Payments, Receipts, and Network State

## Overview

How agents pay each other, how the network records what happened, and where
all the data lives. Payments settle on-chain. Everything else propagates
through gossip and lives on every node.

## Wallet: OWS (Open Wallet Standard)

Each agent has a local wallet managed by OWS (`github.com/open-wallet-standard/core`).
OWS is a local-first, multi-chain wallet with policy-gated signing. Private
keys stay encrypted at rest and are decrypted only inside the signing path
after policy checks pass.

### Key properties

- **Local custody**: keys in `~/.ows/wallets/`, never leave the machine
- **Multi-chain**: EVM, Solana, Bitcoin, Cosmos, Tron, TON, Sui, Spark,
  Filecoin, XRPL — all first-class
- **Policy engine**: pre-signing rules gate every agent operation
- **Two modes**: `sign()` returns raw signed bytes; `signAndSend()` signs
  and broadcasts to the chain

### Agent wallet setup

```bash
# Create a wallet
ows wallet create --name "agent-treasury"

# Fund it (USDC on Solana, or any supported chain)
ows fund deposit --wallet agent-treasury

# Create a spending policy
cat > agent-policy.json << 'EOF'
{
  "id": "daily-limit",
  "name": "Agent Daily Spending Limit",
  "version": 1,
  "rules": [
    {"type": "allowed_chains", "chain_ids": ["solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"]},
    {"type": "expires_at", "timestamp": "2026-05-01T00:00:00Z"}
  ],
  "executable": "/path/to/daily-cap.py",
  "config": {"max_daily_usd": "5.00"}
}
EOF
ows policy create --file agent-policy.json

# Create a scoped API key for the agent
ows key create --wallet agent-treasury --policy daily-limit
# → ows_key_a1b2c3d4...
```

Different agents get different policies:

| Agent          | Daily limit | Chains        | Expiry   |
|----------------|-------------|---------------|----------|
| Personal AI    | $50         | Solana + EVM  | None     |
| Research bot   | $5          | Solana        | 30 days  |
| Untrusted tool | $0.50       | Solana        | 24 hours |

The policy engine enforces this before the key is even decrypted. Token stolen
without disk access = useless. Disk accessed without token = unreadable.

## Payment Protocol

Payments are peer-to-peer. No HTTP, no payment server, no intermediary. The
caller signs a transaction locally and hands the raw bytes to the provider
over skylink. The provider submits it to the chain on their own terms.

### Chain agnosticism

The protocol does not specify a settlement chain. Providers advertise what
they accept. Callers pick one they both support. OWS signs for any chain.

```json
{
  "payment": {
    "accept": [
      {"chain": "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp", "asset": "USDC"},
      {"chain": "eip155:8453", "asset": "USDC"},
      {"chain": "eip155:42161", "asset": "USDC"}
    ]
  }
}
```

Current fee comparison:

| Chain      | USDC transfer cost |
|------------|--------------------|
| Solana     | $0.0003–0.003      |
| Base       | $0.002–0.02        |
| Arbitrum   | $0.001–0.01        |
| Ethereum   | $0.50–5.00+        |

For Solana, agents only need to hold USDC. Fee payer infrastructure (Kora)
can pay SOL gas fees on the agent's behalf, deducting the cost in USDC from
the same transaction. Or the wallet holds a trivial amount of SOL ($0.50
covers thousands of transactions).

### Message flow

Six message types, all carried over skylink P2P:

```
1. call              caller → provider    "do this work"
2. payment_required  provider → caller    "it costs this much"
3. payment_proof     caller → provider    "here's a signed tx"
4. result            provider → caller    "here's the work + receipt"
5. receipt           caller → provider    "counter-signed receipt"
6. error             either → either      "something went wrong"
```

### Detailed flow

```
Caller                                   Provider
  │                                          │
  │─── call ────────────────────────────────►│
  │    {method: "research",                  │
  │     params: {query: "...", depth: "deep"}} │
  │                                          │
  │◄── payment_required ───────────────────│
  │    {price: "2000000",                    │  ← 2 USDC
  │     asset: "USDC",                       │
  │     chain: "solana:5eykt...",            │
  │     address: "7xKXtg...",               │
  │     nonce: "d4e5f6"}                     │
  │                                          │
  │  ┌──────────────────┐                    │
  │  │ OWS (local)      │                    │
  │  │                   │                    │
  │  │ 1. policy check   │                    │
  │  │    chain allowed?  │                    │
  │  │    daily cap ok?   │                    │
  │  │    key not expired?│                    │
  │  │                   │                    │
  │  │ 2. decrypt key    │                    │
  │  │ 3. sign tx        │                    │
  │  │ 4. wipe key       │                    │
  │  └────────┬─────────┘                    │
  │           │                              │
  │─── payment_proof ──────────────────────►│
  │    {signed_tx: "<raw bytes>",            │
  │     chain: "solana:5eykt...",            │
  │     amount: "2000000",                   │
  │     nonce: "d4e5f6"}                     │
  │                                          │
  │                   ┌──────────────────┐   │
  │                   │ verify (off-chain)│   │
  │                   │                   │   │
  │                   │ 1. valid sig?     │   │
  │                   │ 2. correct amount?│   │
  │                   │ 3. correct dest?  │   │
  │                   │ 4. nonce matches? │   │
  │                   │ 5. sender balance?│   │  ← one RPC call
  │                   └────────┬─────────┘   │
  │                            │             │
  │                       does the work      │
  │                            │             │
  │◄── result ─────────────────────────────│
  │    {data: {findings: [...], sources: [...]}, │
  │     receipt: {                           │
  │       tx_hash: "<deterministic>",        │
  │       caller: "sky10://abc...",           │
  │       provider: "sky10://q7m...",         │
  │       method: "research",                │
  │       amount: "2000000",                 │
  │       chain: "solana:5eykt...",          │
  │       timestamp: 1712160000,             │
  │       provider_rating: 5,                │
  │       provider_sig: "..."                │
  │     }}                                   │
  │                                          │
  │─── receipt (counter-signed) ───────────►│
  │    {receipt: {                           │
  │       ...same fields...,                 │
  │       caller_rating: 5,                  │
  │       caller_sig: "...",                 │
  │       provider_sig: "..."                │
  │     }}                                   │
  │                                          │
  │  both publish receipt to gossip          │
  │                                          │
  │                   ┌──────────────────┐   │
  │                   │ OWS (local)      │   │
  │                   │ signAndSend()    │   │
  │                   │ submit signed tx │   │
  │                   └────────┬─────────┘   │
  │                            │             │
  │                       on-chain           │
  │                    (USDC settles)         │
```

### The signed check model

The caller never broadcasts anything. They sign a transaction (a USDC
transfer to the provider's address) and hand over the raw bytes. This is
a signed check — the provider can:

- **Verify it instantly** (off-chain, free): check the signature, amount,
  destination, and sender balance
- **Cash it whenever they want**: submit it to the chain hours or days later
- **Batch-settle**: collect 100 signed payments, submit them all at once
- **Prioritize by trust**: cash immediately for unknown callers, accumulate
  a tab with repeat callers

The provider holds the leverage — they have the signed check and can submit
it at any time. The caller can't revoke it (the nonce is committed). This
incentivizes providers to do the work, since they already have guaranteed
payment.

### Settlement timing strategies

| Trust level         | Strategy                                      |
|---------------------|-----------------------------------------------|
| Unknown caller      | Verify balance, consider demanding pre-settlement |
| First-time caller   | Cash immediately after work completes          |
| Repeat caller       | Batch-settle daily                             |
| Trusted partner     | Accumulate tab, settle weekly                  |

The protocol doesn't enforce settlement timing. The six message types stay
the same regardless. A provider that wants pre-settlement simply doesn't
send `result` until they see the tx confirmed on-chain.

## Receipts

Receipts are the atomic unit of reputation. Every completed transaction
produces one.

### Structure

```json
{
  "version": 1,
  "tx_hash": "3Fxk7...",
  "caller": "sky10://abc123...",
  "provider": "sky10://q7m2k9...",
  "method": "research",
  "amount": "2000000",
  "asset": "USDC",
  "chain": "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
  "timestamp": 1712160000,
  "success": true,
  "caller_rating": 5,
  "provider_rating": 5,
  "work_summary": "researched quantum computing advances, 12 sources",
  "caller_sig": "ed25519:<sig over all fields except caller_sig>",
  "provider_sig": "ed25519:<sig over all fields except provider_sig>"
}
```

### Properties

- **Co-signed**: both parties must sign. Can't forge a review — you need
  the other party's key.
- **Anchored to on-chain payment**: `tx_hash` references a real blockchain
  transaction. Can't fabricate receipts without actually moving money.
- **Immutable once published**: gossipped to every node. Neither party can
  edit or delete it after publication.

### Disputes

If work wasn't delivered, the caller can attach a dispute to the receipt
before counter-signing:

```json
{
  "...receipt fields...",
  "success": false,
  "caller_rating": 1,
  "dispute": "no response after payment proof sent",
  "caller_sig": "..."
}
```

The provider can counter with their own signed statement. Both propagate
through gossip. The network has both sides of the story. This lays the
groundwork for on-chain slashing later — the evidence is already distributed.

## Network State: The Gossip Layer

### The problem with self-served state

If agents store their own receipts, they'll hide the bad ones. Reputation
must not be controlled by the entity it describes.

### The solution: community-held state

Receipts and listings propagate via gossip to every node in the network.
No one controls them. No one can delete them.

### Three tiers of state

```
~/.sky10/
│
├── kv/                     PRIVATE
│   │                       Encrypted, synced to your devices via S3.
│   │                       Only your agents can read or write.
│   │
│   ├── agent-config        agent settings
│   ├── agent-memory/       persistent memory
│   ├── wallet              OWS wallet reference
│   └── history/            full call logs
│
├── public/                 SELF-SOVEREIGN
│   │                       You write, anyone reads via skylink.
│   │                       Signed entries — verifiable by anyone.
│   │
│   ├── profile             name, description, version
│   ├── capabilities        ["web-research", "summarization"]
│   ├── methods/            method specs with pricing
│   ├── payment             accepted chains, assets, addresses
│   └── peer                skylink peer ID, multiaddrs
│
└── network/                COMMUNITY-HELD
    │                       Gossipped to every node. Nobody deletes.
    │                       The protocol writes, everyone stores.
    │
    ├── receipts/           every receipt in the network
    │   ├── index.db        queryable by agent, time, method
    │   └── data/           raw signed receipts
    ├── listings/           every agent's published listing
    │   └── index.db        queryable by capability
    └── disputes/           dispute claims and counter-statements
        └── index.db
```

**Private** (`kv/`): only you can read or write. Encrypted at rest. Synced
to your own devices via S3. Existing skykv infrastructure.

**Self-sovereign** (`public/`): you control the content. Anyone can read it
via skylink. Every entry is signed — readers verify the signature regardless
of where they got the data.

**Community-held** (`network/`): you don't control this. The gossip protocol
writes it. Every node stores it. An agent can't discard a bad rating because
they never held it — it's already on every other machine.

### Signed entries

Every entry in public/ and network/ carries its own proof:

```go
type SignedEntry struct {
    Key       string
    Value     []byte
    Author    ed25519.PublicKey
    Seq       uint64             // monotonic per-key, prevents replay
    Timestamp int64
    Signature []byte             // ed25519 over (key + value + seq + timestamp)
}
```

Anyone holding this entry can verify it came from the claimed author. Doesn't
matter if they got it directly, from a cache, or from a peer — the signature
is self-proving. The sequence number prevents replaying old versions.

### Gossip protocol

When a new receipt or listing is created:

1. The authoring node signs it and stores it locally
2. Sends it to all connected skylink peers
3. Each peer verifies signatures, stores locally, forwards to their peers
4. Flood continues until every node has it

Nodes maintain a seen-set (bloom filter) to avoid re-processing or
re-forwarding entries they already have.

```
A ── receipt ──► A's peers ──► their peers ──► ...
                                    │
                               every node
                               stores it
```

### Discovery via gossip

Because every node has the full listing set, discovery is a local query:

```
Daemon starts
  → connects to peers via skylink
  → peers send their gossip state (listings, receipts)
  → within seconds, you have the network
  → discovery = local database query, zero network latency

agent.discover({capability: "web-research"})
  → scans local listings index
  → cross-references local receipts for reputation
  → returns ranked results instantly
```

No search API. No directory service. No bootstrap registry (beyond a few
seed peers for initial connection). Join the network, sync the gossip,
query your own database.

### Reputation is a local computation

No reputation oracle. No trust in anyone's aggregation. Every node computes
reputation independently from the same set of receipts:

```go
func ComputeReputation(agentAddr string) Reputation {
    receipts := localStore.Query(ReceiptFilter{
        Provider: agentAddr,
    })

    var rep Reputation
    for _, r := range receipts {
        // verify both signatures
        if !verify(r.CallerSig) || !verify(r.ProviderSig) {
            continue
        }
        // verify on-chain anchor exists
        if !verifyOnChain(r.TxHash, r.Amount, r.Chain) {
            continue
        }
        rep.Completed++
        rep.TotalVolume += r.Amount
        rep.RatingSum += r.CallerRating
        if rep.FirstSeen == 0 || r.Timestamp < rep.FirstSeen {
            rep.FirstSeen = r.Timestamp
        }
    }
    rep.AvgRating = rep.RatingSum / rep.Completed
    return rep
}
```

Every node running this over the same receipts gets the same answer.

### Storage projections

Each receipt is roughly 300–500 bytes.

| Network size   | Txns/day | Daily storage | Yearly  |
|----------------|----------|---------------|---------|
| 1,000 agents   | 500      | 250 KB        | 90 MB   |
| 10,000 agents  | 10,000   | 5 MB          | 1.8 GB  |
| 100,000 agents | 500,000  | 250 MB        | 90 GB   |

At 100K agents this is manageable on any modern machine. At larger scale,
introduce tiering:

- **Full nodes**: store everything (power users, index agents)
- **Light nodes**: store receipt headers (~100 bytes: tx_hash + addresses +
  rating), fetch full bodies on demand from peers

This is a future scaling concern. For the first years, every node stores
everything.

## Future: On-Chain Enforcement

The gossip state lays the groundwork. Every node has the full receipt and
dispute history. When the protocol is ready for enforcement:

### Staking

Agents stake collateral in an on-chain contract to register. The stake
signals commitment. Listings from staked agents rank higher in discovery.

### Slashing

When a provider accepts payment but doesn't deliver:

1. The on-chain payment is verifiable (tx_hash)
2. The dispute is signed by the caller and gossipped to every node
3. The provider's lack of response is observable (no counter-statement
   within timeout)
4. A slashing contract verifies the evidence and slashes the stake

The evidence is already distributed across the network. The chain just
arbitrates and enforces consequences.

### Receipt anchoring

For higher-value transactions, receipt hashes (or merkle roots of batched
receipts) can be submitted on-chain for permanent, tamper-proof anchoring.
This supplements the gossip layer with blockchain-grade immutability.

## Summary

```
Concern              System          Where it lives
───────────────────────────────────────────────────
Wallet custody       OWS             ~/.ows/wallets/ (local)
Transaction signing  OWS             local, policy-gated
Payment settlement   Any chain       on-chain (Solana, EVM, etc.)
Agent private state  skykv           ~/.sky10/kv/ (encrypted, S3-synced)
Agent public profile public KV       ~/.sky10/public/ (signed, skylink-served)
Receipts/ratings     gossip          ~/.sky10/network/ (every node)
Listings/discovery   gossip          ~/.sky10/network/ (every node)
Disputes             gossip          ~/.sky10/network/ (every node)
Reputation           computed        local query over receipts
```

Four systems, clean separation:

- **skylink**: communication + gossip state propagation
- **skykv**: private encrypted state (your devices only)
- **OWS**: wallet custody + signing + policy enforcement
- **Chain**: payment settlement (agent-negotiated, protocol-agnostic)
