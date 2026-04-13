/**
 * OpenClaw sky10 channel plugin.
 *
 * This bundled Lima variant starts its sky10 runtime eagerly so the guest
 * auto-registers on the local sky10 daemon without requiring extra channel
 * activation inside OpenClaw.
 */

import { Sky10Client } from "./sky10.js";

let client;
let agentId;
let heartbeatTimer;
let sseConnection = null;
let runtimeInitPromise = null;
let shutdownRequested = false;

const seenIds = new Map();
const DEDUP_TTL_MS = 30_000;

function makeLogger(api) {
  const base = api?.logger ?? console;
  return {
    info: (...args) => (base.info ?? console.log).call(base, ...args),
    warn: (...args) => (base.warn ?? console.warn).call(base, ...args),
    error: (...args) => (base.error ?? console.error).call(base, ...args),
  };
}

function resolveConfig(api) {
  const c = api?.pluginConfig ?? api?.config?.sky10 ?? api?.config ?? {};
  return {
    rpcUrl: c.rpcUrl ?? "http://localhost:9101",
    agentName: c.agentName ?? "openclaw",
    skills: c.skills ?? ["code", "shell", "web-search", "file-ops"],
    gatewayUrl: c.gatewayUrl ?? "http://localhost:18789",
    gatewayToken: c.gatewayToken ?? "",
  };
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function stopRuntime() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
  if (sseConnection) {
    sseConnection.close();
    sseConnection = null;
  }
  runtimeInitPromise = null;
}

async function ensureRegistered(log, cfg) {
  const reg = await client.register(cfg.agentName, cfg.skills);
  agentId = reg.agent_id;
  log.info(`sky10: registered as ${agentId} (${cfg.agentName})`);
}

function startHeartbeat(log, cfg) {
  if (heartbeatTimer) return;
  heartbeatTimer = setInterval(async () => {
    try {
      await client.heartbeat(agentId);
    } catch {
      log.warn("sky10: heartbeat failed, re-registering");
      try {
        await ensureRegistered(log, cfg);
      } catch (err) {
        log.warn(`sky10: re-register failed: ${err?.message ?? err}`);
      }
    }
  }, 25_000);
}

async function bootstrapRuntime(api, log, cfg) {
  while (!shutdownRequested) {
    try {
      await ensureRuntime(api, log, cfg);
      return;
    } catch (err) {
      log.warn(`sky10: runtime init failed: ${err?.message ?? err}`);
      await sleep(5_000);
    }
  }
}

async function ensureRuntime(api, log, cfg) {
  if (shutdownRequested) {
    throw new Error("plugin is shutting down");
  }
  if (runtimeInitPromise) return runtimeInitPromise;
  runtimeInitPromise = (async () => {
    client ??= new Sky10Client(cfg.rpcUrl);
    if (!agentId) {
      await ensureRegistered(log, cfg);
    }
    startHeartbeat(log, cfg);
    if (!sseConnection) {
      startListener(api, log, cfg);
    }
  })().catch((err) => {
    runtimeInitPromise = null;
    throw err;
  });
  return runtimeInitPromise;
}

export default function register(api) {
  const log = makeLogger(api);
  const cfg = resolveConfig(api);

  log.info(`sky10: config = ${JSON.stringify(cfg)}`);
  client = new Sky10Client(cfg.rpcUrl);

  try {
    api.registerChannel({ plugin: createChannel(api, cfg, log) });
    log.info("sky10: channel registered");
  } catch (err) {
    log.warn(`sky10: registerChannel failed: ${err?.message ?? err}`);
  }

  shutdownRequested = false;
  void bootstrapRuntime(api, log, cfg);
}

function createChannel(api, cfg, log) {
  return {
    id: "sky10",
    meta: {
      id: "sky10",
      label: "Sky10",
      selectionLabel: "Sky10 P2P Network",
      blurb: "Communicate with agents on the sky10 P2P network",
    },
    capabilities: {
      chatTypes: ["direct"],
    },
    config: {
      listAccountIds: () => [agentId ?? cfg.agentName],
      resolveAccount: (_cfg, accountId) => ({ accountId: accountId ?? agentId ?? cfg.agentName }),
    },
    gateway: {
      startAccount: async () => {
        shutdownRequested = false;
        await ensureRuntime(api, log, cfg);
      },
      stopAccount: async () => {
        shutdownRequested = true;
        stopRuntime();
      },
    },
    outbound: {
      deliveryMode: "direct",
      sendText: async (params) => {
        try {
          await ensureRuntime(api, log, cfg);
          const result = await client.send(params.to, params.sessionId, params.text, params.to);
          return { ok: true, messageId: result?.id };
        } catch (err) {
          log.error(`sky10: sendText failed: ${err?.message ?? err}`);
          return { ok: false, error: String(err) };
        }
      },
    },
  };
}

