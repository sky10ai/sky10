# Sky10 Google Auth Broker — Backend Implementation Prompt

You are working in a **new, separate backend repository**. The goal of this
repository is to build a small "auth broker" service that lets the Sky10 OSS
client do one-click "Connect Google" for end users without those users (or
Sky10 itself, at MVP) having to set up Google Cloud Console, OAuth clients,
`client_secret.json`, or third-party provider accounts.

This document is a complete standalone spec. Read it end to end before you
write code. The Sky10 client repo has been inspected separately and the
constraints below reflect its actual architecture, not assumptions.

---

## 1. Product goal

Sky10 is an OSS, local-first consumer app that lets users create AI agents.
Agents must be able to act on the user's Google data (Gmail, Calendar, Drive,
Docs, Sheets) on the user's behalf after a single "Connect Google" click in
the Sky10 UI.

Build the smallest backend that:

1. Holds the chosen provider's API key.
2. Creates a hosted Google connection session for a Sky10 user and returns a
   click-through URL.
3. Tracks `Sky10 user id ↔ provider connection id` mapping.
4. Reports connection status back to the client.
5. Proxies a small, allowlisted set of Google tool calls so the client and the
   LLM never see a raw Google OAuth token.
6. Supports revoke / disconnect.

Do **not** build a full agent runtime, prompt orchestration, message store, or
billing system here. The Sky10 client owns chat, tool dispatch, approval UX,
and identity. This service is a thin auth + tool-execution broker.

Avoid scope creep. Prefer one provider at MVP. Keep the surface small enough
that a human can audit it in an afternoon.

---

## 2. Provider decision

Three providers were evaluated against the actual Sky10 client repo.

### Sky10 client signals that drove the decision

- The Sky10 web UI is React 19 + Vite + TypeScript and already uses the
  **Vercel AI SDK v6** (`ai` ^6.0.168) with a typed tool runtime
  (`web/src/lib/rootAssistantTools.ts`). Tools are defined with `tool({...})`,
  zod input schemas, and a `needsApproval` flag.
- The client has a per-tool **policy model**: `read_only`, `approval_required`,
  `disabled`, plus a `risk: low | medium | high` field. Any new Google tools
  must slot into this model cleanly.
- The client's "user" is a libp2p ed25519 identity (`identity.show()` →
  `address`, `device_id`, `device_pubkey`). There is no centralized account
  system, no email/password, and no existing OAuth on the client.
- There is already a hosted-OAuth pattern for ChatGPT/Codex (`codex.loginStart`
  → `verification_url` → popup → callback). The Google flow should feel the
  same to users.
- The client runs against a local Go daemon on `http://localhost:9101` over
  JSON-RPC. There is currently no remote backend dependency.

### Evaluation

| Criterion | Composio | Pipedream Connect | Arcade |
|---|---|---|---|
| One-click Google connect UX | Hosted "managed auth" link, no GCP setup needed | Hosted Connect Link, no GCP setup needed | Hosted, but model is per-tool-call auth (request → auth_url returned mid-execution) |
| Consumer needs an account on the provider | No | No | No |
| Sky10 can avoid owning Google OAuth credentials at MVP | Yes (Composio-managed OAuth) | Yes (Pipedream-hosted OAuth) | Yes (Arcade-managed OAuth) |
| Gmail / Calendar / Drive / Docs / Sheets coverage | Full | Full (and 2,500+ other apps) | Full |
| Agent / tool-calling ergonomics | First-class: `composio` SDK has a Vercel AI SDK adapter that returns a `ToolSet` directly | Action runner + MCP server, looser AI SDK fit | Per-tool-call authorization model is powerful but adds a second auth step inside agent loops |
| MCP compatibility | MCP servers available | MCP server is excellent, broad surface | MCP server first-class |
| Proxy tool execution without exposing tokens | Yes — tool calls go through Composio backend | Yes — `runAction` / MCP proxy | Yes — Arcade Engine proxies |
| Security model | API key (server-side), per-entity isolation | API key + project, per-`external_user_id` isolation | API key, per-user authorization records |
| Backend implementation effort | Low — entity-based SDK is small | Low — Connect SDK is small | Medium — auth-on-tool-call requires extra orchestration |
| Lock-in risk | Medium (tool definitions are theirs) | Lower (Connect is loosely coupled, broker pattern stays portable) | Medium (Engine is the proxy) |
| Cost / quota concerns at MVP | Free tier, usage-based after | Free tier ~250 connected accounts, then per-account | Free tier, usage-based |
| Migration to Sky10-owned Google OAuth app | Supported (BYO OAuth per auth config) | Supported (BYO OAuth per project) | Supported |

