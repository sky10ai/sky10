# Sky10 Client Plan — Google Agent Auth (Connect Google)

This is the Sky10 OSS client-side plan for adding one-click "Connect Google"
support so agents can use Gmail, Calendar, Drive, Docs, and Sheets on the
user's behalf.

The backend that issues hosted Google connect sessions and proxies tool
execution lives in a **separate repository**. The implementation prompt for
that backend is at [`docs/google-agent-auth-backend-prompt.md`](./google-agent-auth-backend-prompt.md).

This file describes only what changes inside this client repo.

---

## TL;DR

- **Recommended provider for the broker: Composio.** Fallback: Pipedream
  Connect. Not Arcade for first. Reasoning is in the backend prompt.
- The client never holds a Google OAuth client secret, never holds a
  Composio / Pipedream / Arcade API key, and never sees a raw Google OAuth
  token.
- The client talks to a separate Sky10 backend at the URL set in
  `VITE_SKY10_API_URL`. No backend URL means the integration UI is shown
  as "Not configured" and is disabled.
- The Sky10 user identifier the broker keys off is the libp2p identity
  address from `identity.show().address`. That call already exists in the
  daemon RPC layer (`web/src/lib/rpc.ts`).
- A typed client lives at `web/src/lib/googleIntegrationClient.ts`. A React
  hook lives at `web/src/lib/useGoogleConnection.ts`. Neither hardcodes a
  provider name in user-visible text — the UI says "Connect Google".

---

## Where the Connect Google UI belongs

The repo has these existing settings pages (`web/src/pages/`):

- `Settings.tsx` — the settings hub.
- `SettingsApps.tsx` — managed *binaries* (OWS wallet, Lima). Not a fit for
  external SaaS integrations.
- `SettingsCodex.tsx` — ChatGPT/Codex sign-in. The closest UX analog: a
  button that opens an OAuth popup and waits for completion.
- `SettingsMessaging.tsx` — messaging adapters (Gmail, IMAP, etc.). This is
  the closest *conceptual* page, but it is scoped to messaging adapters
  with their own connection model.
- `SettingsSecrets.tsx` — encrypted-secret management.

There is **no existing "Integrations" page** for external SaaS tools.

Recommendation:

1. Add a new route `settings/integrations` rendering an `Integrations` page
   that lists third-party services Sky10 agents can use. Google is the
   first entry. Future entries (Notion, Linear, Slack, etc.) slot in here.
2. Until that page exists, the **Connect Google** card can also be
   surfaced from the Agent chat sidebar when an agent attempts a Google
   tool while disconnected (an inline "Connect Google to continue" CTA).
3. Reuse the existing `SettingsPage` shell, `StatusBadge`, `Icon`, and
   `oauthPopup.ts` primitives. The visual structure should mirror
   `SettingsCodex.tsx`.

This client plan does **not** add the page itself. It adds the typed client
and hook so the page is a small follow-up. See "Adding the Integrations
page" below.

---

## Proposed UI states

The hook returns a `GoogleConnectionInfo`:

```ts
type GoogleConnectionStatus =
  | "none" | "pending" | "active" | "revoked" | "error"

interface GoogleConnectionInfo {
  connected: boolean
  status: GoogleConnectionStatus
  provider: "composio" | "pipedream" | "arcade"
  connectedAccountId?: string
  availableTools: string[]
  lastError?: string
}
```

UI states:

| Status | What the user sees |
|---|---|
| `none` | "Connect your Google account so agents can read your inbox, calendar, and Drive." Primary button: **Connect Google**. |
| `pending` | "Finishing connection…" with a spinner. The popup is open. The hook polls every 2s until `active` or `error`. |
| `active` | Green check, "Connected" (with account label if available). A short summary like "12 tools available". Buttons: **Disconnect**, **Reconnect**. |
| `revoked` | "Your Google connection was disconnected. Reconnect to keep using Google tools." Button: **Reconnect**. |
| `error` | Show `lastError` in a non-alarming red. Buttons: **Reconnect**, **Disconnect**. |

Wording rules:
- Always say "Google" in the consumer copy, never "Composio" / "Pipedream" /
  "Arcade".
- Do not surface scope strings to the user — show category descriptions
  ("Read recent emails", "Create calendar events with confirmation").
