---
created: 2026-04-26
updated: 2026-04-27
model: gpt-5.5
---

# AI-First Agent Creation And Smoke

This entry archives the completed April 26 slice of the AI-first app work. It
does not retire `docs/work/current/ai-first-app/`; that plan is still active
for paid agents, public listings, richer jobs/artifacts, real media-file
processing, and the broader product shape. This archive records what landed:
prompt-to-spec creation, review, compile, approval, provisioning into a fresh
OpenClaw Docker sandbox, secret binding, tool/job groundwork, and a successful
fresh-VM smoke test.

The canonical prompt stayed front and center:

```text
make me an ai agent that can process media files to change the accent to british
```

The important product shift was from "chat about an agent" to "create a
reviewable contract, approve it, and provision a durable runtime." The desired
path is:

```text
prompt -> versioned AgentSpec -> review -> approval -> generated runtime files
-> sandbox/VM provisioning -> tool registration -> jobs and artifacts
                          \
                           -> optional payment/receipt later
```

## Landed Commits

Primary commits on the branch after rebasing onto `origin/main`:

- `81aca1fa` `feat(agent): add spec-driven creation flow`
- `96ecb442` `feat(agent): compile specs into runtime previews`
- `fa9f3040` `feat(agent): provision approved specs into sandboxes`
- `a629a5b7` `feat(agent): expose manifest tools from sandboxes`
- `877c95b5` `fix(agent): keep media capability broad`
- `c80a2128` `feat(agent): add durable tool call jobs`
- `070d068d` `feat(agent): report tool call job lifecycle`
- `b58b2b2a` `feat(agent): expose job artifacts`
- `45aafedb` `feat(agent): default specs to openclaw harness`
- `aa146ca9` `feat(agent): apply spec runtime compose layers`
- `23530f53` `feat(sandbox): bind anthropic secrets for agent templates`
- `a1f102f3` `feat(sandbox): use ghcr runtime base images`
- `0dda8c4d` `fix(sandbox): pin openclaw chromium deps`
- `72fe7c5c` `fix(sandbox): locate playwright chromium binary`
- `10f7cacc` `ci(runtime): publish runtime images publicly`
- `927b852f` `ci(runtime): remove package visibility step [skip ci]`
- `6ed5580a` `fix(agent): harden docker agent provisioning`

## Product Position

The AI-first direction from `docs/work/current/ai-first-app/` remains the
guiding architecture:

- The Go daemon and existing HTTP/JSON-RPC layer stay the source of truth.
- The root agent orchestrates daemon capabilities instead of replacing
  them.
- Model-facing tools are curated Vercel AI SDK `tool()` wrappers over RPCs,
  not raw backend method names exposed directly to the model.
- Read-only tools can execute directly; mutating tools are approval-gated.
- Debug, raw repair, destructive, platform-blocked, or high-risk RPCs stay out
  of the default assistant profile.
- Durable agents, jobs, artifacts, and approvals matter more than chat history.

This branch kept that split: the model can plan or draft, but provisioning
still flows through typed daemon RPCs and sandbox managers.

## Root Agent And UI

The current app work moved toward an AI-first layout while keeping the existing
infrastructure surfaces reachable. The active implementation converged on
`/agents` as the first AI workspace: the app root redirects there, and no
compatibility route was kept for `/ai` or `/home`. The broader Home workspace
idea remains in the current planning docs, but this slice put the practical
agent-creation flow on `Agents`.

The `Agents` page now carries the practical agent-creation UI:

- Header is `Agents`, with existing agents summarized underneath.
- A "Create an agent" composer accepts natural-language prompts.
- Prompt suggestion pills start with action verbs, such as "Watch my
  downloads..." and "Summarize new meeting...".
- The primary action is `Create agent`, not `Draft`.
- A secondary `Ask AI` path still routes through the root agent run card,
  so the root-agent workflow and the structured spec workflow can coexist.
