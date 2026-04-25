---
created: 2026-04-25
model: gpt-5.5
---

# P2P CPU And Public Network Load

This entry records the April 25 investigation into high `sky10 serve` CPU in
network mode. It is intentionally written as an operational breadcrumb for
future P2P performance concerns.

The important caveat: there are still P2P performance concerns here, but they
have not been validated yet. In this context, "performance" means P2P
performance: peer discovery latency, GossipSub delivery, direct-vs-relay
success rates, relay usefulness, reconnect behavior, and behavior on larger
real networks. The CPU fix reduced local daemon load in short samples, but it
did not prove that all intended P2P workflows remain optimal under real
multi-peer workloads.

## What Triggered This

The daemon was reported pegging at 100% CPU, then later continuing to sit
around 40-70% CPU after an initial mitigation.

Live inspection showed:

- `sky10 serve` CPU around 40%+ after restart settled
- RSS around 3 GB before the public-network mitigation
- `sky10 link status` reporting hundreds of public peers, including one sample
  with 377 connected peers and no private peers
- repeated public-network coordination failures in `daemon.stderr.log`
- FS peer-sync state had already been compacted, so the remaining load was no
  longer the earlier FS retry/state-size issue

The working conclusion was that remaining load came from public libp2p
participation: DHT traffic, relay service behavior, public peer fanout, and
retry loops around public coordination surfaces.

## Commits

The branch landed three commits after rebasing onto `origin/main`:

- `c754d2a7` `fix(fs): skip snapshot poller watchdog without backend`
- `075b2a03` `fix(sync): back off noisy p2p retries`
- `f15bed26` `fix(link): bound public network load`

The first two commits addressed concrete local load sources:

- snapshot watchdog false positives when no S3 backend exists
- noisy FS P2P retry loops
- oversized FS peer-sync state
- unbounded FS peer-sync tracking

The third commit changed public network participation.

## Public Network Changes

Network mode still enables public libp2p relay behavior:

- relay client remains enabled
- circuit relay service remains enabled
- static relay fallback remains enabled when configured
- GossipSub remains initialized normally

The change was to bound public infrastructure behavior rather than disabling it.

[`pkg/link/node.go`](../../../../../pkg/link/node.go) now uses lower connection
manager watermarks:

- low water: 24
- high water: 48
- trim interval: 15 seconds

It also enables the public circuit relay service with explicit resource caps:

- reservation TTL: 15 minutes
- max reservations: 16
- max active circuits: 4
- max reservations per IP: 2
- max reservations per ASN: 8

This keeps a normal daemon from acting like an unbounded public relay while
still allowing it to serve as a bounded public circuit relay.

[`pkg/link/record.go`](../../../../../pkg/link/record.go) changed the public
DHT from `ModeAutoServer` to `ModeClient`, lowered query concurrency, and moved
bootstrap into a bounded asynchronous path.

That means a normal `sky10` daemon can still publish and query discovery data,
but it should not absorb public DHT server traffic from the wider network.

## GossipSub Implications

GossipSub was not disabled by this work.

[`pkg/link/pubsub.go`](../../../../../pkg/link/pubsub.go) still initializes
`pubsub.NewGossipSub(ctx, n.host)`, and
[`pkg/link/channel.go`](../../../../../pkg/link/channel.go) still uses
GossipSub topics for encrypted channels.

The architectural assumption remains:

- discovery finds peers
- libp2p connects peers directly or through relay
- GossipSub propagates messages over the connected mesh

The public network caps should be compatible with that model because GossipSub
does not need hundreds of random public DHT peers. It needs enough connected,
relevant peers for the topics the node joins.

The unvalidated performance concern is that future heavy use of GossipSub may
change the right connection and relay limits. The current caps fixed local CPU
load in a short observation window, but they have not been validated against
large topic meshes, many simultaneous channels, high message rates, or ugly NAT
conditions where relay paths are essential.

## Test Changes

Because normal network-mode nodes now run the public DHT as clients, tests that
previously assumed every `sky10` node could act as a DHT server had to be
updated.

The link and KV DHT integration tests now start explicit local DHT bootstrap
servers:

- [`pkg/link/discovery_test.go`](../../../../../pkg/link/discovery_test.go)
- [`pkg/link/node_test.go`](../../../../../pkg/link/node_test.go)
- [`pkg/kv/p2p_dht_integration_test.go`](../../../../../pkg/kv/p2p_dht_integration_test.go)

This better matches the new production model: ordinary `sky10` nodes are DHT
clients, while bootstrap/infrastructure nodes should be deliberate.

## Validation Run

Validation for the code changes included:

- `go test ./pkg/link -count=1`
- `go test ./pkg/kv -run 'TestP2PSync.*DHT' -count=1 -v`
- `go test ./... -count=1`
- `make check`
- `git diff --check`

The updated binary was installed locally and the daemon was restarted. Short
post-restart samples showed CPU settling from startup into low single digits,
with connected public peers staying around the high 20s rather than hundreds.
`daemon.stderr.log` was truncated after restart and remained empty during the
short observation window.

Those samples are useful evidence that the CPU issue was mitigated locally.
They are not sufficient evidence that P2P performance is fully validated.

## Follow-Up If P2P Performance Is Questioned Again

Do not treat this entry as proof that the current limits are final.

If P2P performance becomes a concern again, validate these specifically:

- GossipSub delivery latency and loss across realistic topic sizes
- mesh health with many simultaneous encrypted channels
- direct connection success rate after the DHT client-mode change
- relay fallback success under NAT conditions where direct paths fail
- whether 16 relay reservations and 4 circuits are enough for ordinary
  product traffic
- whether dedicated infrastructure nodes need a separate profile with higher
  relay limits and DHT server mode
- whether connection trimming closes useful GossipSub or relay peers under
  sustained topic load

The likely long-term shape is two roles:

- ordinary `network` daemons: bounded public participation, low background CPU,
  client DHT, bounded relay service
- explicit relay/bootstrap infrastructure: higher relay limits and possibly DHT
  server mode, with operator intent and separate performance monitoring