- Write actions must trigger the AI SDK approval prompt (see
  `rootAssistantTools.ts` `approval_required` policy) before the agent
  calls them. The broker also enforces this with `X-Sky10-Confirmed`.

---

## How the client calls the backend

The typed client is `web/src/lib/googleIntegrationClient.ts`. It exposes:

```ts
export const googleIntegration = {
  startConnect(returnUrl?: string): Promise<GoogleConnectStartResponse>
  getStatus(): Promise<GoogleConnectionInfo>
  disconnect(): Promise<{ ok: true }>
  executeTool<T = unknown>(
    tool: string,
    input: unknown,
    opts?: { confirmed?: boolean },
  ): Promise<GoogleToolExecuteResponse<T>>
}
```

The client:

1. Reads `import.meta.env.VITE_SKY10_API_URL` at module load. If unset,
   throws a clear `Sky10IntegrationsNotConfiguredError` from any method.
2. Resolves `userId` lazily by calling `identity.show()` from the existing
   RPC client. The result is cached for the page lifetime.
3. Sends `Authorization: Bearer <token>` using a `getAuthToken()` callback.
   For MVP this can be a no-op (returning empty), but the production
   wiring is a daemon-signed assertion (see "Authentication" below).
4. For `executeTool`, sends `X-Sky10-Confirmed: true` only when
   `opts.confirmed` is true.

The hook `useGoogleConnection()`:

- Loads status on mount via `useRPC`-style state (not reusing `useRPC`
  itself, since `useRPC` is RPC-client-bound).
- Exposes `{ data, error, loading, startConnect, disconnect, refetch }`.
- Automatically refetches when the OAuth popup closes (we listen for a
  `postMessage` from the broker callback page, with a polling fallback).

---

## How the agent checks whether Google is connected

Two patterns:

1. **At chat-start.** When `AgentChat.tsx` mounts a chat session, call
   `googleIntegration.getStatus()` once. If `status === "active"`, fetch
   `availableTools` and merge them into the `ToolSet` passed to the AI SDK
   for that session. If `status !== "active"`, do not register any Google
   tools.

2. **At tool-call time.** If the model attempts a Google tool while the
   connection is `none`/`revoked`/`error`, the broker returns
   `needsReconnect: true`. The chat UI should catch that error code and
   render an inline "Connect Google" CTA in the message stream (similar
   to how missing-secret errors are surfaced today).

The Google tools should slot into the existing tool-policy model in
`web/src/lib/rootAssistantTools.ts`:

- Read tools: `policy: "read_only"`, `risk: "low"` or `"medium"`.
- Write tools: `policy: "approval_required"`, `risk: "medium"` or `"high"`.

Keep them in a separate exported set (e.g. `googleTools`) and merge at
chat-init time. Do not pollute `rootAssistantTools` (which is currently
identity/daemon-only) with optional cloud tools. This keeps the existing
tools usable when the broker is unreachable.

---

## How the agent requests Google tool execution

Each Google tool's `execute` function calls
`googleIntegration.executeTool(<toolName>, input)`. For write tools, the
chat UI must:

1. Surface the AI SDK approval prompt (already supported via
   `needsApproval: true` on the tool definition).
2. On approve, re-call `executeTool(name, input, { confirmed: true })`.
3. On deny, return a structured "user denied" result so the model can
   summarize cleanly.

The tool's `execute` should never call Google directly. It always goes
through the broker. The model never sees a raw access token.

---

## Authentication (signed user assertion)

The Sky10 user identifier is a libp2p ed25519 identity address. The broker
must verify that the caller controls that identity before accepting a
`userId` in any request.

For MVP wiring, the client calls `getAuthToken()` which (initially) returns
an empty string; the broker may run with auth disabled in `NODE_ENV ===
"development"`. This is **not safe for production**.

Production wiring (small follow-up, not in this change):

1. Add a daemon RPC `identity.signBrokerAssertion({ aud: "sky10-broker",
   ttl_s: 300 })` that returns a short-lived ed25519-signed JWT with
   `{ sub: <address>, iat, exp, aud }`.
2. The client's `getAuthToken()` calls that RPC and caches the JWT until
   ~30s before expiry.
3. The broker verifies the signature against the libp2p public key
   embedded in `sub` (or against a key the daemon publishes once per
   session).

