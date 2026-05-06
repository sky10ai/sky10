#!/usr/bin/env python3
"""Bridge sky10 agent messages to a Hermes API server running in the guest."""

from __future__ import annotations

import json
import base64
import hashlib
import mimetypes
import os
import re
import shlex
import signal
import socket
import struct
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections import OrderedDict
from typing import Any, Callable


HEARTBEAT_INTERVAL_SECONDS = 25
RECONNECT_DELAY_SECONDS = 5
SEEN_TTL_SECONDS = 30
DEFAULT_AGENT_SKILLS = ["code", "shell", "web-search", "file-ops"]
DEFAULT_MANIFEST_PATH = "/shared/agent-manifest.json"
WARMUP_PROMPT = "Reply with exactly OK."
MEDIA_ROOT = os.path.join(tempfile.gettempdir(), "sky10-hermes-media")
DEFAULT_JOB_OUTPUT_ROOT = "/shared/jobs"
DEFAULT_X402_ENDPOINT_PATH = "/bridge/metered-services/ws"
DEFAULT_X402_HELPER_PATH = os.path.join(os.path.expanduser("~"), ".local", "bin", "sky10-x402")
X402_CONTEXT_SERVICE_LIMIT = 12


class BridgeError(RuntimeError):
    """Raised when the bridge cannot complete an RPC or API request."""


def log(message: str) -> None:
    timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    print(f"[{timestamp}] {message}", flush=True)


def strip_trailing_path(url: str, suffix: str) -> str:
    url = url.rstrip("/")
    if url.endswith(suffix):
        return url[: -len(suffix)]
    return url


def env_flag(name: str, default: bool) -> bool:
    raw = os.environ.get(name, "").strip().lower()
    if raw == "":
        return default
    return raw in {"1", "true", "yes", "on"}


def derive_x402_ws_url(rpc_url: str, agent_name: str = "", ws_url: str = "") -> str:
    raw = str(ws_url or "").strip()
    base = raw or str(rpc_url or "http://127.0.0.1:9101").strip() or "http://127.0.0.1:9101"
    parsed = urllib.parse.urlparse(base)
    scheme = parsed.scheme
    if scheme == "http":
        scheme = "ws"
    elif scheme == "https":
        scheme = "wss"
    elif scheme not in {"ws", "wss"}:
        scheme = "ws"
        parsed = urllib.parse.urlparse("http://" + base)
    netloc = parsed.netloc
    path = parsed.path or ""
    if not raw or path in {"", "/", "/rpc"}:
        path = DEFAULT_X402_ENDPOINT_PATH
    query = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    if agent_name and not any(key == "agent" for key, _ in query):
        query.append(("agent", agent_name))
    return urllib.parse.urlunparse((scheme, netloc, path, "", urllib.parse.urlencode(query), ""))


def websocket_frame(payload: bytes) -> bytes:
    mask = os.urandom(4)
    length = len(payload)
    if length < 126:
        header = bytes([0x81, 0x80 | length])
    elif length <= 0xFFFF:
        header = bytes([0x81, 0x80 | 126]) + struct.pack("!H", length)
    else:
        header = bytes([0x81, 0x80 | 127]) + struct.pack("!Q", length)
    masked = bytes(byte ^ mask[i % 4] for i, byte in enumerate(payload))
    return header + mask + masked


def read_exact(sock: socket.socket, size: int) -> bytes:
    chunks: list[bytes] = []
    remaining = size
    while remaining > 0:
        chunk = sock.recv(remaining)
        if not chunk:
            raise BridgeError("x402 websocket closed unexpectedly")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def read_websocket_text(sock: socket.socket) -> str:
    for _ in range(16):
        first, second = read_exact(sock, 2)
        opcode = first & 0x0F
        masked = second & 0x80
        length = second & 0x7F
        if length == 126:
            length = struct.unpack("!H", read_exact(sock, 2))[0]
        elif length == 127:
            length = struct.unpack("!Q", read_exact(sock, 8))[0]
        mask = read_exact(sock, 4) if masked else b""
        payload = read_exact(sock, length) if length else b""
        if masked:
            payload = bytes(byte ^ mask[i % 4] for i, byte in enumerate(payload))
        if opcode == 0x1:
            return payload.decode("utf-8")
        if opcode == 0x8:
            raise BridgeError("x402 websocket closed before response")
        if opcode == 0x9:
            # Ping; this short-lived helper waits for the actual response.
            continue
    raise BridgeError("x402 websocket did not return a text response")


def x402_ws_request(ws_url: str, envelope: dict[str, Any], timeout: float = 30.0) -> dict[str, Any]:
    parsed = urllib.parse.urlparse(ws_url)
    if parsed.scheme not in {"ws", "wss"}:
        raise BridgeError(f"x402 websocket URL must use ws or wss, got {parsed.scheme!r}")
    if parsed.scheme == "wss":
        raise BridgeError("x402 helper does not support wss from the sandbox; use guest-local ws")
    host = parsed.hostname or "127.0.0.1"
    port = parsed.port or 80
    path = parsed.path or DEFAULT_X402_ENDPOINT_PATH
    if parsed.query:
        path += "?" + parsed.query
    key = base64.b64encode(os.urandom(16)).decode("ascii")
    request = "\r\n".join(
        [
            f"GET {path} HTTP/1.1",
            f"Host: {host}:{port}",
            "Upgrade: websocket",
            "Connection: Upgrade",
            f"Sec-WebSocket-Key: {key}",
            "Sec-WebSocket-Version: 13",
            "",
            "",
        ]
    ).encode("ascii")
    with socket.create_connection((host, port), timeout=timeout) as sock:
        sock.settimeout(timeout)
        sock.sendall(request)
        response = b""
        while b"\r\n\r\n" not in response:
            chunk = sock.recv(4096)
            if not chunk:
                break
            response += chunk
            if len(response) > 65536:
                raise BridgeError("x402 websocket handshake response too large")
        status = response.split(b"\r\n", 1)[0].decode("ascii", errors="replace")
        if " 101 " not in status:
            raise BridgeError(f"x402 websocket upgrade failed: {status}")
        sock.sendall(websocket_frame(json.dumps(envelope, separators=(",", ":")).encode("utf-8")))
        body = read_websocket_text(sock)
    return json.loads(body)


def x402_request(ws_url: str, typ: str, payload: dict[str, Any] | None = None) -> Any:
    request_id = str(uuid.uuid4())
    envelope = {
        "type": typ,
        "request_id": request_id,
        "nonce": str(uuid.uuid4()),
        "payload": payload or {},
    }
    response = x402_ws_request(ws_url, envelope)
    if response.get("request_id") != request_id:
        raise BridgeError("x402 websocket returned a mismatched request_id")
    if response.get("error"):
        err = response["error"]
        raise BridgeError(f"x402 {err.get('code', 'error')}: {err.get('message', '')}".strip())
    return response.get("payload")


def x402_list_services(ws_url: str) -> list[dict[str, Any]]:
    payload = x402_request(ws_url, "x402.list_services", {})
    services = payload.get("services") if isinstance(payload, dict) else []
    return services if isinstance(services, list) else []