- Recent specs are listed separately from registered agents.
- A selected spec can be edited, saved, approved, or discarded.

Root agent history is daemon-owned JSONL under `.sky10`, not browser local
storage. The active records are stored under:

- `.sky10/rootagent/runs.jsonl`
- `.sky10/agents/specs.jsonl`
- `.sky10/agents/jobs.jsonl`

Secret values are never stored in those records; specs and jobs only carry
secret names, bindings, digests, payload refs, and metadata.

## AgentSpec 0.1.0

`AgentSpec` became the reviewable object produced from a prompt. Every fixture
and generated spec carries:

```yaml
spec: 0.1.0
```

The schema covers:

- identity and lifecycle: `id`, `status`, `prompt`, `name`, timestamps
- runtime: target, provider, template, harness, packages, and containers
- fulfillment mode
- exported `tools[]`
- inputs and outputs
- secret requirements and environment bindings
- permissions/effects
- optional commerce and pricing
- job policy
- publish policy
- metadata

The branch intentionally uses `tools[]`, not `skills[]`, for the new contract.
The exported tool surface is the callable service surface; internal runtime
abilities remain hidden unless the owner explicitly exports them.

Capabilities use broad `domain.action` labels by convention, not as a rigid
validation rule. For the media example, the exported capability is
`media.convert`, not a hyper-specific nested label such as
`media.accent.convert`.

## Fixtures And Prompt Inference

The branch added a large set of YAML fixtures under
`pkg/agent/testdata/specs/` so the spec contract is concrete instead of only
described in prose. Examples include:

- `media-accent-private.yaml`
- `media-accent-paid.yaml`
- `coding-codex-private.yaml`
- `coding-codex-public-paid.yaml`
- `financial-dexter-private.yaml`
- `financial-dexter-paid.yaml`
- `podcast-transcribe.yaml`
- `video-subtitles.yaml`
- `meeting-summarizer.yaml`
- `downloads-organizer.yaml`
- `invoice-extractor.yaml`
- `data-extract-ipfs.yaml`
- `browser-research-manual.yaml`
- `calendar-scheduler.yaml`
- `customer-support-agent.yaml`
- `image-generation-public.yaml`

The inference rules that landed are intentionally simple but prove the shape:

- media/accent/British prompts create a media agent with `ffmpeg`, a
  `media.convert` tool, audio/video payload input, artifact output, and
  `ELEVENLABS_API_KEY` as the canonical optional provider secret
- explicit Codex/coding prompts select a Codex harness/template shape
- explicit Dexter/financial prompts select a Dexter harness/template shape and
  preserve the Dexter source reference
- otherwise, generic agent prompts default to OpenClaw
- OpenClaw is the default harness unless the prompt clearly asks for something
  else
- paid wording such as "charge $2 per minute" turns on optional commerce fields
  in the spec, but does not make paid agents a launch requirement

## Compile And Provision

`agent.spec.compile` turns an approved or draft spec into a dry-run runtime
preview. It validates the spec and produces:

- compiled runtime metadata
- secret bindings
- generated files
- provisioning actions
- warnings

For the media accent spec, compile output includes:

- runtime target `sandbox`
- provider `lima`
- template `openclaw-docker`
- harness `openclaw`
- runtime package `ffmpeg`
- worker container `ubuntu:24.04`
- exported tool `media.convert`
- secret binding `ELEVENLABS_API_KEY -> ELEVENLABS_API_KEY`
- generated `agent-manifest.json`
- generated `.env.example`
- generated `README.md`
- generated `compose.yaml`
- generated worker `Dockerfile` when needed

`agent.spec.provision` requires an approved spec. It compiles the spec, then
hands the generated files and secret bindings to the sandbox manager so the
runtime can be created from the reviewed contract. For the first slice, only
sandbox provisioning is supported.

The key safety behavior is that secrets are attached before VM boot. That lets
the generated OpenClaw Docker runtime start with the provider environment it
needs, without storing raw secret values in the spec or generated manifest.

