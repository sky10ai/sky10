# RPC naming

RPC method names are dot-delimited and start with the owning product
domain.

Use a flat `domain.verb` shape when a domain only has a small set of
top-level operations:

- `sandbox.list`
- `sandbox.get`
- `sandbox.start`

Use `domain.subdomain.verb` when a domain has a related group of
operations. Keep the subdomain in the same position across the group:

- `agent.mailbox.send`
- `agent.mailbox.claim`
- `sandbox.runtime.status`
- `sandbox.runtime.upgrade`
- `system.update.status`
- `system.update.install`

Prefer clear nouns for domains and subdomains. Avoid mixed pairs such as
`runtimeStatus` and `upgradeRuntime`; the stable shape is
`sandbox.runtime.status` and `sandbox.runtime.upgrade`.

Existing shipped namespaces and aliases are compatibility surface. For
example, `skyfs.*` is still valid even if a future API would prefer a
shorter `fs.*` namespace. Do not rename or remove shipped methods unless
the change includes an explicit compatibility plan.