def install_x402_helper(helper_path: str, script_path: str, ws_url: str) -> str:
    path = str(helper_path or DEFAULT_X402_HELPER_PATH).strip() or DEFAULT_X402_HELPER_PATH
    os.makedirs(os.path.dirname(path), exist_ok=True)
    body = "\n".join(
        [
            "#!/bin/sh",
            "set -e",
            f"export SKY10_X402_WS_URL={shlex.quote(ws_url)}",
            f"exec /usr/bin/env python3 {shlex.quote(script_path)} --x402 \"$@\"",
            "",
        ]
    )
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(body)
    os.chmod(path, 0o755)
    return path


def read_agent_manifest(path: str) -> dict[str, Any]:
    manifest_path = (path or DEFAULT_MANIFEST_PATH).strip() or DEFAULT_MANIFEST_PATH
    try:
        with open(manifest_path, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def normalize_tools(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    tools: list[dict[str, Any]] = []
    seen: set[str] = set()
    for raw in value:
        if not isinstance(raw, dict):
            continue
        tool = dict(raw)
        name = str(tool.get("name") or "").strip()
        capability = str(tool.get("capability") or "").strip()
        if not name:
            continue
        key = capability or name
        if key in seen:
            continue
        seen.add(key)
        tool["name"] = name
        if capability:
            tool["capability"] = capability
        tools.append(tool)
    return tools


def skills_from_tools(tools: list[dict[str, Any]]) -> list[str]:
    skills: list[str] = []
    seen: set[str] = set()
    for tool in tools:
        for raw in (tool.get("capability"), tool.get("name")):
            skill = str(raw or "").strip()
            if not skill or skill in seen:
                continue
            seen.add(skill)
            skills.append(skill)
    return skills


def extract_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, dict):
        text = content.get("text")
        if isinstance(text, str):
            return text
    return json.dumps(content, ensure_ascii=True)


def sanitize_filename(name: Any, fallback: str = "attachment.bin") -> str:
    candidate = str(name or "").strip() or fallback
    sanitized = re.sub(r"[^A-Za-z0-9._-]+", "-", candidate).strip("-")
    return sanitized or fallback


def guess_mime_type(value: Any) -> str:
    guessed, _encoding = mimetypes.guess_type(str(value or ""))
    return guessed or ""


def part_kind(part: dict[str, Any]) -> str:
    kind = str(part.get("type") or "").strip()
    if kind in {"image", "audio", "video"}:
        return kind
    source = part.get("source") if isinstance(part.get("source"), dict) else {}
    media_type = str(part.get("media_type") or source.get("media_type") or guess_mime_type(part.get("filename") or source.get("filename") or source.get("url"))).strip()
    if media_type.startswith("image/"):
        return "image"
    if media_type.startswith("audio/"):
        return "audio"
    if media_type.startswith("video/"):
        return "video"
    return "file"


def normalize_content_parts(content: Any) -> list[dict[str, Any]]:
    if isinstance(content, str):
        return [{"type": "text", "text": content}]
    if not isinstance(content, dict):
        if content is None:
            return []
        return [{"type": "text", "text": json.dumps(content, ensure_ascii=True)}]

    parts = content.get("parts")
    if isinstance(parts, list) and parts:
        normalized: list[dict[str, Any]] = []
        for part in parts:
            if not isinstance(part, dict):
                continue
            source = part.get("source") if isinstance(part.get("source"), dict) else {}
            normalized.append(
                {
                    "type": str(part.get("type") or ("text" if isinstance(part.get("text"), str) else "file")).strip(),
                    "text": part.get("text") if isinstance(part.get("text"), str) else "",
                    "filename": part.get("filename") if isinstance(part.get("filename"), str) else "",
                    "media_type": part.get("media_type") if isinstance(part.get("media_type"), str) else "",
                    "caption": part.get("caption") if isinstance(part.get("caption"), str) else "",
                    "source": {
                        "type": source.get("type") if isinstance(source.get("type"), str) else "",
                        "data": source.get("data") if isinstance(source.get("data"), str) else "",
                        "url": source.get("url") if isinstance(source.get("url"), str) else "",
                        "filename": source.get("filename") if isinstance(source.get("filename"), str) else "",
                        "media_type": source.get("media_type") if isinstance(source.get("media_type"), str) else "",
                    } if source else None,
                }
            )
        return normalized

    text = content.get("text")
    if isinstance(text, str) and text:
        return [{"type": "text", "text": text}]
    return [{"type": "text", "text": json.dumps(content, ensure_ascii=True)}]


def ensure_media_session_dir(session_id: str) -> str:
    directory = os.path.join(MEDIA_ROOT, urllib.parse.quote(session_id or "main", safe=""))
    os.makedirs(directory, exist_ok=True)
    return directory


def stage_base64_part(session_id: str, part: dict[str, Any]) -> str:
    source = part.get("source") if isinstance(part.get("source"), dict) else {}
    filename = sanitize_filename(part.get("filename") or source.get("filename"), f"{part_kind(part)}.bin")
    stamp = f"{int(time.time() * 1000)}-{uuid.uuid4().hex[:8]}"
    path = os.path.join(ensure_media_session_dir(session_id), f"{stamp}-{filename}")
    data = str(source.get("data") or "").strip()
    with open(path, "wb") as handle:
        handle.write(base64.b64decode(data))
    return path


def build_attachment_prompt(part: dict[str, Any], location: str) -> str:
    kind = part_kind(part)
    lines = [f"[Attached {kind}]"]
    source = part.get("source") if isinstance(part.get("source"), dict) else {}
    filename = str(part.get("filename") or source.get("filename") or "").strip()
    media_type = str(part.get("media_type") or source.get("media_type") or guess_mime_type(filename or source.get("url") or location)).strip()
    if filename:
        lines.append(f"filename: {filename}")
    if media_type:
        lines.append(f"mime: {media_type}")
    if location:
        if location.startswith("http://") or location.startswith("https://"):
            lines.append(f"url: {location}")
        else:
            lines.append(f"path: {location}")
    caption = part.get("caption")
    if isinstance(caption, str) and caption.strip():
        lines.append(f"caption: {caption.strip()}")
    return "\n".join(lines)


def build_inbound_body(content: Any, session_id: str) -> str:
    chunks: list[str] = []
    for part in normalize_content_parts(content):
        if part.get("type") == "text":
            text = part.get("text")
            if isinstance(text, str) and text:
                chunks.append(text)
            continue
        source = part.get("source") if isinstance(part.get("source"), dict) else {}
        source_type = str(source.get("type") or "").strip()
        if source_type == "base64" and str(source.get("data") or "").strip():
            chunks.append(build_attachment_prompt(part, stage_base64_part(session_id, part)))
            continue
        if source_type == "url" and str(source.get("url") or "").strip():
            chunks.append(build_attachment_prompt(part, str(source.get("url")).strip()))
            continue
        chunks.append(build_attachment_prompt(part, ""))
    return "\n\n".join(chunk for chunk in chunks if chunk)


def content_has_image_parts(content: Any) -> bool:
    for part in normalize_content_parts(content):
        if part_kind(part) != "image":
            continue
        source = part.get("source") if isinstance(part.get("source"), dict) else {}
        source_type = str(source.get("type") or "").strip()
        if source_type == "base64" and str(source.get("data") or "").strip():
            return True
        if source_type == "url" and str(source.get("url") or "").strip():
            return True
    return False


def image_url_for_part(part: dict[str, Any]) -> str:
    source = part.get("source") if isinstance(part.get("source"), dict) else {}
    source_type = str(source.get("type") or "").strip()
    if source_type == "base64":
        data = str(source.get("data") or "").strip()
        if not data:
            return ""
        media_type = str(part.get("media_type") or source.get("media_type") or guess_mime_type(part.get("filename") or source.get("filename"))).strip() or "application/octet-stream"
        return f"data:{media_type};base64,{data}"
    if source_type == "url":
        return str(source.get("url") or "").strip()
    return ""


def build_chat_completions_user_content(content: Any, session_id: str) -> list[dict[str, Any]] | str:
    items: list[dict[str, Any]] = []
    text_chunks: list[str] = []

    def push_text(value: str) -> None:
        if value.strip():
            text_chunks.append(value.strip())

    def flush_text() -> None:
        if not text_chunks:
            return
        items.append({"type": "text", "text": "\n\n".join(text_chunks)})
        text_chunks.clear()

    for part in normalize_content_parts(content):
        if part.get("type") == "text":
            text = part.get("text")
            if isinstance(text, str) and text:
                push_text(text)
            continue

        source = part.get("source") if isinstance(part.get("source"), dict) else {}
        source_type = str(source.get("type") or "").strip()
        if part_kind(part) == "image":
            fallback_location = ""
            if source_type == "base64" and str(source.get("data") or "").strip():
                fallback_location = stage_base64_part(session_id, part)
            elif source_type == "url" and str(source.get("url") or "").strip():
                fallback_location = str(source.get("url")).strip()
            if fallback_location:
                push_text(build_attachment_prompt(part, fallback_location))
            image_url = image_url_for_part(part)
            if image_url:
                flush_text()
                items.append({"type": "image_url", "image_url": {"url": image_url}})
                caption = part.get("caption")
                if isinstance(caption, str) and caption.strip():
                    push_text(caption)
                continue
            if fallback_location:
                continue

        if source_type == "base64" and str(source.get("data") or "").strip():
            push_text(build_attachment_prompt(part, stage_base64_part(session_id, part)))
            continue
        if source_type == "url" and str(source.get("url") or "").strip():
            push_text(build_attachment_prompt(part, str(source.get("url")).strip()))
            continue
        push_text(build_attachment_prompt(part, ""))

    flush_text()
    if not items:
        fallback = build_inbound_body(content, session_id)
        return fallback or extract_text(content)
    return items


def media_part_from_url(ref: str) -> dict[str, Any]:
    parsed = urllib.parse.urlparse(ref)
    filename = sanitize_filename(os.path.basename(parsed.path), "attachment.bin")
    media_type = guess_mime_type(filename or ref)
    kind = part_kind({"type": "", "filename": filename, "media_type": media_type, "source": {"url": ref, "media_type": media_type}})
    return {
        "type": kind,
        "filename": filename,
        "media_type": media_type,
        "source": {
            "type": "url",
            "url": ref,
            "filename": filename,
            "media_type": media_type,
        },
    }


def media_part_from_file(ref: str) -> dict[str, Any] | None:
    if not os.path.isfile(ref):
        return None
    filename = sanitize_filename(os.path.basename(ref), "attachment.bin")
    media_type = guess_mime_type(ref)
    kind = part_kind({"type": "", "filename": filename, "media_type": media_type, "source": {"filename": filename, "media_type": media_type}})
    with open(ref, "rb") as handle:
        data = base64.b64encode(handle.read()).decode("ascii")
    return {
        "type": kind,
        "filename": filename,
        "media_type": media_type,
        "source": {
            "type": "base64",
            "data": data,
            "filename": filename,
            "media_type": media_type,
        },
    }


def build_outbound_content(payload: Any) -> dict[str, Any] | None:
    source_text = payload if isinstance(payload, str) else str(payload or "")
    body_lines: list[str] = []
    media_parts: list[dict[str, Any]] = []
    seen_media_refs: set[str] = set()

    def add_media_ref(ref: Any) -> bool:
        normalized = str(ref or "").strip()
        if not normalized or normalized in seen_media_refs:
            return bool(normalized)
        media_part = media_part_from_url(normalized) if normalized.startswith(("http://", "https://")) else media_part_from_file(normalized)
        if not media_part:
            return False
        seen_media_refs.add(normalized)
        media_parts.append(media_part)
        return True

    for line in source_text.splitlines():
        trimmed = line.strip()
        if trimmed.startswith("MEDIA:"):
            ref = trimmed[len("MEDIA:"):].strip()
            if ref and add_media_ref(ref):
                continue
        elif trimmed and (trimmed.startswith(("http://", "https://")) or os.path.isabs(trimmed) or trimmed.startswith("./") or trimmed.startswith("../")):
            if add_media_ref(trimmed):
                continue
        body_lines.append(line)

    body_text = "\n".join(body_lines).strip()
    if not media_parts:
        if not body_text:
            return None
        return {"text": body_text}

    parts: list[dict[str, Any]] = []
    if body_text:
        parts.append({"type": "text", "text": body_text})
    parts.extend(media_parts)
    return {
        "text": body_text or None,
        "parts": parts,
    }


def extract_client_request_id(content: Any) -> str:
    if not isinstance(content, dict):
        return ""
    client_request_id = content.get("client_request_id")
    if not isinstance(client_request_id, str):
        return ""
    return client_request_id.strip()


def resolve_job_output_dir(job_id: str, content: dict[str, Any]) -> str:
    job_context = content.get("job_context") if isinstance(content.get("job_context"), dict) else {}
    configured = str(job_context.get("output_dir") or "").strip()
    if configured:
        return configured
    root = os.environ.get("SKY10_JOB_OUTPUT_ROOT", DEFAULT_JOB_OUTPUT_ROOT).strip() or DEFAULT_JOB_OUTPUT_ROOT
    return os.path.join(root, job_id, "outputs")


def compact_text(value: Any, limit: int = 180) -> str:
    text = re.sub(r"\s+", " ", str(value or "")).strip()
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 1)].rstrip() + "..."