async function dispatchInbound(log, cfg, msg, text) {
  log.info(`sky10: dispatching via gateway API ${cfg.gatewayUrl}/v1/responses`);
  try {
    const headers = { "Content-Type": "application/json" };
    if (cfg.gatewayToken) {
      headers.Authorization = `Bearer ${cfg.gatewayToken}`;
    }

    let res = await fetch(`${cfg.gatewayUrl}/v1/responses`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        model: "openclaw",
        input: text,
        user: `sky10:${msg.from}:${msg.session_id}`,
      }),
    });

    if (res.status === 404) {
      res = await fetch(`${cfg.gatewayUrl}/v1/chat/completions`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          model: "openclaw",
          messages: [{ role: "user", content: text }],
          user: `sky10:${msg.from}:${msg.session_id}`,
        }),
      });
    }

    if (!res.ok) {
      const body = await res.text();
      log.error(`sky10: gateway API ${res.status}: ${body.substring(0, 200)}`);
      return;
    }

    const data = await res.json();
    const reply = data.output_text
      ?? data.output?.[0]?.content?.[0]?.text
      ?? data.choices?.[0]?.message?.content;
    if (!reply) {
      log.warn(`sky10: empty reply from gateway API — keys: ${Object.keys(data).join(", ")}`);
      return;
    }

    await client.send(msg.from, msg.session_id, reply, msg.from);
    log.info("sky10: reply sent");
  } catch (err) {
    log.error(`sky10: gateway API dispatch failed: ${err?.message ?? err}`);
  }
}

function drainSSEBuffer(buffer, onEvent) {
  let boundary = buffer.indexOf("\n\n");
  while (boundary !== -1) {
    const rawEvent = buffer.slice(0, boundary);
    buffer = buffer.slice(boundary + 2);

    let eventName = "message";
    const dataLines = [];
    for (const line of rawEvent.split("\n")) {
      if (!line || line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const field = colon === -1 ? line : line.slice(0, colon);
      let value = colon === -1 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") eventName = value;
      if (field === "data") dataLines.push(value);
    }

    if (dataLines.length > 0) {
      onEvent(eventName, dataLines.join("\n"));
    }

    boundary = buffer.indexOf("\n\n");
  }
  return buffer;
}

function handleAgentMessage(log, cfg, data) {
  try {
    const parsed = JSON.parse(data);
    const msg = parsed.data ?? parsed;

    if (msg.to !== agentId && msg.to !== cfg.agentName) {
      return;
    }

    const msgId = msg.id || `${msg.session_id}:${msg.from}:${msg.timestamp ?? ""}`;
    if (seenIds.has(msgId)) {
      return;
    }
    seenIds.set(msgId, Date.now());
    for (const [key, ts] of seenIds) {
      if (Date.now() - ts > DEDUP_TTL_MS) {
        seenIds.delete(key);
      }
    }

    const text = msg.content?.text ?? JSON.stringify(msg.content ?? {});
    void dispatchInbound(log, cfg, msg, text);
  } catch (err) {
    log.error(`sky10: SSE parse error: ${err?.message ?? err}`);
  }
}

function startListener(_api, log, cfg) {
  const url = client.sseUrl();
  const controller = new AbortController();
  let closed = false;

  sseConnection = {
    close() {
      closed = true;
      controller.abort();
    },
  };

  void (async () => {
    const decoder = new TextDecoder();
    while (!closed && !shutdownRequested) {
      try {
        const response = await fetch(url, {
          headers: {
            Accept: "text/event-stream",
            "Cache-Control": "no-cache",
          },
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(`HTTP ${response.status}`);
        }
        if (!response.body) {
          throw new Error("event stream body missing");
        }

        log.info(`sky10: SSE connected to ${url}`);
        let buffer = "";
        const reader = response.body.getReader();
        while (!closed && !shutdownRequested) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n").replace(/\r/g, "\n");
          buffer = drainSSEBuffer(buffer, (eventName, data) => {
            if (eventName === "agent.message") {
              handleAgentMessage(log, cfg, data);
            }
          });
        }
        reader.releaseLock?.();
      } catch (err) {
        if (closed || controller.signal.aborted || shutdownRequested) {
          return;
        }
        log.warn(`sky10: SSE connection lost: ${err?.message ?? err}; reconnecting in 5s`);
      }

      if (closed || controller.signal.aborted || shutdownRequested) {
        return;
      }
      await sleep(5_000);
    }
  })().catch((err) => {
    if (!closed && !controller.signal.aborted && !shutdownRequested) {
      log.error(`sky10: SSE loop crashed: ${err?.message ?? err}`);
    }
  });
}