### Recommendation

- **Primary provider for MVP: Composio.**
  Composio's first-party Vercel AI SDK adapter aligns directly with Sky10's
  existing tool runtime; the `entity` model maps cleanly to Sky10's libp2p
  identity address; "Composio-managed OAuth" means consumers connect Google
  with zero developer setup; the migration path to BYO Google OAuth is
  documented and supported. The client repo is *already* AI-SDK-shaped, so
  Composio gives the smallest impedance mismatch.

- **Fallback provider: Pipedream Connect.**
  If Composio's quotas, pricing, or Google action coverage become a problem,
  Pipedream Connect is the cleanest swap. Its Connect Link UX is equivalent
  for end users, its app coverage is broader (relevant if Sky10 expands
  beyond Google), and its MCP server is mature.

- **Why not Arcade first.**
  Arcade's strongest feature is per-tool-call authorization — the agent
  requests a tool, gets an `auth_url` back, the user authorizes, the tool
  retries. That model is great for stricter, security-conscious agent
  platforms but it conflicts with Sky10's product UX, which is "connect
  Google once in Settings, then just use tools." Arcade is a strong second
  choice if Sky10 later wants per-action consent prompts.

- **Assumptions and uncertainties.**
  - Assumes Composio's "managed OAuth" tier permits production use without
    a per-Sky10-user GCP project. Verify this against Composio's current
    Terms of Service before public launch — providers occasionally retire
    shared OAuth apps for high-volume tenants.
  - Assumes Composio's Google action set covers create-draft + send-with-
    confirmation, calendar create/update, Drive search + read, Docs/Sheets
    read + write. If a specific action is missing, fall back to Pipedream
    for that surface only via the provider abstraction.
  - Assumes Sky10 will eventually want BYO Google OAuth for higher quotas
    and a Sky10-branded consent screen. Build the broker so this is a
    config switch, not a rewrite.

### Implementation order

Implement the **Composio** provider first behind the abstraction in §12. The
Pipedream and Arcade implementations are not required for MVP but the
interface must be designed so they can be added later without touching API
routes or the data model.

---

## 3. Preferred backend stack

Recommend: **Hono on Node or Bun.**

- The Sky10 web client uses **Bun 1.3.x** (`web/package.json` declares
  `packageManager: "bun@1.3.11"`). Using Bun on the broker keeps the
  toolchain consistent and lets the team share lint / format rules.
- Hono is small, portable, and runs unchanged on Node, Bun, or Cloudflare
  Workers. If Sky10 later wants to deploy the broker to Workers (low-cost,
  edge-distributed, good for connect-link redirects), no rewrite is needed.
- TypeScript everywhere.

Alternatives if Hono/Bun is not viable:
- **Hono on Node** — same code, more boring runtime.
- **Fastify on Node** — fine if the team already runs Fastify in
  production. Slightly heavier but very battle-tested.

Do not pick Cloudflare Workers as the *first* deploy target unless the
Composio SDK is verified to run cleanly on Workers. Some provider SDKs ship
Node-only crypto; Workers / Bun compatibility is worth checking before
committing.

Style:
- 2-space indentation.
- No semicolons (matches the Sky10 web client).
- Strict TypeScript (`"strict": true`, `"noUncheckedIndexedAccess": true`).