def x402_service_line(service: dict[str, Any]) -> str:
    parts = [str(service.get("id") or service.get("display_name") or "x402-service").strip()]
    display = str(service.get("display_name") or "").strip()
    if display and display != parts[0]:
        parts.append(f"({display})")
    price = str(service.get("price_usdc") or "").strip()
    if price:
        parts.append(f"cost ~{price} USDC")
    tier = str(service.get("tier") or "").strip()
    if tier:
        parts.append(f"tier {tier}")
    hint = compact_text(service.get("hint") or service.get("description"), 220)
    if hint:
        parts.append(f"- {hint}")
    return " ".join(part for part in parts if part)


def x402_endpoint_lines(service: dict[str, Any], limit: int = 3) -> list[str]:
    endpoints = service.get("endpoints")
    if not isinstance(endpoints, list):
        return []
    lines: list[str] = []
    for endpoint in endpoints[:limit]:
        if not isinstance(endpoint, dict):
            continue
        method = str(endpoint.get("method") or "GET").upper()
        url = str(endpoint.get("url") or "").strip()
        price = str(endpoint.get("price_usdc") or "").strip()
        description = compact_text(endpoint.get("description"), 120)
        parts = [f"  endpoints: {method} {url}".strip()]
        if price:
            parts.append(f"~{price} USDC")
        if description:
            parts.append(description)
        lines.append(" - ".join(parts))
    if len(endpoints) > limit:
        lines.append(f"  endpoints: ...{len(endpoints) - limit} more; run the list command for the full catalog.")
    return lines


