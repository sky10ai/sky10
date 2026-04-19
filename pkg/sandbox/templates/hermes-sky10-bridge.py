#!/usr/bin/env python3
"""Bridge sky10 agent messages to a Hermes API server running in the guest."""

from __future__ import annotations

import json
import os
import signal
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from collections import OrderedDict
from typing import Any, Callable


HEARTBEAT_INTERVAL_SECONDS = 25
RECONNECT_DELAY_SECONDS = 5
SEEN_TTL_SECONDS = 30
DEFAULT_AGENT_SKILLS = ["code", "shell", "web-search", "file-ops"]


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


def extract_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, dict):
        text = content.get("text")
        if isinstance(text, str):
            return text
    return json.dumps(content, ensure_ascii=True)


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

    def send_delta(self, to: str, session_id: str, text: str, device_id: str, stream_id: str) -> None:
        if not text:
            return
        self.send_content(
            to,
            session_id,
            {
                "text": text,
                "stream_id": stream_id,
            },
            device_id,
            "delta",
        )

    def send_done(self, to: str, session_id: str, device_id: str, stream_id: str) -> None:
        self.send_content(
            to,
            session_id,
            {
                "stream_id": stream_id,
            },
            device_id,
            "done",
        )


class HermesClient:
    def __init__(self) -> None:
        api_base = os.environ.get("HERMES_API_BASE_URL", "http://127.0.0.1:8642/v1").strip()
        self.api_base = strip_trailing_path(api_base, "/")
        self.api_key = os.environ.get("API_SERVER_KEY", "").strip()
        self.model = os.environ.get("API_SERVER_MODEL_NAME", "hermes-agent").strip() or "hermes-agent"
        self.use_responses_api = True
        self.history_lock = threading.Lock()
        self.chat_history: dict[str, list[dict[str, str]]] = {}

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
        with self.history_lock:
            history = list(self.chat_history.get(session_id, []))
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
            body = exc.read().decode("utf-8", errors="replace")
            raise BridgeError(f"Hermes API /chat/completions failed with HTTP {exc.code}: {body}") from exc
        except urllib.error.URLError as exc:
            raise BridgeError(f"Hermes API /chat/completions failed: {exc.reason}") from exc

        answer = "".join(chunks).strip() or "Done."
        history.append({"role": "assistant", "content": answer})
        with self.history_lock:
            self.chat_history[session_id] = history
        return answer

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
        self.stop_event = threading.Event()
        self.agent_id = ""
        self.seen_lock = threading.Lock()
        self.seen_messages: OrderedDict[str, float] = OrderedDict()
        self.session_lock_guard = threading.Lock()
        self.session_locks: dict[str, threading.Lock] = {}

    def run(self) -> None:
        self.hermes.wait_until_ready(self.stop_event)
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
        with self._session_lock(session_id):
            user_text = extract_text(message.get("content"))
            sender = str(message.get("from") or "").strip()
            if not sender:
                log("Skipping inbound message without sender")
                return

            stream_id = uuid.uuid4().hex
            try:
                reply = self.hermes.stream(
                    session_id,
                    user_text,
                    lambda chunk: self.sky10.send_delta(sender, session_id, chunk, sender, stream_id),
                )
                if reply.strip():
                    self.sky10.send_content(
                        sender,
                        session_id,
                        {
                            "text": reply,
                            "stream_id": stream_id,
                        },
                        sender,
                        "text",
                    )
                    self.sky10.send_done(sender, session_id, sender, stream_id)
                    log(f"Reply sent for session {session_id}")
            except Exception as exc:
                error_text = f"Hermes bridge error: {exc}"
                log(error_text)
                try:
                    self.sky10.send_content(
                        sender,
                        session_id,
                        {
                            "text": error_text,
                            "stream_id": stream_id,
                        },
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

    def _session_lock(self, session_id: str) -> threading.Lock:
        with self.session_lock_guard:
            lock = self.session_locks.get(session_id)
            if lock is None:
                lock = threading.Lock()
                self.session_locks[session_id] = lock
            return lock

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
