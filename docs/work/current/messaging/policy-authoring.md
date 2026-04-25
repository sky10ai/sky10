---
created: 2026-04-25
updated: 2026-04-25
model: gpt-5.5
---

# Messaging Policy Authoring

Messaging policy needs two layers:

- a conversational intent that a human can understand and edit
- a compiled rule set that the broker can enforce deterministically

The policy file is the bridge between those layers. An AI can draft or update
the file, but the broker should apply AI-generated policy only after explicit
human review.

## Canonical File Shape

Use YAML for human editing:

```yaml
kind: sky10.messaging.policy
version: v1alpha1
intent: >
  When anyone from the board emails me, let Sky10 read those emails, draft a
  reply, and ask me before sending. Do not send attachments.

policy:
  id: policy/board-replies
  name: Board Reply Approval
  rules:
    read_inbound: true
    create_drafts: true
    send_messages: true
    require_approval: true
    reply_only: true
    allow_new_conversations: false
    allow_attachments: false
    search_identities: true
    search_conversations: true
    search_messages: true
    allowed_identity_ids:
      - identity/work-email

bindings:
  - connection_id: gmail/work

generated_by:
  type: ai
  name: sky10-policy-assistant
  model: gpt-5.4
  prompt_ref: prompt/board-replies

review:
  required: true
```

`pkg/messaging/policy` can parse this document and compile `policy.rules` into
the existing broker-enforced `messaging.Policy` model.

## AI Authoring Rule

The AI should update the policy document, not broker state directly.

For AI-generated files:

- `generated_by.type` must be `ai`
- `review.required` must be `true`
- the file must preserve `intent`
- the generated rules must be explainable from the intent
- applying the policy must require a separate human approval step

This gives the product a safe conversational flow:

1. The user says what they want in ordinary language.
2. The AI proposes a policy file diff.
3. Sky10 validates the document and shows the effective permissions.
4. The user approves or edits the file.
5. Sky10 applies the compiled policy to connections or exposures.

## Current Gaps

- The document compiler exists, but applying `bindings` to the store is still a
  follow-up.
- Time-window and connection-scope rules are still roadmap items.
- The UI still needs a policy editor/review surface that shows both intent and
  compiled permissions.