def format_x402_prompt_context(services: list[dict[str, Any]], helper_path: str) -> str:
    if not services:
        return ""
    shown = services[:X402_CONTEXT_SERVICE_LIMIT]
    lines = [
        "Settings-approved x402 APIs are available.",
        "Routing rule: use browser or web-search for casual browsing and unstructured reading. Use x402 only when an approved service's hint, description, or endpoint list advertises structured/API-grade data that directly matches the task.",
        "Prefer the listed service's x402 API over browser/search when you need the exact records described below; otherwise use free local tools first.",
        "The sky10 helper handles x402 payment, receipts, and wallet signing; do not manage wallets, payment headers, or x402 challenges yourself.",
        f"List services: {helper_path} list",
        f"Check budget: {helper_path} budget",
        f"Call a service: {helper_path} call '{{\"service_id\":\"SERVICE_ID\",\"path\":\"/PATH\",\"method\":\"GET\",\"max_price_usdc\":\"0.01\"}}'",
        "For calls, use service_id from the list and pass a relative path plus any query string; do not pass a full URL.",
        "Approved services:",
    ]
    for service in shown:
        if not isinstance(service, dict):
            continue
        lines.append(f"- {x402_service_line(service)}")
        lines.extend(x402_endpoint_lines(service))
    if len(services) > len(shown):
        lines.append(f"- ...{len(services) - len(shown)} more; run the list command for the full catalog.")
    return "\n".join(lines)


def ensure_x402_tool(tools: list[dict[str, Any]], helper_path: str) -> list[dict[str, Any]]:
    for tool in tools:
        if str(tool.get("name") or "") == "sky10.x402":
            return tools
    merged = list(tools)
    merged.append(
        {
            "name": "sky10.x402",
            "capability": "x402",
            "description": "Call Settings-approved paid x402 APIs through the guest-local sky10 bridge. Use browser/search first unless the service description explicitly advertises the needed structured/API-grade data.",
            "audience": "agent",
            "scope": "sandbox",
            "input_schema": {
                "type": "object",
                "properties": {
                    "command": {"type": "string", "enum": ["list", "budget", "call"]},
                    "params": {"type": "object"},
                },
            },
            "fulfillment": {
                "mode": "shell",
                "note": f"Use {helper_path} list, {helper_path} budget, or {helper_path} call '<json>'.",
            },
            "pricing": {"model": "x402", "currency": "USDC"},
            "meta": {"bridge_endpoint": DEFAULT_X402_ENDPOINT_PATH},
        }
    )
    return merged


def file_digest(path: str) -> str:
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return "sha256:" + digest.hexdigest()


def collect_output_refs(output_dir: str) -> list[dict[str, Any]]:
    if not os.path.isdir(output_dir):
        return []
    refs: list[dict[str, Any]] = []
    for root, _dirs, files in os.walk(output_dir):
        for filename in sorted(files):
            path = os.path.join(root, filename)
            if not os.path.isfile(path):
                continue
            rel = os.path.relpath(path, output_dir)
            media_type = guess_mime_type(path)
            refs.append(
                {
                    "kind": "file",
                    "key": rel,
                    "uri": "file://" + urllib.request.pathname2url(os.path.abspath(path)),
                    "mime_type": media_type or "application/octet-stream",
                    "size": os.path.getsize(path),
                    "digest": file_digest(path),
                }
            )
    refs.sort(key=lambda ref: str(ref.get("key") or ""))
    return refs


def build_agent_contract_prompt(manifest: dict[str, Any] | None) -> str:
    spec = manifest if isinstance(manifest, dict) else {}
    lines: list[str] = []
    prompt = str(spec.get("prompt") or "").strip()
    if prompt:
        lines.extend(["Original user prompt:", prompt, ""])
    description = str(spec.get("description") or "").strip()
    if description:
        lines.append(f"Agent purpose: {description}")
    tools = spec.get("tools")
    if isinstance(tools, list) and tools:
        lines.append("Exported tools:")
        for raw in tools:
            if not isinstance(raw, dict):
                continue
            name = str(raw.get("name") or raw.get("capability") or "").strip()
            capability = str(raw.get("capability") or "").strip()
            description = str(raw.get("description") or "").strip()
            if name or capability or description:
                suffix = f" ({capability})" if capability and capability != name else ""
                lines.append(f"- {name or capability}{suffix}: {description}")
    inputs = spec.get("inputs")
    if isinstance(inputs, list) and inputs:
        lines.append("Expected inputs:")
        for raw in inputs:
            if isinstance(raw, dict):
                lines.append(f"- {raw.get('kind') or 'input'}: {raw.get('description') or ''}")
    outputs = spec.get("outputs")
    if isinstance(outputs, list) and outputs:
        lines.append("Expected outputs:")
        for raw in outputs:
            if isinstance(raw, dict):
                lines.append(f"- {raw.get('kind') or 'artifact'}: {raw.get('description') or ''}")
    bindings = spec.get("secret_bindings")
    if isinstance(bindings, list) and bindings:
        lines.append("Available secret bindings:")
        for raw in bindings:
            if isinstance(raw, dict):
                required = "required" if raw.get("required") else "optional"
                lines.append(f"- {raw.get('env') or ''} ({required})")
    return "\n".join(line for line in lines if line is not None).strip()


def build_tool_call_prompt(
    content: dict[str, Any],
    output_dir: str,
    manifest: dict[str, Any] | None = None,
    x402_context: str = "",
) -> str:
    tool = str(content.get("tool") or "").strip() or "tool"
    capability = str(content.get("capability") or "").strip()
    job_context = content.get("job_context") if isinstance(content.get("job_context"), dict) else {}
    job_id = str(content.get("job_id") or job_context.get("job_id") or "").strip()
    payload_refs: list[Any] = []
    payload_ref = content.get("payload_ref")
    if isinstance(payload_ref, dict):
        payload_refs.append(payload_ref)
    raw_payload_refs = content.get("payload_refs")
    if isinstance(raw_payload_refs, list):
        payload_refs.extend(raw_payload_refs)
    payload = {
        "job_id": job_id,
        "tool": tool,
        "capability": capability,
        "input": content.get("input") if "input" in content else {},
        "payload_refs": payload_refs,
        "output_dir": output_dir,
        "job_context": job_context,
        "budget": content.get("budget"),
        "bid_id": content.get("bid_id"),
    }
    contract_prompt = build_agent_contract_prompt(manifest)
    lines = [
        "You are this sky10 agent. Follow the agent contract below." if contract_prompt else "You are a sky10 durable agent.",
    ]
    if contract_prompt:
        lines.extend([contract_prompt, ""])
    lines.extend(
        [
            "You are fulfilling a sky10 durable agent tool call.",
            "Complete the requested tool call autonomously using the tools and credentials available in this VM.",
            "Infer the workflow yourself from the original prompt, exported tool contract, input payloads, available files, installed packages, and configured provider secrets.",
            f"Treat payload_refs as input handles and write generated artifacts under this directory: {output_dir}",
            "Use job_context.update_methods when you need to report progress, completion, or failure through sky10.",
            "Respect any budget, pricing, or payment context attached to the tool call.",
            "Return a concise result summary and include any output artifact paths or URIs.",
        ]
    )
    if x402_context:
        lines.extend(["", x402_context])
    lines.extend(["", "Tool call:", json.dumps(payload, ensure_ascii=True, indent=2, sort_keys=True)])
    return "\n".join(lines)


