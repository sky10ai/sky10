---
created: 2026-04-26
updated: 2026-04-26
model: gpt-5.5
---

# Prompt To Agent And Optional Commerce Plan

## Goal

Let a user describe an agent in plain language, review the inferred contract,
and provision it into a real runtime. The first useful path is a private or
free agent. Commerce is optional infrastructure that lets the same agent later
charge for callable services when the owner chooses to expose that surface.

The desired product path is:

```text
prompt -> draft AgentSpec -> approval -> provisioned runtime -> exported tools
-> durable jobs -> result and artifacts
                          \
                           -> optional payment -> receipt
```

This plan combines the AI-first agent creation flow with the Agent Tools,
Commerce, and Jobs V1 model without making paid agents a launch requirement.

## Product Shape

The default flow should create a usable agent with no monetization at all:

```text
Create an agent that redubs videos with a British accent.
```

Only when the owner asks to charge, or later toggles commerce on for an exported
tool, should sky10 introduce pricing, wallet policy, payment proofs, and
receipts.

The user should be able to say:

```text
Create an agent that redubs videos with a British accent and charges $2 per
minute of processed audio.
```

sky10 should infer:

- the user wants a durable agent, not a chat answer
- the agent needs media input and media output
- the runtime needs ffmpeg and a speech or voice provider
- the runtime needs secrets such as provider API keys
- the agent should expose one or more curated public or private tools
- tool calls should become jobs
- paid tools require pricing, wallet policy, payment proof, and receipts

The user reviews and approves the inferred contract before anything is
provisioned or published.

## Non-Goals For This Branch

- no public marketplace
- no competitive bidding in the first slice
- no requirement that initial agents charge for work
- no automatic spending by agents without explicit policy
- no broad export of every internal runtime, MCP, shell, or model tool
- no `skills[]` compatibility path in the new agent contract

## Core Objects

### AgentSpec

`AgentSpec` is the reviewable object produced from a prompt.

It should include:

- `name`
- `description`
- `runtime`: VM template, container set, or local target
- `fulfillment`: autonomous, assisted, manual, or unspecified
- `tools`: curated `ToolSpec[]`
- `inputs`: supported file types, payload refs, or trigger sources
- `outputs`: artifacts, destination folders, and result schemas
- `secrets`: required secret names and environment bindings
- `permissions`: effects the agent may perform
- `commerce`: seller policy, payout wallet, and default pricing
- `job_policy`: cancellation, streaming, retention, max duration
- `publish_policy`: private first, public later

The spec is persisted separately from the live agent registration so it can be
reviewed, edited, reprovisioned, and audited.

### ToolSpec

Agents export `tools[]`, not `skills[]`.

Each tool should describe:

- `name`
- `capability`
- `description`
- `audience`: private or public
- `scope`: current, trusted, or explicit
- `input_schema`
- `output_schema`
- `stream_schema`
- `effects`
- `availability`
- `fulfillment`
- `pricing`
- `supports_cancel`
- `supports_streaming`
- `meta`

Tools are the service surface. Internal runtime abilities stay hidden unless the
owner intentionally exports them.

### Job

Every accepted tool call should become a durable job, even if it finishes
quickly.

Jobs carry:

- buyer and seller
- tool and capability
- input digest and result digest
- work state
- payment state
- payload refs
- status and stream events
- cancellation state
- receipts

Work state and payment state must stay separate. A job can be complete while
payment is still authorized, and a job can be waiting on payment before work
starts.

### Commerce

Commerce is optional per tool.

If a tool is not paid, its pricing model is `free` and the job payment state is
`none`. That should be the default for the first prompt-to-agent flow.

Supported first pricing modes:

- `free`
- `fixed`
- `variable`
- `per_interval`

Paid jobs use:

- tool-level pricing for expectation setting
- `payment_required` for the binding job-specific request
- local wallet policy for buyer approval
- `payment_proof` to authorize payment
- receipts after completion

Agents must not spend money or accept paid public work without explicit owner
policy.

## Prompt-To-Agent Flow

1. User enters a prompt on `/agents`.
2. Root assistant classifies it as `agent_create`.
3. The model drafts an `AgentSpec`.
4. The system enriches the spec with deterministic checks:
   - available templates
   - required tools and Docker packages
   - known secret names
   - input and output locations
   - wallet and payout readiness
   - unsupported platform gaps
5. UI shows a review screen with sections for:
   - purpose
   - runtime
   - exported tools
   - secrets
   - files and payloads
   - permissions and effects
   - pricing and payout
   - job behavior
6. User edits and approves the spec.
7. Provisioner creates or selects the runtime.
8. Secrets are attached before runtime boot when needed.
9. Runtime files are generated, including Docker Compose files where the
   template supports containers.
10. The adapter registers the live agent with `agent.register` and `tools[]`.
11. The new agent appears under `My Agents`.
12. A test job can be run immediately.

## Paid Agent Flow

1. Owner chooses "Charge for this agent" in the review screen.
2. UI requires:
   - pricing model
   - amount or rate
   - payment asset
   - payout address or wallet
   - refund or cancellation terms
   - audience and scope
3. sky10 validates that wallet and policy are configured.
4. The exported `ToolSpec.pricing` is stored with the agent.
5. A buyer calls the tool through `agent.call`.
6. Seller returns one of:
   - `accepted`
   - `payment_required`
   - `input_required`
   - `result`
   - `error`
