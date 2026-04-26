---
created: 2026-04-26
updated: 2026-04-26
model: gpt-5.5
---

# Slack Messenger Adapter

This is the first external Slack adapter bundle for Sky10. It speaks the
Sky10 messaging adapter protocol over JSON-RPC on stdio and calls Slack's Web
API with a staged bot/user token credential.

## Credential

Use a credential JSON payload with one of these token fields:

```json
{
  "bot_token": "xoxb-...",
  "team_id": "T123"
}
```

Supported token fields are `bot_token`, `access_token`, `token`, and
`slack_bot_token`.

## Connection Metadata

Optional connection metadata:

- `slack_api_base_url`: overrides `https://slack.com/api` for tests or
  compatible gateways.
- `slack_team_id`: sets the workspace/team ID when the token is org-wide.
- `slack_conversation_types`: comma-separated Slack conversation types. Default
  is `public_channel,private_channel,im,mpim`.
- `slack_history_limit`: maximum history page size. Default is `15`.

## Slack Scopes

Useful Slack scopes for the current adapter surface:

- `chat:write` for `chat.postMessage`.
- `channels:read`, `groups:read`, `im:read`, and `mpim:read` for
  `conversations.list`.
- `channels:history`, `groups:history`, `im:history`, and `mpim:history` for
  `conversations.history` and `conversations.replies`.
- `users:read` for `users.list`.
- `search:read` for `search.messages` where available.

Slack scope requirements vary by token type and conversation type, so the
adapter reports platform errors directly when the workspace token is missing a
scope.
