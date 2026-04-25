# RPC TODOs

## Remove `skyfs.health`

`system.health` is now the canonical daemon health RPC method.
`skyfs.health` remains as a compatibility alias for older callers.

Before removing `skyfs.health`:

- migrate in-repo callers and generated docs to `system.health`
- ship at least one compatibility release with both method names
- audit tray/menu binaries and older automation that may still call
  `skyfs.health`
- remove the alias only with an explicit compatibility note in release
  notes