7. If payment is required, buyer wallet policy approves or rejects it.
8. Buyer submits `payment_proof`.
9. Seller verifies proof and starts or resumes work.
10. Job emits status, stream, and result events.
11. Completion creates a receipt binding buyer, seller, job, amount, result
    digest, and signatures.

## Runtime Adapter Contract

Every provisioned runtime needs one thin sky10-facing adapter.

Minimum adapter operations:

- `ListExportedTools()`
- `CallTool(name, input, job_context)`
- `CancelJob(job_id)`
- `SubscribeJobEvents(job_id)`

The runtime can be Hermes, OpenClaw, Dexter, Codex, Claude Code, local shell,
human-assisted UI, or another system. sky10 only standardizes the proxy
boundary.

## Initial RPC Surface

First slice:

- `agent.spec.draft`
- `agent.spec.get`
- `agent.spec.update`
- `agent.spec.approve`
- `agent.provision`
- `agent.register` with `tools[]`
- `agent.list`
- `agent.call`
- `agent.cancel`
- `agent.job.get`
- `agent.job.list`

Commerce slice:

- `agent.payment.proof`
- `agent.receipt.get`
- `agent.receipt.list`

Later:

- `agent.publish`
- `agent.discover`
- `agent.request`
- `agent.bid`

## Storage

Use daemon-owned durable records under `.sky10`, not browser local storage.

Initial storage can be JSONL while the model settles:

- `.sky10/agents/specs.jsonl`
- `.sky10/agents/jobs.jsonl`
- `.sky10/agents/receipts.jsonl`

Records should be append-only snapshots keyed by stable IDs, matching the Home
history pattern. If the shape stabilizes, move to a richer store later.

Do not persist secret values in specs, job records, or receipts. Persist only
secret names, binding metadata, digests, and payload refs.

## Implementation Milestones

### Milestone 1: Draft And Review

- define `AgentSpec`, `ToolSpec`, pricing, fulfillment, availability, effects
- add `agent.spec.draft` backed by the root assistant
- add review UI on `/agents`
- support free/private tools only
- make "charge for this tool" visibly optional and off by default
- persist drafts in daemon JSONL

Acceptance:

- prompt produces a structured draft
- user can edit, approve, or discard it
- no runtime is created before approval

### Milestone 2: Provision A Private Free Agent

- create runtime from approved spec
- generate Docker Compose files when the template uses containers
- attach required secrets before VM boot
- register the agent with `tools[]`
- show exported tools on the agent page

Acceptance:

- user can create a media or coding agent from a prompt
- agent appears in `My Agents`
- exported tools are visible and callable locally

### Milestone 3: Jobs And Artifacts

- add durable job records
- add `agent.call`, `agent.cancel`, `agent.job.get`, `agent.job.list`
- stream status events keyed by `job_id`
- support URI-backed payload refs including `skyfs://`, `ipfs://`, and `https://`
- show jobs and artifacts on the agent page

Acceptance:

- tool calls create durable jobs
- files can go in and artifacts can come out
- job history survives UI refresh and daemon restart

### Milestone 4: Direct Paid Tools

- add pricing editor to spec review
- require payout wallet configuration for paid tools
- return `payment_required` from paid calls
- add buyer payment proof flow
- add payment state to jobs
- emit receipts after completion

Acceptance:

- owner can create a paid private tool
- buyer sees price and approves payment
- job records separate work state from payment state
- receipt is produced without leaking payload contents

### Milestone 5: Public Listings

- sign public tool listings
- publish listings for public tools
- discover by capability, price, availability, effects, and payment asset
- keep execution scope explicit even for public tools

Acceptance:

- public discovery can find a seller by capability
- a public listing does not expose secrets or private payloads
- calls still go through normal `agent.call` and job records

### Milestone 6: Bidding

- add `agent.request`
- add signed `agent.bid`
- allow buyer to accept a bid and call the selected seller
- keep bidding optional

Acceptance:

- direct calls still work without bids
- bids select the seller but do not create work by themselves
- accepted work still becomes a normal job

## First Vertical Slice

Use the media dubbing agent because it forces the right product boundaries.

Prompt:

```text
Create an agent that takes an audio or video file, redubs the voice with a
British accent, writes the output file back, and charges $2 per minute.
```

Expected draft:

- runtime: sandbox with container support
- tools:
  - `media.redub`
- inputs:
  - audio/video payload ref
- outputs:
  - transcript
  - dubbed audio
  - dubbed video when input is video
- packages:
  - ffmpeg
- secrets:
  - ElevenLabs or chosen voice provider API key
- effects:
  - `file.read`
  - `file.write`
  - `payment.charge`
- pricing:
  - variable, unit `audio_minutes`, rate `2.00`
- job behavior:
  - supports streaming
  - supports cancel before final render

The user should not need to know to ask for ffmpeg, Docker Compose, payload
refs, payment proofs, or receipt records. The system should infer those from the
outcome.

## Open Questions

- Which wallet rail and asset should be the first paid-tool default?
- Should the first paid flow require settled payment before work starts, or
  allow authorized payment with settlement after completion?
- Which payload ref scheme should be used for local private files before IPFS is
  configured?
- Should `agent.spec.*` be a separate namespace long term, or collapse into
  `agent.create` after the draft/review flow stabilizes?
- How should public agents prove availability without leaking machine state?
