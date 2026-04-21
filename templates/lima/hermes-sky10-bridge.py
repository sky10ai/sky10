#!/usr/bin/env python3
"""Bridge sky10 agent messages to a Hermes API server running in the guest."""

from __future__ import annotations

import json
import base64
import mimetypes
import os
import re
import signal
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
WARMUP_PROMPT = "Reply with exactly OK."
MEDIA_ROOT = os.path.join(tempfile.gettempdir(), "sky10-hermes-media")


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

    def register(self, agent_name: str, agent_key_name: str, skills: list[str]) -> str:
        result = self.rpc(
            "agent.register",
            {
                "name": agent_name,
                "key_name": agent_key_name,
                "skills": skills,
            },
        )
        agent_id = (result or {}).get("agent_id", "").strip()
        if not agent_id:
            raise BridgeError("sky10 RPC agent.register returned an empty agent_id")
        return agent_id

    def heartbeat(self, agent_id: str) -> None:
        self.rpc("agent.heartbeat", {"agent_id": agent_id})

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
        self.chat_history: dict[str, list[dict[str, str]]] = {}
        self.chat_pending_turns: dict[str, dict[int, tuple[str, str] | None]] = {}
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

    def stream(self, session_id: str, text: str, on_delta: Callable[[str], None]) -> str:
        if self.use_responses_api:
            try:
                return self._stream_responses(session_id, text, on_delta)
            except BridgeError as exc:
                if self._responses_api_unsupported(str(exc)):
                    log(f"Hermes Responses API unavailable, falling back to chat completions: {exc}")
                    self.use_responses_api = False
                else:
                    raise
            return self._stream_chat_completions(session_id, text, on_delta)
        try:
            return self._stream_chat_completions(session_id, text, on_delta)
        except BridgeError as exc:
            if self._chat_completions_api_unsupported(str(exc)):
                log(f"Hermes chat completions unavailable, falling back to Responses API: {exc}")
                self.use_responses_api = True
            else:
                raise
        return self._stream_responses(session_id, text, on_delta)

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

    def _stream_chat_completions(self, session_id: str, text: str, on_delta: Callable[[str], None]) -> str:
        turn_id, history = self._reserve_chat_turn(session_id)
        history.append({"role": "user", "content": text})

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
        self._commit_chat_turn(session_id, turn_id, text, answer)
        return answer

    def _reserve_chat_turn(self, session_id: str) -> tuple[int, list[dict[str, str]]]:
        with self.history_lock:
            history = list(self.chat_history.get(session_id, []))
            turn_id = self.chat_next_turn_id.get(session_id, 0)
            self.chat_next_turn_id[session_id] = turn_id + 1
        return turn_id, history

    def _commit_chat_turn(self, session_id: str, turn_id: int, user_text: str, answer: str) -> None:
        self._resolve_chat_turn(session_id, turn_id, (user_text, answer))

    def _skip_chat_turn(self, session_id: str, turn_id: int) -> None:
        self._resolve_chat_turn(session_id, turn_id, None)

    def _resolve_chat_turn(self, session_id: str, turn_id: int, turn: tuple[str, str] | None) -> None:
        with self.history_lock:
            pending = self.chat_pending_turns.setdefault(session_id, {})
            pending[turn_id] = turn
            next_commit_id = self.chat_next_commit_id.get(session_id, 0)
            history = self.chat_history.setdefault(session_id, [])
            while next_commit_id in pending:
                completed_turn = pending.pop(next_commit_id)
                if completed_turn is not None:
                    user_text, answer = completed_turn
                    history.append({"role": "user", "content": user_text})
                    history.append({"role": "assistant", "content": answer})
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

        self.agent_name = str(config.get("agent_name") or "").strip() or "hermes"
        self.agent_key_name = str(config.get("agent_key_name") or "").strip() or self.agent_name
        self.host_rpc_url = str(config.get("host_rpc_url") or "").strip()
        if not self.host_rpc_url:
            raise BridgeError("Hermes bridge config is missing host_rpc_url")

        raw_skills = config.get("skills") or []
        self.skills = [str(skill).strip() for skill in raw_skills if str(skill).strip()] or list(DEFAULT_AGENT_SKILLS)
        self.sky10 = Sky10Client(self.host_rpc_url)
        self.hermes = HermesClient()
        self.skip_warmup = env_truthy("HERMES_BRIDGE_SKIP_WARMUP")
        self.stop_event = threading.Event()
        self.agent_id = ""
        self.seen_lock = threading.Lock()
        self.seen_messages: OrderedDict[str, float] = OrderedDict()

    def run(self) -> None:
        self.hermes.wait_until_ready(self.stop_event)
        if not self.skip_warmup:
            try:
                self.hermes.warm_up()
            except Exception as exc:
                log(f"Hermes API warm-up failed: {exc}")
        self.ensure_registered()

        heartbeat_thread = threading.Thread(target=self._heartbeat_loop, name="sky10-hermes-heartbeat", daemon=True)
        heartbeat_thread.start()

        try:
            self._event_loop()
        finally:
            self.stop_event.set()
            heartbeat_thread.join(timeout=2)

    def shutdown(self, *_args: Any) -> None:
        self.stop_event.set()

    def ensure_registered(self) -> None:
        self.agent_id = self.sky10.register(self.agent_name, self.agent_key_name, self.skills)
        log(f"Registered Hermes bridge as {self.agent_id} ({self.agent_name})")

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
        user_text = build_inbound_body(content, session_id)
        client_request_id = extract_client_request_id(content)
        sender = str(message.get("from") or "").strip()
        if not sender:
            log("Skipping inbound message without sender")
            return

        stream_id = uuid.uuid4().hex
        try:
            reply = self.hermes.stream(
                session_id,
                user_text,
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

def main() -> int:
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