def env_truthy(name: str) -> bool:
    value = os.environ.get(name, "")
    return value.strip().lower() in {"1", "true", "yes", "on"}


def iter_sse(response: Any) -> Any:
    event_name = "message"
    data_lines: list[str] = []
    for raw_line in response:
        line = raw_line.decode("utf-8", errors="replace").rstrip("\n").rstrip("\r")
        if not line:
            if data_lines:
                yield event_name, "\n".join(data_lines)
            event_name = "message"
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        field, _, value = line.partition(":")
        if value.startswith(" "):
            value = value[1:]
        if field == "event":
            event_name = value or "message"
        elif field == "data":
            data_lines.append(value)


class Sky10Client:
    def __init__(self, rpc_url: str) -> None:
        base_url = strip_trailing_path(rpc_url.strip(), "/rpc")
        self.base_url = base_url
        self.rpc_url = f"{base_url}/rpc"
        self.events_url = f"{base_url}/rpc/events"
        self.next_id = 1

    def rpc(self, method: str, params: dict[str, Any] | None = None) -> Any:
        request = urllib.request.Request(
            self.rpc_url,
            data=json.dumps(
                {
                    "jsonrpc": "2.0",
                    "method": method,
                    "params": params or {},
                    "id": self.next_id,
                }
            ).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        self.next_id += 1

        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                payload = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"sky10 RPC {method} failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            raise BridgeError(f"sky10 RPC {method} failed: {exc.reason}") from exc

        if payload.get("error"):
            raise BridgeError(f"sky10 RPC {method} failed: {payload['error'].get('message', 'unknown error')}")
        return payload.get("result")

    def register(self, agent_name: str, agent_key_name: str, skills: list[str], tools: list[dict[str, Any]]) -> str:
        result = self.rpc(
            "agent.register",
            {
                "name": agent_name,
                "key_name": agent_key_name,
                "skills": skills,
                "tools": tools,
            },
        )
        agent_id = (result or {}).get("agent_id", "").strip()
        if not agent_id:
            raise BridgeError("sky10 RPC agent.register returned an empty agent_id")
        return agent_id

    def heartbeat(self, agent_id: str) -> None:
        self.rpc("agent.heartbeat", {"agent_id": agent_id})

    def update_job_status(self, job_id: str, work_state: str, message: str = "", progress: float | None = None) -> None:
        params: dict[str, Any] = {
            "job_id": job_id,
            "work_state": work_state,
        }
        if message:
            params["message"] = message
        if progress is not None:
            params["progress"] = progress
        self.rpc("agent.job.updateStatus", params)

    def complete_job(self, job_id: str, output: Any = None, payload_refs: list[dict[str, Any]] | None = None, message: str = "") -> None:
        params: dict[str, Any] = {
            "job_id": job_id,
        }
        if output is not None:
            params["output"] = output
        if payload_refs:
            params["payload_refs"] = payload_refs
        if message:
            params["message"] = message
        self.rpc("agent.job.complete", params)

    def fail_job(self, job_id: str, code: str, message: str) -> None:
        self.rpc(
            "agent.job.fail",
            {
                "job_id": job_id,
                "code": code,
                "message": message,
            },
        )

    def send_content(self, to: str, session_id: str, content: dict[str, Any], device_id: str, msg_type: str = "text") -> None:
        self.rpc(
            "agent.send",
            {
                "to": to,
                "device_id": device_id,
                "session_id": session_id,
                "type": msg_type,
                "content": content,
            },
        )

    def send(self, to: str, session_id: str, text: str, device_id: str, msg_type: str = "text") -> None:
        self.send_content(to, session_id, {"text": text}, device_id, msg_type)

    def send_delta(self, to: str, session_id: str, text: str, device_id: str, stream_id: str, client_request_id: str = "") -> None:
        if not text:
            return
        content = {
            "text": text,
            "stream_id": stream_id,
        }
        if client_request_id:
            content["client_request_id"] = client_request_id
        self.send_content(
            to,
            session_id,
            content,
            device_id,
            "delta",
        )

    def send_done(self, to: str, session_id: str, device_id: str, stream_id: str, client_request_id: str = "") -> None:
        content = {
            "stream_id": stream_id,
        }
        if client_request_id:
            content["client_request_id"] = client_request_id
        self.send_content(
            to,
            session_id,
            content,
            device_id,
            "done",
        )


class HermesClient:
    def __init__(self) -> None:
        api_base = os.environ.get("HERMES_API_BASE_URL", "http://127.0.0.1:8642/v1").strip()
        self.api_base = strip_trailing_path(api_base, "/")
        self.api_key = os.environ.get("API_SERVER_KEY", "").strip()
        self.model = os.environ.get("API_SERVER_MODEL_NAME", "hermes-agent").strip() or "hermes-agent"
        self.use_responses_api = env_flag("HERMES_PREFER_RESPONSES_API", True)
        self.history_lock = threading.Lock()
        self.chat_history: dict[str, list[dict[str, Any]]] = {}
        self.chat_pending_turns: dict[str, dict[int, tuple[dict[str, Any], dict[str, Any]] | None]] = {}
        self.chat_next_turn_id: dict[str, int] = {}
        self.chat_next_commit_id: dict[str, int] = {}

    def wait_until_ready(self, stop_event: threading.Event) -> None:
        health_url = strip_trailing_path(self.api_base, "/v1") + "/health"
        while not stop_event.is_set():
            request = urllib.request.Request(health_url, method="GET")
            try:
                with urllib.request.urlopen(request, timeout=10) as response:
                    payload = json.loads(response.read().decode("utf-8"))
                if payload.get("status") == "ok":
                    log(f"Hermes API ready at {self.api_base}")
                    return
            except Exception:
                pass
            stop_event.wait(RECONNECT_DELAY_SECONDS)
        raise BridgeError("Hermes API did not become ready before shutdown")

    def stream(self, session_id: str, content: Any, on_delta: Callable[[str], None]) -> str:
        user_message = {
            "role": "user",
            "content": build_chat_completions_user_content(content, session_id),
        }
        if content_has_image_parts(content):
            return self._stream_chat_completions(session_id, user_message, on_delta)
        user_text = build_inbound_body(content, session_id)
        if self.use_responses_api:
            try:
                return self._stream_responses(session_id, user_text, on_delta)
            except BridgeError as exc:
                if self._responses_api_unsupported(str(exc)):
                    log(f"Hermes Responses API unavailable, falling back to chat completions: {exc}")
                    self.use_responses_api = False
                else:
                    raise
            return self._stream_chat_completions(session_id, user_message, on_delta)
        try:
            return self._stream_chat_completions(session_id, user_message, on_delta)
        except BridgeError as exc:
            if self._chat_completions_api_unsupported(str(exc)):
                log(f"Hermes chat completions unavailable, falling back to Responses API: {exc}")
                self.use_responses_api = True
            else:
                raise
        return self._stream_responses(session_id, user_text, on_delta)

    def warm_up(self) -> None:
        start = time.time()
        if self.use_responses_api:
            try:
                self._warm_responses()
                elapsed_ms = int((time.time() - start) * 1000)
                log(f"Hermes API warm-up completed in {elapsed_ms}ms via /responses")
                return
            except BridgeError as exc:
                if self._responses_api_unsupported(str(exc)):
                    log(f"Hermes Responses API warm-up unavailable, falling back to chat completions: {exc}")
                    self.use_responses_api = False
                else:
                    raise
            self._warm_chat_completions()
            elapsed_ms = int((time.time() - start) * 1000)
            log(f"Hermes API warm-up completed in {elapsed_ms}ms via /chat/completions")
            return
        try:
            self._warm_chat_completions()
            elapsed_ms = int((time.time() - start) * 1000)
            log(f"Hermes API warm-up completed in {elapsed_ms}ms via /chat/completions")
            return
        except BridgeError as exc:
            if self._chat_completions_api_unsupported(str(exc)):
                log(f"Hermes chat completions warm-up unavailable, falling back to Responses API: {exc}")
                self.use_responses_api = True
            else:
                raise
        self._warm_responses()
        elapsed_ms = int((time.time() - start) * 1000)
        log(f"Hermes API warm-up completed in {elapsed_ms}ms via /responses")

    def _warm_responses(self) -> None:
        payload = {
            "model": self.model,
            "input": WARMUP_PROMPT,
            "store": False,
            "stream": True,
            "max_output_tokens": 1,
        }
        request = self._stream_request("/responses", payload)
        try:
            with urllib.request.urlopen(request, timeout=600) as response:
                for event_name, data in iter_sse(response):
                    if data.strip() == "[DONE]":
                        return
                    try:
                        event = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    if self._extract_responses_delta(event_name, event):
                        return
                    if event_name in {"response.completed", "response.output_text.done"}:
                        return
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"Hermes API warm-up /responses failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            raise BridgeError(f"Hermes API warm-up /responses failed: {exc.reason}") from exc

    def _warm_chat_completions(self) -> None:
        payload = {
            "model": self.model,
            "messages": [{"role": "user", "content": WARMUP_PROMPT}],
            "stream": True,
            "max_tokens": 1,
        }
        request = self._stream_request("/chat/completions", payload)
        try:
            with urllib.request.urlopen(request, timeout=600) as response:
                for _event_name, data in iter_sse(response):
                    if data.strip() == "[DONE]":
                        return
                    try:
                        event = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    if self._extract_chat_completions_delta(event):
                        return
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"Hermes API warm-up /chat/completions failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            raise BridgeError(f"Hermes API warm-up /chat/completions failed: {exc.reason}") from exc

    def _stream_responses(self, session_id: str, text: str, on_delta: Callable[[str], None]) -> str:
        payload = {
            "model": self.model,
            "input": text,
            "conversation": session_id,
            "store": True,
            "stream": True,
        }
        request = self._stream_request("/responses", payload)
        chunks: list[str] = []
        completed = ""
        try:
            with urllib.request.urlopen(request, timeout=600) as response:
                for event_name, data in iter_sse(response):
                    if data.strip() == "[DONE]":
                        break
                    try:
                        event = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    delta = self._extract_responses_delta(event_name, event)
                    if delta:
                        chunks.append(delta)
                        on_delta(delta)
                    if event_name in {"response.completed", "response.output_text.done"}:
                        completed = self._extract_responses_text(event.get("response") if isinstance(event.get("response"), dict) else event)
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"Hermes API /responses failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            raise BridgeError(f"Hermes API /responses failed: {exc.reason}") from exc
        result = (completed or "".join(chunks)).strip()
        return result or "Done."

    def _stream_chat_completions(self, session_id: str, user_message: dict[str, Any], on_delta: Callable[[str], None]) -> str:
        turn_id, history = self._reserve_chat_turn(session_id)
        history.append(user_message)

        payload = {
            "model": self.model,
            "messages": history,
            "stream": True,
        }
        request = self._stream_request("/chat/completions", payload)
        chunks: list[str] = []
        try:
            with urllib.request.urlopen(request, timeout=600) as response:
                for _event_name, data in iter_sse(response):
                    if data.strip() == "[DONE]":
                        break
                    try:
                        event = json.loads(data)
                    except json.JSONDecodeError:
                        continue
                    delta = self._extract_chat_completions_delta(event)
                    if delta:
                        chunks.append(delta)
                        on_delta(delta)
        except urllib.error.HTTPError as exc:
            self._skip_chat_turn(session_id, turn_id)
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"Hermes API /chat/completions failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            self._skip_chat_turn(session_id, turn_id)
            raise BridgeError(f"Hermes API /chat/completions failed: {exc.reason}") from exc
        except Exception:
            self._skip_chat_turn(session_id, turn_id)
            raise

        answer = "".join(chunks).strip() or "Done."
        self._commit_chat_turn(
            session_id,
            turn_id,
            user_message,
            {"role": "assistant", "content": answer},
        )
        return answer

    def _reserve_chat_turn(self, session_id: str) -> tuple[int, list[dict[str, Any]]]:
        with self.history_lock:
            history = list(self.chat_history.get(session_id, []))
            turn_id = self.chat_next_turn_id.get(session_id, 0)
            self.chat_next_turn_id[session_id] = turn_id + 1
        return turn_id, history

    def _commit_chat_turn(self, session_id: str, turn_id: int, user_message: dict[str, Any], answer_message: dict[str, Any]) -> None:
        self._resolve_chat_turn(session_id, turn_id, (user_message, answer_message))

    def _skip_chat_turn(self, session_id: str, turn_id: int) -> None:
        self._resolve_chat_turn(session_id, turn_id, None)

    def _resolve_chat_turn(self, session_id: str, turn_id: int, turn: tuple[dict[str, Any], dict[str, Any]] | None) -> None:
        with self.history_lock:
            pending = self.chat_pending_turns.setdefault(session_id, {})
            pending[turn_id] = turn
            next_commit_id = self.chat_next_commit_id.get(session_id, 0)
            history = self.chat_history.setdefault(session_id, [])
            while next_commit_id in pending:
                completed_turn = pending.pop(next_commit_id)
                if completed_turn is not None:
                    user_message, answer_message = completed_turn
                    history.append(user_message)
                    history.append(answer_message)
                next_commit_id += 1
            self.chat_next_commit_id[session_id] = next_commit_id
            if not pending:
                self.chat_pending_turns.pop(session_id, None)

    def _request(self, path: str, payload: dict[str, Any]) -> dict[str, Any]:
        url = f"{self.api_base}{path}"
        headers = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"

        request = urllib.request.Request(
            url,
            data=json.dumps(payload).encode("utf-8"),
            headers=headers,
            method="POST",
        )

        try:
            with urllib.request.urlopen(request, timeout=600) as response:
                return json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"Hermes API {path} failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            raise BridgeError(f"Hermes API {path} failed: {exc.reason}") from exc

    def _stream_request(self, path: str, payload: dict[str, Any]) -> urllib.request.Request:
        url = f"{self.api_base}{path}"
        headers = {
            "Accept": "text/event-stream",
            "Cache-Control": "no-cache",
            "Content-Type": "application/json",
        }
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"

        return urllib.request.Request(
            url,
            data=json.dumps(payload).encode("utf-8"),
            headers=headers,
            method="POST",
        )

    @staticmethod
    def _responses_api_unsupported(message: str) -> bool:
        lowered = message.lower()
        return "/responses" in lowered or "previous_response_id" in lowered or "conversation" in lowered or "http 404" in lowered

    @staticmethod
    def _chat_completions_api_unsupported(message: str) -> bool:
        lowered = message.lower()
        return "/chat/completions" in lowered or "http 404" in lowered

    @staticmethod
    def _extract_responses_text(payload: dict[str, Any]) -> str:
        output_text = payload.get("output_text")
        if isinstance(output_text, str) and output_text.strip():
            return output_text.strip()

        parts: list[str] = []
        for item in payload.get("output") or []:
            if not isinstance(item, dict) or item.get("type") != "message":
                continue
            if item.get("role") != "assistant":
                continue
            content = item.get("content")
            if isinstance(content, str):
                if content.strip():
                    parts.append(content.strip())
                continue
            if not isinstance(content, list):
                continue
            for block in content:
                if not isinstance(block, dict):
                    continue
                text = block.get("text") or block.get("output_text")
                if isinstance(text, str) and text.strip():
                    parts.append(text.strip())
        return "\n".join(parts).strip()

    @staticmethod
    def _extract_responses_delta(event_name: str, payload: dict[str, Any]) -> str:
        if event_name == "response.output_text.delta":
            delta = payload.get("delta") or payload.get("text") or payload.get("output_text")
            return delta if isinstance(delta, str) else ""
        if event_name == "response.output_item.added":
            item = payload.get("item")
            if isinstance(item, dict):
                return HermesClient._extract_responses_text({"output": [item]})
        return ""

    @staticmethod
    def _extract_chat_completions_delta(payload: dict[str, Any]) -> str:
        choices = payload.get("choices")
        if not isinstance(choices, list) or not choices:
            return ""
        delta = (choices[0] or {}).get("delta")
        if not isinstance(delta, dict):
            return ""
        content = delta.get("content")
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            parts: list[str] = []
            for item in content:
                if not isinstance(item, dict):
                    continue
                text = item.get("text")
                if isinstance(text, str):
                    parts.append(text)
            return "".join(parts)
        return ""


class Bridge:
    def __init__(self, config_path: str) -> None:
        with open(config_path, "r", encoding="utf-8") as fh:
            config = json.load(fh)

        manifest_path = str(config.get("manifest_path") or DEFAULT_MANIFEST_PATH).strip() or DEFAULT_MANIFEST_PATH
        manifest = read_agent_manifest(manifest_path)
        self.manifest = manifest
        manifest_name = str(manifest.get("name") or "").strip()
        self.agent_name = str(config.get("agent_name") or "").strip() or manifest_name or "hermes"
        self.agent_key_name = str(config.get("agent_key_name") or "").strip() or self.agent_name
        self.sky10_rpc_url = str(config.get("sky10_rpc_url") or "").strip()
        if not self.sky10_rpc_url:
            raise BridgeError("Hermes bridge config is missing sky10_rpc_url")

        self.x402_ws_url = derive_x402_ws_url(
            self.sky10_rpc_url,
            self.agent_name,
            str(config.get("x402_ws_url") or "").strip(),
        )
        self.x402_helper_path = str(config.get("x402_helper_path") or DEFAULT_X402_HELPER_PATH).strip() or DEFAULT_X402_HELPER_PATH
        raw_skills = config.get("skills") or []
        self.tools = normalize_tools(config.get("tools") or manifest.get("tools"))
        self.tools = ensure_x402_tool(self.tools, self.x402_helper_path)
        manifest_skills = skills_from_tools(self.tools)
        self.skills = [str(skill).strip() for skill in raw_skills if str(skill).strip()] or manifest_skills or list(DEFAULT_AGENT_SKILLS)
        self.sky10 = Sky10Client(self.sky10_rpc_url)
        self.hermes = HermesClient()
        self.skip_warmup = env_truthy("HERMES_BRIDGE_SKIP_WARMUP")
        self.stop_event = threading.Event()
        self.active_response_lock = threading.Lock()
        self.active_response: Any = None
        self.agent_id = ""
        self.seen_lock = threading.Lock()
        self.seen_messages: OrderedDict[str, float] = OrderedDict()

    def run(self) -> None:
        self.install_x402_helper()
        self.hermes.wait_until_ready(self.stop_event)
        self.ensure_registered()
        warmup_thread = None
        if not self.skip_warmup:
            warmup_thread = threading.Thread(target=self._warm_up_background, name="sky10-hermes-warmup", daemon=True)
            warmup_thread.start()

        heartbeat_thread = threading.Thread(target=self._heartbeat_loop, name="sky10-hermes-heartbeat", daemon=True)
        heartbeat_thread.start()

        try:
            self._event_loop()
        finally:
            self.stop_event.set()
            heartbeat_thread.join(timeout=2)
            if warmup_thread is not None:
                warmup_thread.join(timeout=2)

    def shutdown(self, *_args: Any) -> None:
        self.stop_event.set()
        with self.active_response_lock:
            response = self.active_response
        if response is not None:
            try:
                response.close()
            except Exception:
                pass

    def ensure_registered(self) -> None:
        self.agent_id = self.sky10.register(self.agent_name, self.agent_key_name, self.skills, self.tools)
        log(f"Registered Hermes bridge as {self.agent_id} ({self.agent_name})")

    def install_x402_helper(self) -> None:
        try:
            path = install_x402_helper(self.x402_helper_path, os.path.abspath(sys.argv[0]), self.x402_ws_url)
            log(f"Installed sky10 x402 helper at {path}")
        except Exception as exc:
            log(f"Failed to install sky10 x402 helper: {exc}")

    def x402_prompt_context(self) -> str:
        try:
            services = x402_list_services(self.x402_ws_url)
        except Exception as exc:
            log(f"x402 service discovery unavailable: {exc}")
            return ""
        if services:
            log(f"x402 approved services available: {len(services)}")
        return format_x402_prompt_context(services, self.x402_helper_path)

    def _warm_up_background(self) -> None:
        try:
            self.hermes.warm_up()
        except Exception as exc:
            if not self.stop_event.is_set():
                log(f"Hermes API warm-up failed: {exc}")

    def _heartbeat_loop(self) -> None:
        while not self.stop_event.wait(HEARTBEAT_INTERVAL_SECONDS):
            try:
                if not self.agent_id:
                    self.ensure_registered()
                    continue
                self.sky10.heartbeat(self.agent_id)
            except Exception as exc:
                log(f"Heartbeat failed, re-registering: {exc}")
                self.agent_id = ""

    def _event_loop(self) -> None:
        while not self.stop_event.is_set():
            try:
                request = urllib.request.Request(
                    self.sky10.events_url,
                    headers={
                        "Accept": "text/event-stream",
                        "Cache-Control": "no-cache",
                    },
                    method="GET",
                )
                with urllib.request.urlopen(request, timeout=3600) as response:
                    with self.active_response_lock:
                        self.active_response = response
                    try:
                        log(f"Listening for sky10 events from {self.sky10.events_url}")
                        for event_name, data in iter_sse(response):
                            if self.stop_event.is_set():
                                return
                            if event_name != "agent.message":
                                continue
                            try:
                                payload = json.loads(data)
                            except json.JSONDecodeError:
                                log("Skipping malformed sky10 SSE payload")
                                continue
                            message = payload.get("data") if isinstance(payload, dict) else None
                            if not isinstance(message, dict):
                                continue
                            self._dispatch_message(message)
                    finally:
                        with self.active_response_lock:
                            if self.active_response is response:
                                self.active_response = None
            except Exception as exc:
                if self.stop_event.is_set():
                    return
                log(f"SSE connection lost: {exc}; reconnecting in {RECONNECT_DELAY_SECONDS}s")
                self.stop_event.wait(RECONNECT_DELAY_SECONDS)

    def _dispatch_message(self, message: dict[str, Any]) -> None:
        target = str(message.get("to") or "").strip()
        if target not in {self.agent_id, self.agent_name}:
            return

        message_id = str(message.get("id") or f"{message.get('session_id', 'main')}:{message.get('timestamp', time.time())}")
        if not self._claim_message(message_id):
            return

        thread = threading.Thread(target=self._handle_message, args=(message,), name=f"sky10-hermes-{message_id}", daemon=True)
        thread.start()

    def _handle_message(self, message: dict[str, Any]) -> None:
        session_id = str(message.get("session_id") or "main").strip() or "main"
        content = message.get("content")
        if str(message.get("type") or "").strip() == "tool_call":
            self._handle_tool_call(content if isinstance(content, dict) else {})
            return

        client_request_id = extract_client_request_id(content)
        sender = str(message.get("from") or "").strip()
        if not sender:
            log("Skipping inbound message without sender")
            return

        stream_id = uuid.uuid4().hex
        try:
            reply = self.hermes.stream(
                session_id,
                content,
                lambda chunk: self.sky10.send_delta(sender, session_id, chunk, sender, stream_id, client_request_id),
            )
            reply_content = build_outbound_content(reply)
            if reply_content:
                reply_content["stream_id"] = stream_id
                if client_request_id:
                    reply_content["client_request_id"] = client_request_id
                msg_type = "chat" if isinstance(reply_content.get("parts"), list) and reply_content.get("parts") else "text"
                self.sky10.send_content(
                    sender,
                    session_id,
                    reply_content,
                    sender,
                    msg_type,
                )
                self.sky10.send_done(sender, session_id, sender, stream_id, client_request_id)
                log(f"Reply sent for session {session_id}")
        except Exception as exc:
            error_text = f"Hermes bridge error: {exc}"
            log(error_text)
            try:
                error_content = {
                    "text": error_text,
                    "stream_id": stream_id,
                }
                if client_request_id:
                    error_content["client_request_id"] = client_request_id
                self.sky10.send_content(
                    sender,
                    session_id,
                    error_content,
                    sender,
                    "error",
                )
            except Exception as send_exc:
                log(f"Failed to send bridge error back to sky10: {send_exc}")

    def _handle_tool_call(self, content: dict[str, Any]) -> None:
        job_context = content.get("job_context") if isinstance(content.get("job_context"), dict) else {}
        job_id = str(content.get("job_id") or job_context.get("job_id") or "").strip()
        if not job_id:
            log("Skipping tool_call without job_id")
            return
        tool = str(content.get("tool") or content.get("capability") or "tool").strip() or "tool"
        output_dir = resolve_job_output_dir(job_id, content)
        try:
            os.makedirs(output_dir, exist_ok=True)
            self.sky10.update_job_status(job_id, "running", f"Running {tool}")
            x402_context = self.x402_prompt_context()
            reply = self.hermes.stream(
                job_id,
                {"text": build_tool_call_prompt(content, output_dir, self.manifest, x402_context)},
                lambda _chunk: None,
            )
            output_refs = collect_output_refs(output_dir)
            self.sky10.complete_job(
                job_id,
                {
                    "summary": reply,
                    "text": reply,
                    "artifacts": output_refs,
                },
                payload_refs=output_refs,
                message="Tool call completed.",
            )
            log(f"Tool call completed for job {job_id}")
        except Exception as exc:
            error_text = str(exc)
            log(f"Tool call failed for job {job_id}: {error_text}")
            try:
                self.sky10.fail_job(job_id, "runtime_error", error_text)
            except Exception as fail_exc:
                log(f"Failed to mark job {job_id} failed: {fail_exc}")

    def _claim_message(self, message_id: str) -> bool:
        now = time.time()
        with self.seen_lock:
            while self.seen_messages:
                oldest_id, seen_at = next(iter(self.seen_messages.items()))
                if now - seen_at <= SEEN_TTL_SECONDS:
                    break
                self.seen_messages.pop(oldest_id, None)
            if message_id in self.seen_messages:
                return False
            self.seen_messages[message_id] = now
        return True


def run_x402_cli(args: list[str]) -> int:
    command = args[0] if args else ""
    raw = args[1] if len(args) > 1 else ""
    ws_url = os.environ.get("SKY10_X402_WS_URL", "").strip()
    if not ws_url:
        config_path = os.environ.get("SKY10_BRIDGE_CONFIG_PATH", "/sandbox-state/bridge.json").strip()
        if config_path and os.path.exists(config_path):
            with open(config_path, "r", encoding="utf-8") as fh:
                config = json.load(fh)
            agent_name = str(config.get("agent_name") or "").strip()
            ws_url = derive_x402_ws_url(str(config.get("sky10_rpc_url") or "http://127.0.0.1:9101"), agent_name, str(config.get("x402_ws_url") or ""))
        else:
            ws_url = derive_x402_ws_url("http://127.0.0.1:9101")

    def parse_arg(label: str) -> dict[str, Any]:
        if not raw:
            return {}
        try:
            value = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise BridgeError(f"{label} must be JSON: {exc}") from exc
        return value if isinstance(value, dict) else {}

    if command == "list":
        result = x402_request(ws_url, "x402.list_services", parse_arg("list params"))
    elif command == "budget":
        result = x402_request(ws_url, "x402.budget_status", {})
    elif command == "call":
        result = x402_request(ws_url, "x402.service_call", parse_arg("call params"))
    else:
        print("usage: sky10-x402 <list [json] | budget | call json>", file=sys.stderr)
        return 2
    print(json.dumps(result, ensure_ascii=True, indent=2, sort_keys=True))
    return 0


def main() -> int:
    if len(sys.argv) > 1 and sys.argv[1] == "--x402":
        return run_x402_cli(sys.argv[2:])

    config_path = os.environ.get("SKY10_BRIDGE_CONFIG_PATH", "/sandbox-state/bridge.json").strip()
    if not config_path:
        raise BridgeError("SKY10_BRIDGE_CONFIG_PATH is empty")
    if not os.path.exists(config_path):
        raise BridgeError(f"Hermes bridge config not found: {config_path}")

    bridge = Bridge(config_path)
    signal.signal(signal.SIGINT, bridge.shutdown)
    signal.signal(signal.SIGTERM, bridge.shutdown)
    bridge.run()
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BridgeError as exc:
        log(str(exc))
        raise SystemExit(1)