---

## 4. Required environment variables

Provide a `.env.example` checked into the repo. Never commit real secrets.

```
# Provider — Composio (primary)
COMPOSIO_API_KEY=
COMPOSIO_BASE_URL=               # optional, default Composio cloud
COMPOSIO_GOOGLE_AUTH_CONFIG_ID=  # the "auth config" id for the Google integration in your Composio project

# General
SKY10_ALLOWED_ORIGINS=http://localhost:5173,http://localhost:9101
SKY10_SESSION_SECRET=            # 32+ bytes, used for signed callback state and any short-lived broker JWTs
DATABASE_URL=file:./broker.db    # SQLite for MVP; swap for Postgres later
PORT=8787
NODE_ENV=development

# Optional: BYO Google OAuth (later, when Sky10 has its own GCP project)
GOOGLE_OAUTH_CLIENT_ID=
GOOGLE_OAUTH_CLIENT_SECRET=

# Optional fallback providers (do not enable both)
PIPEDREAM_CLIENT_ID=
PIPEDREAM_CLIENT_SECRET=
PIPEDREAM_PROJECT_ID=
ARCADE_API_KEY=
```

Validate env at boot using `zod`. The process should refuse to start if a
required variable is missing or malformed (e.g., `SKY10_ALLOWED_ORIGINS` not
a comma-separated list of valid origins).

---

## 5. Core data model

SQLite + Drizzle ORM for MVP. Schema is intentionally tiny.

```ts
// users
{
  id: string            // = Sky10 identity address (libp2p ed25519). Stable across devices.
  created_at: number    // unix ms
}

// google_connections
{
  id: string
  user_id: string                          // FK -> users.id
  provider: "composio" | "pipedream" | "arcade"
  provider_connected_account_id: string    // Composio: connectedAccountId; Pipedream: account_id; Arcade: user/auth id
  status: "pending" | "active" | "revoked" | "error"
  scopes: string                           // JSON array of scope/tool category names actually requested
  last_error: string | null
  created_at: number
  updated_at: number
  last_used_at: number | null
}

// tool_audit_log (optional but strongly recommended)
{
  id: string
  user_id: string
  tool_name: string
  provider: string
  status: "ok" | "error" | "denied" | "needs_confirmation"
  redacted_input_summary: string           // e.g., "send_email to=<redacted>, subject=Re: ..., body_chars=412"
  redacted_output_summary: string          // e.g., "messageId=<redacted>, threadId=<redacted>"
  error: string | null
  created_at: number
}
```

Notes:
- `users.id` is the Sky10 libp2p identity address. Treat it as opaque. Do
  not assume it is an email or has any human-readable structure.
- A user has at most **one active** `google_connections` row at a time. A
  re-connect should mark the previous row `revoked` and insert a new row.
- Audit log redaction: never log raw email bodies, file contents, attendee
  lists, etc. Log counts, lengths, and opaque IDs only.

---

## 6. Required API endpoints

All endpoints respond with `application/json`. All errors use a stable shape:

```json
{ "error": { "code": "string", "message": "string" } }
```

Auth: every endpoint except a health probe requires `Authorization: Bearer
<token>` (see §7). The token must validate the Sky10 identity claim before
the request is allowed to reference a `userId`.

### `POST /api/integrations/google/connect/start`

Create a hosted Google connection session for the caller and return the URL
the client should open in a popup.

Request:

```json
{
  "userId": "string",
  "returnUrl": "string (optional, must be in SKY10_ALLOWED_ORIGINS if set)"
}
```

Response:

```json
{
  "connectUrl": "string",
  "connectionId": "string",
  "provider": "composio"
}
```

Behavior:
- Upsert `users` row.
- Mark any existing `active` or `pending` `google_connections` rows for the
  user as `revoked` (idempotent re-connect).
