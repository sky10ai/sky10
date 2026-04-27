// Package discovery ingests x402 service catalogs into pkg/x402's
// Registry. Catalogs come from one or more Sources: a built-in
// StaticSource for the curated primitive set, and (later) an
// AgenticMarketSource that polls https://agentic.market.
//
// Refresh is the orchestrator. It pulls every Source, classifies each
// observation against the registry's current state via Classify, then
// applies safe changes immediately and returns risky ones for the
// daemon to surface for re-approval. Removed services are marked but
// retained so receipts and pin enforcement keep working until the user
// explicitly revokes.
//
// An Overlay merges sky10-curated metadata (tier, default-on flag,
// routing hint) over each service's upstream manifest. Overlay data
// ships in overlay.json embedded into the binary.
//
// See docs/work/current/x402/auto-update.md for the diff classification
// rules and refresh-cadence design.
package discovery
