---
created: 2026-04-07
model: gpt-5.4
---

# Invite & Join Bootstrap Hardening

Reworked the private-network invite and join path so fresh pairing no
longer depends entirely on slow global discovery before the two devices
can even begin talking. This upgraded the invite payload with direct
dial hints, made invite creation publish fresher discovery state, and
changed join to try direct dial and Nostr-backed bootstrap before
falling all the way back to the full global resolver.

This work shipped primarily in `v0.41.2`.

## Why

The earlier private-network refactor made join daemon-owned and made the
private network more reliable overall, but fresh bootstrap still had the
wrong dependency:

- invites already carried Nostr relays
- but `identity.join` still waited on global resolution of the inviter's
  live presence
- if DHT/provider propagation lagged, join could take tens of seconds or
  time out even though the inviter was alive and had just issued the
  invite

That was the wrong model. The first join should use the information
already in the invite as directly as possible. Global discovery is for
reconnects, not for the first handshake.

## What Changed

### 1. Invite payloads now carry direct bootstrap hints

P2P invites now include:

- the inviter peer ID
- current inviter multiaddrs
- an issued-at timestamp
- a short relay list

That matters because a joiner now has concrete dial hints immediately
instead of only a private-network identity and some relays.

### 2. Invite creation publishes fresher state up front

When an invite is created, the daemon now does a best-effort immediate
publish of private-network membership and presence before returning the
code.

That matters because the joiner is less likely to race stale discovery
state when the invite is used right away.

### 3. Join is now direct-dial-first

The join path now prefers:

1. direct dial from invite hints
2. Nostr-only bootstrap resolution using the invite relay set
3. full resolver fallback

That matters because the joiner no longer jumps straight to the slowest
and most eventually-consistent path.

### 4. Nostr is used more narrowly and more intentionally during bootstrap

The invite relays are now part of a bootstrap-specific path instead of
only being one more global discovery input.

That matters because the join flow can use the relays it already knows
about without treating them as an all-purpose replacement for the
steady-state libp2p data plane.

## User-Facing Outcomes

- fresh invites contain enough information for immediate direct dial
  attempts
- `identity.join` no longer depends only on slow global propagation
- clean two-device pairing succeeds more reliably from a cold start
- bootstrap uses the invite's relay information more effectively

## What This Improved

This work materially improved bootstrap correctness:

- invite generation returns a more useful code
- join is less likely to fail before it ever reaches the inviter
- clean pairing is more consistent than before

That is real progress. The system no longer wastes the information
already present in the invite.

## What This Did Not Solve

This work did **not** materially solve post-join time-to-usable
connection.

Observed on clean `v0.41.3` testing:

- join over RPC succeeds
- both devices quickly converge on the same private-network identity
- both devices get matching KV namespace IDs
- but there can still be a noticeable convergence window after the
  joiner restart before:
  - `peer_count: 1`
  - `skykv.status.sync_state: ok`
  - immediate set/delete round-trips

So the improvement is mostly:

- better first-contact correctness

not:

- instant post-join usability

That remaining lag now looks much more like a restart-boundary
reconnect problem than an invite-code problem.

## Important Releases In This Series

- `v0.41.2`: invite payload upgrade, direct-dial-first join, Nostr-only
  bootstrap fallback

## Remaining Work

The next follow-on is the part this did not finish:

- make the joiner come back from restart and reach a usable
  private-network peer quickly and predictably
- reduce or eliminate the post-join convergence window before KV is
  actually usable
- surface clearer bootstrap phases if the system is still reconnecting

That is a separate problem from "does the invite contain enough
bootstrap information," and it should be treated as its own follow-on
effort.