- Call the provider to create a connect session for `entity_id = userId`
  (Composio: `entity.initiateConnection({...})`; Pipedream: create connect
  token with `external_user_id = userId`).
- Insert a new `google_connections` row with `status = "pending"`.
- Return the provider's hosted URL plus the local `connectionId`.

Do **not** redirect server-side. The Sky10 client will open the URL in a
popup and either listen for a postMessage from the broker's callback page
or poll `/status`.

### `GET /api/integrations/google/connect/callback`

Optional. Implement only if the chosen provider redirects to your origin
after consent.

- For **Composio**: the connect link redirects to a Composio-owned page by
  default; you do not need a callback unless you opt into "redirect to your
  app" mode. If unused, document that explicitly in the route comment.
- For **Pipedream**: the Connect flow redirects to your `success_url`
  (which can be your callback). Implement this if you want a polished
  "connection complete, you can close this tab" page.

If implemented, the callback must:
- Verify a signed `state` parameter against `SKY10_SESSION_SECRET` (HMAC).
- Resolve the `connectionId` it encoded.
- Confirm the connection with the provider, write
  `status = "active"` (or `"error"` with `last_error`), and render a
  minimal HTML page that:
  - posts a `{ type: "sky10:google-connect", status, connectionId }`
    message to `window.opener` if present,
  - then auto-closes.
- The callback should never display PII (no email, no name) — just the
  status.

### `GET /api/integrations/google/status?userId=<id>`

Response:

```json
{
  "connected": true,
  "status": "none | pending | active | revoked | error",
  "provider": "composio",
  "connectedAccountId": "string (optional)",
  "availableTools": ["gmail.search_messages", "calendar.list_events", "..."],
  "lastError": "string (optional, present when status is error)"
}
```

Behavior:
- Return `status: "none"` and `connected: false` when there is no row.
- For `pending`, optionally re-query the provider to upgrade to `active`
  before responding (lazy promotion).
- `availableTools` is the **intersection** of the broker's allowlist (§8)
  and the tools the provider actually exposes for this user / app version.
  The client uses this list to decide which Google tools to register with
  the AI SDK.

### `POST /api/integrations/google/disconnect`

Request:

```json
{ "userId": "string" }
```

Behavior:
- Call provider revoke if supported (Composio: delete connected account;
  Pipedream: delete account). Tolerate "already gone" responses.
- Mark the row `status = "revoked"`, set `updated_at`.
- Return `{ "ok": true }`.

### `POST /api/tools/google/execute`

Request:

```json
{
  "userId": "string",
  "tool": "string",
  "input": { "...": "..." }
}
```

Response (success):

```json
{ "ok": true, "result": { "...": "..." } }
```

Response (failure):

```json
{
  "ok": false,
  "error": { "code": "string", "message": "string" },
  "needsReconnect": false
}
```

Behavior:
- Validate `tool` against the allowlist (§8). Reject anything else with
  `error.code = "tool_not_allowed"`.
- Validate `input` with a per-tool zod schema before forwarding it to the
  provider. Be conservative; reject unknown fields.
- Look up the active `google_connections` row for `userId`. If none or
  status is not `active`, return `needsReconnect: true`.
- Execute via the provider abstraction (§12).
- Update `last_used_at` on the connection.
- Insert a `tool_audit_log` row with redacted input/output summaries.
- For **write actions** (§9), require an `X-Sky10-Confirmed: true` header
  from the client. The client is responsible for surfacing the confirmation
  prompt to the user; the broker enforces that the header is set before
  forwarding the call.

### `GET /healthz`

Returns `{ "ok": true, "version": "..." }`. No auth required. Used for
liveness probes only — no app data may leak from this endpoint.

---

## 7. Security requirements

The broker is the security boundary between the Sky10 client and Google.
Treat it as such.

- **Never** expose the provider API key to the client. Only the broker
  process should have it in memory.
- **Never** expose raw Google OAuth tokens to the client or to any LLM.
  The provider SDK is the only thing that sees the token.