## RPC And AI Tool Surface

The root agent tool registry in `web/src/lib/rootAgentTools.ts` uses
Vercel AI SDK `tool()` definitions with schemas, execution functions, and
approval policy in one place. The branch added spec/provision wrappers to that
registry, including:

- `agents_createSpec` -> `agent.spec.create`
- `agents_listSpecs` -> `agent.spec.list`
- `agents_getSpec` -> `agent.spec.get`
- `agents_compileSpec` -> `agent.spec.compile`
- `agents_updateSpec` -> `agent.spec.update`
- `agents_approveSpec` -> `agent.spec.approve`
- `agents_discardSpec` -> `agent.spec.discard`
- `agents_provisionSpec` -> `agent.spec.provision` and sandbox creation

The broader approved-RPC posture from the current AI-first docs remains:

- ordinary user-configurable RPC workflows should become assistant-addressable
- read-only inspection is safe by default
- writes need exact parameters, visible effects, and approval
- raw debug, repair, KV mutation, low-level S3, raw mailbox mutation, broad
  host control, and destructive platform verbs stay disabled by default

The planner uses `gpt-5.5` with low reasoning effort for intent
classification. That is only the planner choice for this web-side root
assistant path; the durable sandbox templates use their own model settings.

## Secrets And Models

Secret binding work landed for both spec-created runtimes and normal sandbox
creation:

- sandbox records carry explicit `secret_bindings`
- bindings are normalized and validated as environment variable names
- bindings resolve through the sky10 secret store
- values materialize into the sandbox state `.env` file under a managed block
- attach, detach, and sync paths rematerialize the sandbox env
- create-time bindings are supported, so the user does not have to create a VM
  first and attach secrets later

The canonical media spec now uses `ELEVENLABS_API_KEY` for both the sky10
secret name and the sandbox environment variable. A smoke check confirmed that
the key was present in the OpenClaw container, but the key value is intentionally
not recorded here.

OpenClaw and Hermes templates now default to:

```text
anthropic/claude-opus-4-6
```

For Anthropic-backed OpenClaw/Hermes templates, sandbox creation adds a default
`ANTHROPIC_API_KEY` binding when a matching sky10 secret exists and the selected
model uses Anthropic. Non-Anthropic model choices do not get an Anthropic
binding injected.

## Jobs And Artifacts

The branch added the first durable job layer for callable agent tools:

- `agent.call`
- `agent.cancel`
- `agent.job.get`
- `agent.job.list`
- `agent.job.updateStatus`
- `agent.job.complete`
- `agent.job.fail`

Jobs are persisted under `.sky10/agents/jobs.jsonl`. They track buyer, seller,
agent, tool, capability, work state, payment state, payload refs, output dir,
input/result digests, idempotency keys, progress, delivery metadata, and error
state.

Work state and payment state are separate. The data model already has states
such as `payment_required`, `authorized`, `settled`, and `refunded`, but the
paid-agent flow is not finished in this archive.

Artifacts can be exposed through result refs and downloaded through the
daemon-owned artifact HTTP handler when the file URI is inside the job output
directory. The handler rejects missing files, directories, non-file URI schemes,
and paths outside the job output directory.

Hermes and OpenClaw bridge code also learned to preserve media/artifact parts
in chat responses, so a future media job can return inspectable files instead
of burying outputs in prose.

## Docker And Runtime Provisioning

The fresh-VM smoke forced several Docker-backed runtime fixes:

- Docker install in guest templates now has stronger apt retry behavior.
- Docker Compose pull/build/start has retry wrappers instead of failing on a
  single transient registry timeout.
- Docker-backed templates emit an explicit `guest.docker.pull` progress step
  before Compose build.
- OpenClaw and Hermes Docker templates explicitly pull their runtime image
  before build/start.
- Generated worker containers use `ubuntu:24.04` instead of a custom
  `ghcr.io/sky10ai/sky10-agent-ubuntu:24.04` image.
