# Venice x402 Proxy

Use the local `sky10` daemon as an OpenAI-compatible Venice proxy backed by an
OWS wallet on Base.

This is the clean path when you want Hermes or OpenClaw to use Venice over
x402 without teaching either client how to:

- sign `X-Sign-In-With-X`
- top up Venice via Base USDC
- retry paid requests after a `402`

## What It Does

When the daemon is configured, it exposes:

```text
http://127.0.0.1:9101/venice/v1
```

Requests under that prefix are forwarded to `https://api.venice.ai/api/v1/...`.

For each upstream request the proxy:

1. signs a fresh `X-Sign-In-With-X` header with OWS
2. sends the request to Venice
3. if Venice returns `402`, performs `POST /api/v1/x402/top-up`
4. retries the original request once

The proxy uses the same OWS wallet for both wallet auth and Base USDC top-up.

## Requirements

- `ows` installed and available to the daemon
- an OWS wallet with a Base-capable EVM account
- Base `USDC` in that wallet
- `OWS_PASSPHRASE` set in the daemon environment if the wallet is encrypted

## Daemon Environment

Set these before starting `sky10 serve`:

```bash
export SKY10_VENICE_WALLET=my-ows-wallet
export SKY10_VENICE_TOP_UP_USD=10
export OWS_PASSPHRASE='your-wallet-passphrase'
```

Optional:

```bash
export SKY10_VENICE_API_URL=https://api.venice.ai
```

Then start the daemon normally:

```bash
sky10 serve
```

If `SKY10_VENICE_WALLET` is unset, the route still exists but returns a
configuration error instead of proxying.

## Hermes

Point Hermes at the local proxy as a custom OpenAI-compatible endpoint:

```yaml
model:
  provider: custom
  base_url: http://127.0.0.1:9101/venice/v1
  api_key: sky10-local
  default: venice-uncensored
```

`api_key` can be any placeholder value. The proxy ignores upstream bearer auth
and signs requests with the OWS wallet instead.

## OpenClaw

Point a custom provider at the same base URL:

```json
{
  "models": {
    "mode": "merge",
    "providers": {
      "venice-x402": {
        "baseUrl": "http://127.0.0.1:9101/venice/v1",
        "api": "openai-completions",
        "models": [
          {
            "id": "venice-uncensored",
            "name": "Venice Uncensored",
            "input": ["text", "image"]
          }
        ]
      }
    }
  }
}
```

The gateway can still send a placeholder bearer token if it wants. The proxy
strips it before forwarding.

## Supported Routes

The proxy is generic for Venice `/api/v1/...` routes, not just chat. That means
Hermes/OpenClaw can use:

- `/models`
- `/chat/completions`
- `/embeddings`
- other Venice `/api/v1/*` routes that only need wallet auth plus balance

There is also a local helper for manual crediting:

```bash
curl -s http://127.0.0.1:9101/venice/v1/x402/top-up \
  -H 'Content-Type: application/json' \
  -d '{"amountUsd":"10"}'
```

## Notes

- This path assumes the daemon and the OWS wallet live on the same machine.
- For Lima/OpenClaw guest setups, the simplest approach is usually to keep the
  wallet and proxy on the host, then point the guest model config at the host
  daemon URL that is reachable from the VM.
- The proxy retries once after a successful top-up. It is not trying to hide
  repeated upstream billing failures forever.