- **Authentication.** Every API request must carry a bearer token that
  proves the caller controls the `userId` it is acting on. The Sky10
  client's stable user identifier is its libp2p ed25519 identity. Two
  acceptable mechanisms for MVP:
  1. **Signed user assertion** (recommended). The Sky10 daemon holds the
     identity private key and signs a short-lived JWT (`{ sub: <address>,
     iat, exp, aud: "sky10-broker" }`) using ed25519. The broker verifies
     the signature against the public key embedded in `sub` (libp2p
     addresses are derivable to a public key) or against a key the daemon
     publishes once per session. The client attaches this JWT as
     `Authorization: Bearer ...`.
  2. **Broker-issued session token.** A first-call handshake exchanges a
     daemon-signed assertion for a short-lived session token (HS256 with
     `SKY10_SESSION_SECRET`). The client uses the session token thereafter.
  Pick one, document it, and reject unauthenticated requests. Do not
  trust a `userId` field on its own — it is forgeable.
- **Bind every connection to a stable Sky10 user ID.** No anonymous
  connections, no shared `entity_id`s.
- **Validate `returnUrl`** against `SKY10_ALLOWED_ORIGINS`. Reject any URL
  whose origin is not in the allowlist. Strip query params/fragments
  before redirecting if you reflect the URL.
- **CORS.** Allowlist only the origins in `SKY10_ALLOWED_ORIGINS`. No
  wildcard. Allow credentials only if you actually need cookies (you
  probably do not — bearer tokens are simpler).
- **Rate limit.** Apply per-user limits on:
  - `connect/start` (e.g., 10 / hour / userId)
  - `tools/execute` (e.g., 60 / minute / userId, with a smaller burst)
  Use a sliding window in memory for MVP; move to Redis / KV when
  multi-instance.
- **Tool allowlist enforcement.** §8. Do not provide a generic "passthrough
  any provider tool" endpoint. Each allowed tool gets an explicit handler
  with an explicit input schema.
- **Audit logging.** Log every tool execution with redacted summaries.
  Retain logs for at least 30 days. Provide an admin-only endpoint or
  database query path to export a user's logs on request.
- **Revoke / disconnect.** Always available, regardless of provider state.
  If the provider revoke call fails, still mark the local row revoked and
  surface the provider error in `last_error`.
- **Expired / revoked connections.** When the provider returns "auth
  required" or 401 for a tool call, the broker must:
  - mark the connection `status = "error"` with a clear `last_error`,
  - return `{ ok: false, needsReconnect: true }` to the client.
- **Logging hygiene.** Never log:
  - Bearer tokens.
  - Provider API keys.
  - Email bodies, attachments, file contents, attendee email addresses.
  - Full input/output objects from tool calls.
  Log only opaque IDs, counts, lengths, and error codes.

---

## 8. MVP Google tool allowlist

These are categories. Map each to the actual provider action name (for
Composio: `GMAIL_FETCH_MESSAGE_BY_ID`, etc.; for Pipedream: the action
slug). Maintain a `TOOLS` table in code that lists `{ allowlistName,
providerAction, inputSchema, outputProjection, isWrite }`.

Read-only (allowed after connect, no extra confirmation):

- **Gmail**
  - `gmail.list_recent_messages`
  - `gmail.search_messages`
  - `gmail.get_message`
  - `gmail.get_thread`
- **Calendar**
  - `calendar.list_calendars`
  - `calendar.list_events`
  - `calendar.get_event`
- **Drive**
  - `drive.search_files`
  - `drive.get_file_metadata`
  - `drive.read_file_content` (only for files the user explicitly references
    or that the search step returned; do not allow arbitrary file IDs the
    LLM made up)
- **Docs / Sheets**
  - `docs.read_document`
  - `sheets.read_range`

Write / state-changing (allowed only with `X-Sky10-Confirmed: true`):

- **Gmail**
  - `gmail.create_draft`
  - `gmail.send_email`