- The removed custom worker image avoided an unnecessary GHCR dependency for
  trivial generated worker containers.
- OpenClaw Docker entrypoint clears stale Xvfb display locks before launch and
  logs Xvfb startup failures clearly.
- The host-connect ready flow checks whether the generated agent is already
  visible before blocking on `skylink.connect`.
- Runtime images were made publicly pullable in GHCR so anonymous fresh-VM
  provisioning can work.

This made the current path retryable and smoke-testable, but it is not the
right final product shape. Fresh VMs still pull the large OpenClaw runtime image
inside their own Docker daemon. The next design pass should make GHCR a cache
or optimization path, not a hard dependency during agent creation.

Candidate follow-ups:

- preload runtime images into reusable VM/template cache
- import a host-side image tar into the VM before bootstrap
- build from staged local runtime assets in the VM without requiring GHCR
  during agent creation

## Fresh VM Smoke

The end-to-end smoke passed with a disposable sandbox named
`media-accent-smoke-0426233424`.

The smoke proved:

1. The canonical prompt created an `AgentSpec`.
2. The spec compiled to OpenClaw Docker, `ffmpeg`, `media.convert`, and the
   `ELEVENLABS_API_KEY` binding.
3. Approval succeeded.
4. Provisioning created a fresh Lima VM.
5. The VM installed Docker and started OpenClaw Docker containers.
6. The generated OpenClaw agent registered with the host.
7. The real host websocket smoke path completed successfully.

The smoke command was:

```sh
sky10 sandbox smoke media-accent-smoke-0426233424 \
  --message 'sky10 fresh agent smoke: reply with ok' \
  --timeout 180s \
  --ready-timeout 60s
```

Result:

- `sky10` forwarded health endpoint: OK
- `openclaw_gateway` forwarded health endpoint: OK
- websocket result: `ok`
- websocket ready: 2ms
- ack: 2ms
- first delta: about 4.8s
- final message: about 5s
- redacted in-container secret check: `ELEVENLABS_API_KEY` present with a
  51-byte value
- disposable VM deleted after the smoke

The smoke did not prove actual media-file conversion. It proved the system can
go from prompt to spec to approved fresh VM to registered generated agent to
websocket chat over the real path.

## What Is Still Current Work

Keep `docs/work/current/ai-first-app/` active for the remaining plan. The major
open pieces are:

- real media file input and output UX, including a consumer-friendly way to
  attach a media file and retrieve generated artifacts
- the actual ElevenLabs plus `ffmpeg` worker implementation for changing an
  audio/video voice to a British accent
- durable job UI that makes artifacts easier to inspect than chat history
- approval cards for executing mutating root-agent tools from the AI run
  surface
- a better warm runtime/image strategy so fresh VM creation does not depend on
  pulling large images from GHCR during user-visible provisioning
- paid-agent owner flow: pricing editor, payout wallet, payment policy,
  payment proof, and receipts
- buyer flow: price display, wallet approval, proof submission, and receipt
  retrieval
- public listing and discovery
- bidding/request flows
- final payload ref policy for local private files, SkyFS refs, IPFS refs, and
  remote URLs
- Windows-ready agent runtime/hypervisor story

The first paid-agent target remains optional: a private/free agent should work
first, and commerce should turn on only when the owner explicitly asks to
charge or later enables pricing for an exported tool.

## Validation

Validation recorded during this work included focused Go tests around agent
specs, sandbox templates, Lima provisioning, Docker scripts, OpenClaw/Hermes
runtime bundles, secret bindings, and lifecycle handling. The final smoke log
also recorded:

```sh
git diff --check
make dev-install
sky10 sandbox smoke media-accent-smoke-0426233424 \
  --message 'sky10 fresh agent smoke: reply with ok' \
  --timeout 180s \
  --ready-timeout 60s
```

After the final rebase onto `origin/main`, `make build-web` passed.
