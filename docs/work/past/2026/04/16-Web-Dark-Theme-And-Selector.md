---
created: 2026-04-16
model: gpt-5.4
---

# Web Dark Theme And Selector

This entry covers the web-theme work that landed in `0d825de`
(`feat(web): add system-aware dark theme`), `766bb98`
(`fix(web): strengthen dark chat bubble contrast`), and `7e390ae`
(`fix(web): simplify theme control label`).

The goal was straightforward: make the React web UI usable in dark mode
without turning the feature into a one-off set of scattered `dark:` patches,
and expose a user-facing theme selector that follows the operating system by
default.

## Why

The web UI already had a modern shell and a partial Tailwind dark-mode
configuration, but it was still effectively light-only:

- the Tailwind palette was hardcoded to light values
- the app had no persistent theme state
- the root document did not apply theme before React rendered
- several pages assumed a light canvas even when the sidebar had dark overrides

That meant "add dark mode" was not just a cosmetic toggle. The UI needed a
coherent theme model so the command center could look intentional in both
appearances instead of feeling like a light UI with a dark sidebar bolted on.

## What Shipped

### 1. The web UI now has a real theme model

The frontend now uses a three-state preference model:

- `system`
- `light`
- `dark`

`system` is the default. When no override is stored, the app follows
`prefers-color-scheme`. Explicit `light` and `dark` choices are persisted in
`localStorage`.

The main implementation lives in:

- [`web/src/components/ThemeProvider.tsx`](../../../../../web/src/components/ThemeProvider.tsx)
- [`web/index.html`](../../../../../web/index.html)
- [`web/src/main.tsx`](../../../../../web/src/main.tsx)

This existed because a binary light/dark toggle would have been the wrong model
for the product requirement. "Follow system unless the user overrides it"
requires a real preference state, not a boolean.

### 2. Theme is applied before React renders

The root HTML document now resolves theme immediately on page load, applies the
`dark` class on `<html>`, sets `data-theme` metadata, and updates
`color-scheme` before the React app mounts.

This mattered because otherwise the page would flash the wrong appearance on
load whenever the stored or system theme differed from the default CSS.

### 3. Tailwind color tokens now route through CSS variables

The existing semantic palette was preserved, but the Tailwind color config now
resolves from CSS variables instead of fixed hex values. Light and dark token
sets live in [`web/src/index.css`](../../../../../web/src/index.css), so
existing classes such as `bg-surface-container-lowest`,
`text-on-surface`, and `border-outline-variant` can flip automatically.

Relevant files:

- [`web/tailwind.config.ts`](../../../../../web/tailwind.config.ts)
- [`web/src/index.css`](../../../../../web/src/index.css)

This mattered because the UI already used semantic surface and text tokens
widely. Replacing the token source was the right leverage point; sprinkling
manual `dark:` overrides everywhere was not.

### 4. A user-facing selector was added to the top-right header

The global theme control now lives in the top-right of the app header and opens
the three choices: `System`, `Light`, and `Dark`.

The final control intentionally shows as an icon-only affordance instead of
rendering the active theme name inline. Hover text explains whether the UI is
following the system appearance or locked to a specific theme.

Relevant files:

- [`web/src/components/Header.tsx`](../../../../../web/src/components/Header.tsx)
- [`web/src/components/ThemeControl.tsx`](../../../../../web/src/components/ThemeControl.tsx)

This mattered because the control needed to be globally visible on every page,
but the visible `System` / `Light` / `Dark` label in the header felt noisy once
the interaction model was clear.

### 5. Shared shell and page hotspots were cleaned up for dark mode

The dark-theme work also included a first pass over the places where semantic
tokens alone were not enough:

- sidebar active/idle navigation states
- status badges
- primary buttons and gradients
- the network dashboard graph/background/status chips
- the invite flow hero cards
- the settings skylink mode card
- assorted button text that still assumed white-on-primary semantics

Representative files:

- [`web/src/components/Sidebar.tsx`](../../../../../web/src/components/Sidebar.tsx)
- [`web/src/components/StatusBadge.tsx`](../../../../../web/src/components/StatusBadge.tsx)
- [`web/src/pages/Network.tsx`](../../../../../web/src/pages/Network.tsx)
- [`web/src/pages/InviteDevice.tsx`](../../../../../web/src/pages/InviteDevice.tsx)
- [`web/src/pages/Settings.tsx`](../../../../../web/src/pages/Settings.tsx)

This mattered because the command center had several bespoke visual surfaces
that would not become legible in dark mode just by flipping the base tokens.

### 6. Agent chat needed a dark-mode readability follow-up

After the first dark-theme pass, the agent-response bubble in chat blended too
closely into the surrounding dark surfaces. `766bb98` tightened the message
area contrast, added a clearer bubble border, and made code blocks read as
their own nested surfaces again.

Relevant file:

- [`web/src/pages/AgentChat.tsx`](../../../../../web/src/pages/AgentChat.tsx)

This mattered because the chat page is one of the most important interactive
surfaces in the web UI, and dark mode is only successful if the reply content
remains obviously readable.

## User-Facing Outcome

After this work:

- the web UI follows the operating system appearance by default
- a user can explicitly lock the UI to light or dark mode
- the preference persists locally in the browser
- the app applies the right theme before React mounts
- the shared shell and primary screens now read correctly in dark mode
- the header control stays compact while still explaining its behavior on hover

This did not attempt a pixel-perfect visual redesign of every page. It
established the real theme foundation and cleaned up the most visible dark-mode
failures so the feature is usable and coherent as part of the product.