- **Calendar**
  - `calendar.create_event`
  - `calendar.update_event`
  - `calendar.delete_event`
- **Drive**
  - `drive.create_file`
  - `drive.move_file`
  - `drive.delete_file`
  - `drive.share_file` (treat as high-risk — see §9)
- **Docs / Sheets**
  - `docs.update_document`
  - `sheets.update_range`

Per tool:
- Define a strict zod schema. Unknown fields are rejected.
- Project the provider response to the minimum the client needs. Do not
  pass through the full provider response if it contains tokens, refresh
  tokens, or PII not relevant to the agent's task.
- Tag with `risk: "low" | "medium" | "high"` so the client UI can render
  appropriate confirmation dialogs.

When a provider does not implement a category, document it in the README.
Do not silently fall back to a different surface.

---

## 9. Confirmation policy

The broker treats writes as a privileged operation. Implementation:

- Each tool's handler declares `isWrite: boolean`.
- For `isWrite: true`, the broker requires `X-Sky10-Confirmed: true`. If
  missing, respond:

  ```json
  {
    "ok": false,
    "error": {
      "code": "confirmation_required",
      "message": "This action modifies the user's Google data. The Sky10 client must surface an explicit confirmation prompt and resend with X-Sky10-Confirmed: true."
    }
  }
  ```

- Treat the following as **high-risk** writes that should be flagged
  separately so the client can render a stronger warning:
  - `gmail.send_email`
  - `calendar.delete_event`
  - `drive.delete_file`
  - `drive.share_file` (especially with public / "anyone with link"
    permissions)
  - `docs.update_document` and `sheets.update_range` when targeting
    existing documents (vs creating new ones)
- The broker does not cache or batch confirmations. Each write call must
  be confirmed individually. The client is responsible for showing the
  user what is about to happen with concrete values (recipient, subject,
  affected file name) before sending the confirmed call.

---

## 10. Client contract (Sky10 OSS client)

This section reflects the actual Sky10 client repo as of this prompt.

- **Frontend framework.** React 19 + Vite 6 + TypeScript (strict). Tailwind
  for styling. The web UI ships embedded inside the Go binary at runtime
  but is developed standalone.
- **Package manager.** Bun (`bun@1.3.11`).
- **AI runtime.** Vercel AI SDK v6 (`ai` ^6.0.168). Tools are defined with
  `tool({ description, inputSchema, execute, needsApproval })` and live in
  `web/src/lib/rootAssistantTools.ts`. Each tool has metadata:

  ```ts
  type RootAssistantToolPolicy = "approval_required" | "disabled" | "read_only"
  type Risk = "low" | "medium" | "high"
  ```

  The Google tools the client will register must follow this same shape
  and policy/risk classification.
- **Existing auth/session.** None at the HTTP layer. The client talks to a
  local Go daemon on `http://localhost:9101` over JSON-RPC. The user
  identity is libp2p ed25519 (`identity.show()` returns `address`,
  `device_id`, `device_pubkey`). Use `address` as the broker's `userId`.
- **API base URL.** Read from `import.meta.env.VITE_SKY10_API_URL` at
  runtime (Vite convention). Document this in the client repo's
  `.env.example` and CI build config. If unset, the client renders an
  "Integrations not configured" state and disables Connect Google.
- **Recommended client-facing TypeScript types** (the broker should mirror
  these or generate them):

  ```ts
  export type GoogleConnectionStatus =
    | "none" | "pending" | "active" | "revoked" | "error"

  export interface GoogleConnectionInfo {
    connected: boolean
    status: GoogleConnectionStatus
    provider: "composio" | "pipedream" | "arcade"
    connectedAccountId?: string
    availableTools: string[]
    lastError?: string
  }

  export interface GoogleConnectStartResponse {
    connectUrl: string
    connectionId: string
    provider: "composio" | "pipedream" | "arcade"
  }

  export interface GoogleToolExecuteResponse<T = unknown> {
    ok: boolean
    result?: T
    error?: { code: string; message: string }
    needsReconnect?: boolean
  }
  ```

