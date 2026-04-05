---
name: debug-remote
description: Diagnose a remote sky10 machine through the local daemon socket using fresh debug dumps
allowed-tools: Read, Bash, Grep
---

# Debug a remote sky10 machine

Use this workflow when investigating another machine via the local
daemon socket at `/tmp/sky10/sky10.sock`.

If the user already has a dump key, skip straight to fetching it.
Otherwise collect a fresh dump first.

## Rules

- Prefer a fresh dump over historical dumps.
- Identify the correct device before reading or deleting dumps.
- Use `skyfs.s3Delete` to remove stale debug dumps when they will cause
  confusion.
- `skyfs.debug*` and `skyfs.s3*` require S3-backed storage on the target
  daemon. If the target is running P2P-only, this workflow does not
  apply.
- Use daemon RPC or Go code. There is no separate S3 CLI configured for
  this workflow.

## Socket setup

All commands below use JSON-RPC over the Unix socket:

```bash
SOCK=/tmp/sky10/sky10.sock
```

Pretty-print responses with `python3 -m json.tool`.

## 1. Identify the device

```bash
echo '{"jsonrpc":"2.0","method":"skyfs.deviceList","id":1}' | nc -U "$SOCK" | python3 -m json.tool
```

Match the hostname or `device_id` from the user's report to the machine
you care about.

## 2. List existing dumps

```bash
echo '{"jsonrpc":"2.0","method":"skyfs.debugList","id":1}' | nc -U "$SOCK" | python3 -m json.tool
```

Relevant keys look like `debug/<deviceID>/<timestamp>.json`.

## 3. Clear stale dumps for the target device when needed

Delete old dumps if they will make the investigation ambiguous:

```bash
echo '{"jsonrpc":"2.0","method":"skyfs.s3Delete","params":{"key":"debug/<deviceID>/<timestamp>.json"},"id":1}' | nc -U "$SOCK" | python3 -m json.tool
```

Repeat for each stale key you want gone.

## 4. Trigger a fresh dump from the target machine

Run this on the target machine, not the one doing the inspection:

```bash
echo '{"jsonrpc":"2.0","method":"skyfs.debugDump","id":1}' | nc -U "$SOCK" | python3 -m json.tool
```

This uploads a new dump under `debug/<deviceID>/<timestamp>.json`.

## 5. Fetch the dump

```bash
echo '{"jsonrpc":"2.0","method":"skyfs.debugGet","params":{"key":"debug/<deviceID>/<timestamp>.json"},"id":1}' | nc -U "$SOCK" | python3 -m json.tool
```

## 6. What to inspect

- `drives[].snapshot_files` and `snapshot_file_count`
- `drives[].outbox` and `outbox_count`
- `drives[].local_files` and `local_file_count`
- `remote_ops_recent` and `remote_ops_error`
- `devices` and `devices_error`
- `namespace_keys` and `namespace_keys_error`
- `logs_raw` or `logs`

## 7. Optional direct S3 inspection

Browse prefixes directly when the dump is not enough:

```bash
echo '{"jsonrpc":"2.0","method":"skyfs.s3List","params":{"prefix":"debug/"},"id":1}' | nc -U "$SOCK" | python3 -m json.tool
echo '{"jsonrpc":"2.0","method":"skyfs.s3List","params":{"prefix":"ops/"},"id":1}' | nc -U "$SOCK" | python3 -m json.tool
echo '{"jsonrpc":"2.0","method":"skyfs.s3List","params":{"prefix":"keys/namespaces/"},"id":1}' | nc -U "$SOCK" | python3 -m json.tool
```

## Closeout

Summarize:

- which device and dump key you inspected
- whether the dump was fresh
- the concrete symptoms in snapshot, outbox, logs, or S3 state
- the next action to take on the target machine