Until that is in place, **do not ship the integration UI to production**.

---

## Secrets that must never be committed

- Google OAuth client secrets (`client_secret.json`, etc.) — Sky10 does
  not own a Google OAuth app at MVP; the broker's provider does.
- Composio / Pipedream / Arcade API keys — only the broker process holds
  these.
- `VITE_SKY10_API_URL` is **not** a secret (it ends up in the JS bundle).
  But the broker URL chosen for production must already require auth.

The client repo's `.gitignore` already excludes `.env*`. Keep it that way.
Add `web/.env.example` (this change) but not `web/.env*`.

---

## Required env var for backend URL

`VITE_SKY10_API_URL` — the base URL of the Sky10 broker, e.g.,
`https://api.sky10ai.com` in production or `http://localhost:8787` in
dev.

- Vite exposes only `VITE_*` env vars to client code (`import.meta.env`).
- The vite config already reads `SKY10_WEB_RPC_TARGET` for the daemon
  proxy; this new variable is read at runtime by the client lib, not at
  vite-build time, so the existing proxy config does not need changes.

For local dev, a developer running both the broker and the Sky10 web UI
should:

```sh
echo 'VITE_SKY10_API_URL=http://localhost:8787' > web/.env.local
bun --cwd web dev
```

The client falls back to a clear "Sky10 backend not configured" message
when this is missing — it does not silently break.

---

## Recommended provider and fallback

- **Primary: Composio.** It has a first-party Vercel AI SDK adapter, an
  `entity` model that maps cleanly to Sky10's libp2p identity address, and
  a "Composio-managed OAuth" tier that means consumers do not have to set
  up Google Cloud and Sky10 does not need to ship a Google OAuth client
  at MVP. The Sky10 web client already uses the Vercel AI SDK, so this is
  the smallest integration cost.
- **Fallback: Pipedream Connect.** Equivalent end-user UX, broader app
  catalog if Sky10 expands beyond Google, mature MCP server. Swap by
  changing `SKY10_PROVIDER` in the broker — no client changes required if
  the broker's tool allowlist names stay stable.
- **Not Arcade for first.** Arcade's per-tool-call authorization model
  conflicts with the "Connect Google once in Settings, then just use
  tools" UX Sky10 is going for. Strong second pick later if Sky10 wants
  per-action consent prompts.

The provider choice is **invisible** to Sky10 consumer UI. The
`provider` field is exposed in the status response only for diagnostics;
do not render it in user-facing copy.

---

## Files added in this client change

- `docs/google-agent-auth-backend-prompt.md` — the standalone prompt for
  the backend repo's AI agent.
- `docs/google-agent-auth-client-plan.md` — this file.
- `web/src/lib/googleIntegrationClient.ts` — typed client to the broker.
- `web/src/lib/useGoogleConnection.ts` — React hook that wraps the
  client.
- `web/.env.example` — documents `VITE_SKY10_API_URL`.

No UI page is added in this change. See "Adding the Integrations page"
below for the small follow-up.

---

## Adding the Integrations page (follow-up, not in this change)

When ready:

1. Create `web/src/pages/SettingsIntegrations.tsx` modeled on
   `SettingsCodex.tsx`. It should:
   - Use `useGoogleConnection()` for state.
   - Use `openOAuthPopup` / `navigateOAuthPopup` /
     `closeOAuthPopup` from `web/src/lib/oauthPopup.ts` to open the
     `connectUrl` returned by `startConnect`.
   - Listen for a `postMessage` from the broker callback page
     (`{ type: "sky10:google-connect", status, connectionId }`) plus a
     polling fallback that refetches `getStatus()` every 2s while the
     popup is open.
   - Show the UI states described above.
2. Register the route in `web/src/App.tsx`:
   ```tsx
   <Route path="settings/integrations" element={<SettingsIntegrations />} />
   ```
3. Add a sidebar/settings entry pointing to `/settings/integrations`.
4. (Optional) Add a `pinnablePageID` for the Integrations page, mirroring
   how other settings pages register themselves.

When that page lands, also register the Google tools in a new
`web/src/lib/googleTools.ts` (mirrors `rootAssistantTools.ts` shape) and
merge it into the chat tool set in `AgentChat.tsx` when
`getStatus().status === "active"`.