- **UI states the client expects to render** (see also
  `docs/google-agent-auth-client-plan.md` in the client repo):
  - `none` — show "Connect Google" CTA.
  - `pending` — show a "Finishing connection…" spinner; poll `/status`
    every ~2s until `active` or `error`.
  - `active` — show the connected account label (or "Connected" if no
    label is exposed), a list of `availableTools`, and a Disconnect
    button.
  - `revoked` — show "Connection was disconnected. Reconnect to continue."
  - `error` — show `lastError`, with Reconnect and Disconnect buttons.

- **Confirmation prompts.** The Sky10 client already has an
  `approval_required` policy in its tool runtime. Mark every Google
  *write* tool as `approval_required` and forward `X-Sky10-Confirmed: true`
  only after the user approves the AI SDK approval prompt. The broker
  enforces this independently — do not rely on the client alone.

---

## 11. Implementation tasks for the backend repo

Build in this order. Each step should be a single small commit with tests.

1. **Init.** `bun init`, add `tsconfig.json` (strict, ESNext, bundler
   resolution). Add Hono, zod, drizzle-orm, better-sqlite3 (or
   `bun:sqlite`).
2. **Env validation.** `src/env.ts` parses `process.env` through a zod
   schema and freezes the result. Export typed `env`.
3. **HTTP server.** `src/server.ts` wires Hono with CORS (allowlist from
   env), JSON body parsing, request logger (no PII), and a `/healthz`
   route.
4. **Auth middleware.** `src/auth.ts` verifies the bearer token (see §7),
   attaches `ctx.set("userId", ...)`. Reject if missing or invalid.
5. **DB.** `src/db/schema.ts` (Drizzle), `src/db/migrate.ts`. Use SQLite
   for MVP. Run migrations at boot.
6. **Provider abstraction.** `src/providers/index.ts` exports the
   interface in §12. `src/providers/composio.ts` implements it.
7. **Tool registry.** `src/tools/registry.ts` exports `TOOLS` (the
   allowlist in §8) with input schemas, output projections, and
   write/read flag. The provider tells the broker which provider action
   each entry maps to.
8. **API routes.** `src/routes/integrations.google.ts` and
   `src/routes/tools.google.ts` implement §6. Use the provider
   abstraction; do not import Composio directly here.
9. **Audit logging.** `src/audit.ts` writes redacted summaries from
   shared helpers so each tool handler does not reinvent redaction.
10. **Tests.**
    - Unit: input schema validation, tool allowlist enforcement, write
      requires `X-Sky10-Confirmed`, returnUrl origin check.
    - Integration: a fake `GoogleAuthProvider` exercising the full
      connect → status → execute → disconnect lifecycle. Do not hit the
      real Composio API in CI; record/replay or stub.
11. **README.md.** Setup steps, env variables, local dev (`bun run dev`),
    how to obtain a Composio API key, how to register a Google auth
    config in Composio, how to deploy.
12. **`.env.example`.** Mirrors §4. No real values.
13. **Local dev command.** `bun run dev` should start the server, run
    migrations, and watch for changes.
14. **Deployment notes.** Document Bun-on-Render, Bun-on-Fly, and
    Node-on-Vercel-Functions paths. Workers is not the first target — see
    §3.

What **not** to build at MVP:
- A user dashboard.
- Multi-tenant admin UI.
- A workflow / cron / trigger system.
- Custom Google OAuth (BYO is a later enhancement, gated by env vars).
- Provider plugins beyond Composio.

---

## 12. Provider abstraction

Define this interface and program against it. The route handlers must
never import a provider SDK directly.

```ts
export type GoogleConnectionStatus =
  | "none" | "pending" | "active" | "revoked" | "error"

export interface GoogleConnectionInfo {
  connected: boolean
  status: GoogleConnectionStatus
  provider: "composio" | "pipedream" | "arcade"
  connectedAccountId?: string
  availableTools: string[]
  lastError?: string
}

export interface ConnectLinkResult {
  connectUrl: string
  providerConnectionId: string
}

export interface GoogleAuthProvider {
  readonly name: "composio" | "pipedream" | "arcade"

  createConnectLink(
    userId: string,
    returnUrl?: string,
  ): Promise<ConnectLinkResult>

  getStatus(userId: string): Promise<GoogleConnectionInfo>

  revoke(userId: string): Promise<void>

  executeTool(
    userId: string,
    tool: string,
    input: unknown,
  ): Promise<{ ok: true; result: unknown }
           | { ok: false; error: { code: string; message: string }; needsReconnect?: boolean }>
}
```

Initial implementation:
- `ComposioGoogleAuthProvider` — uses the `composio` SDK with
  `entity_id = userId` and the `COMPOSIO_GOOGLE_AUTH_CONFIG_ID` from env.

Future implementations (do not build yet, but the interface must support
them):
- `PipedreamGoogleAuthProvider`
- `ArcadeGoogleAuthProvider`

Pick the active provider via env (`SKY10_PROVIDER=composio|pipedream|arcade`,
default `composio`). The selection happens once at boot in
`src/providers/index.ts`.

---

## 13. Open questions for the Sky10 client team

Surface these in the README and ask the Sky10 client team to answer before
the broker ships to production:

1. **User accounts.** Sky10 has no central user accounts — identity is a
   libp2p ed25519 keypair. Do we want the broker to *require* a daemon-
   signed JWT (§7, option 1), or do we want a separate Sky10 cloud account
   service to issue session tokens? Recommendation: daemon-signed JWT for
   MVP; revisit when there is a Sky10 cloud account.
2. **Local-first.** The Sky10 app is local-first today. Is the broker
   expected to be hosted by Sky10 (one shared instance, central rate
   limits) or self-hostable by power users (each user runs their own
   broker, points the client at it)? Recommendation: design for both —
   Sky10-hosted by default, self-host documented.
3. **Where agents run.** The Sky10 client runs agents both in-process
   (Vercel AI SDK in the browser, calling local-daemon RPC tools) and in
   Lima sandboxes (`pkg/sandbox`). Should Google tool calls always go
   through the broker over HTTP, or should the browser get a short-lived
   tool-execution capability that is bound to a specific tool + input?
   Recommendation: always HTTP for MVP. Capability tokens are a v2
   optimization.
4. **Tool execution layering.** Sky10 already has a typed tool layer with
   policy + approval (`web/src/lib/rootAssistantTools.ts`). Should Google
   tools be registered as additional entries in that file, or in a
   dedicated `googleTools` set that the chat page conditionally merges
   when a connection is active? Recommendation: a dedicated set, merged at
   chat start time, so the existing `rootAssistantTools` stays
   identity/daemon-only.
5. **Provider tool format vs Sky10 tool format.** The broker exposes tools
   under stable names like `gmail.send_email`. The provider returns its
   own response shape. The broker projects to a stable Sky10-friendly
   shape (§8). Should that projection live in the broker or in the
   client? Recommendation: in the broker. The client should not have to
   change when the broker swaps providers.
6. **Sandbox-bound execution.** Sky10 has `sandbox.secrets.attach` to
   inject sky10 secrets as env vars into Lima sandboxes. Should the
   broker also be able to mint a short-lived per-sandbox API token that
   the sandboxed agent can use directly? Out of scope for MVP, but worth
   keeping the door open in the data model.

---

## Final deliverable expectations

When the backend is ready, post a single message back to the Sky10 client
team with:

- The deployed broker base URL.
- The `VITE_SKY10_API_URL` value to set in the client.
- The bearer-token format the client should produce (per §7 decision).
- A short README excerpt of what worked, what is stubbed, and what is
  intentionally deferred.
- Any deviations from this prompt and why.
