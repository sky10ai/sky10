/**
 * OpenClaw sky10 channel plugin.
 *
 * This bundled Lima variant auto-registers the guest on the local sky10
 * daemon by running the bridge as a plugin service, while exposing a stable
 * outbound channel account for direct sends.
 */

import { Sky10Client } from "./sky10.js";

const GLOBAL_STATE_KEY = Symbol.for("sky10.openclaw.bridge");
const DEDUP_TTL_MS = 30_000;

function getBridgeState() {
  const globalScope = globalThis;
  if (!globalScope[GLOBAL_STATE_KEY]) {
    globalScope[GLOBAL_STATE_KEY] = {
      client: null,
      agentId: null,
      heartbeatTimer: null,
      sseConnection: null,
      runtimeInitPromise: null,
      shutdownRequested: false,
      serviceRefs: 0,
      seenIds: new Map(),
    };
  }
  return globalScope[GLOBAL_STATE_KEY];
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function makeLogger(api) {
  const base = api?.logger ?? console;
  return {
    info: (...args) => (base.info ?? console.log).call(base, ...args),
    warn: (...args) => (base.warn ?? console.warn).call(base, ...args),
    error: (...args) => (base.error ?? console.error).call(base, ...args),
  };
}

function resolveAccount(cfg, accountId) {
  return { accountId: accountId ?? cfg.agentName };
}

function resolveConfig(api) {
  const c = api?.pluginConfig ?? api?.config?.sky10 ?? api?.config ?? {};
  return {
    rpcUrl: c.rpcUrl ?? "http://localhost:9101",
    agentName: c.agentName ?? "openclaw",
    skills: c.skills ?? ["code", "shell", "browser", "web-search", "file-ops"],
    gatewayUrl: c.gatewayUrl ?? "http://localhost:18789",
    gatewayToken: c.gatewayToken ?? "",
  };
}

function stopRuntime() {
  const state = getBridgeState();
  if (state.heartbeatTimer) {
    clearInterval(state.heartbeatTimer);
    state.heartbeatTimer = null;
  }
  if (state.sseConnection) {
    state.sseConnection.close();
    state.sseConnection = null;
  }
  state.runtimeInitPromise = null;
}

async function ensureRegistered(log, cfg) {
  const state = getBridgeState();
  const reg = await state.client.register(cfg.agentName, cfg.skills);
  state.agentId = reg.agent_id;
  log.info(`sky10: registered as ${state.agentId} (${cfg.agentName})`);
}

function startHeartbeat(log, cfg) {
  const state = getBridgeState();
  if (state.heartbeatTimer) return;
  state.heartbeatTimer = setInterval(async () => {
    try {
      if (!state.agentId) {
        return;
      }
      await state.client.heartbeat(state.agentId);
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

function waitForAbort(signal) {
  if (signal?.aborted) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    signal?.addEventListener("abort", resolve, { once: true });
  });
}

async function bootstrapRuntime(log, cfg) {
  const state = getBridgeState();
  while (!state.shutdownRequested) {
    try {
      await ensureRuntime(log, cfg);
      return;
    } catch (err) {
      log.warn(`sky10: runtime init failed: ${err?.message ?? err}`);
      await sleep(5_000);
    }
  }
}

async function ensureRuntime(log, cfg) {
  const state = getBridgeState();
  if (state.shutdownRequested) {
    throw new Error("plugin is shutting down");
  }
  if (state.runtimeInitPromise) return state.runtimeInitPromise;
  state.runtimeInitPromise = (async () => {
    state.client ??= new Sky10Client(cfg.rpcUrl);
    await ensureRegistered(log, cfg);
    startHeartbeat(log, cfg);
    if (!state.sseConnection) {
      startListener(log, cfg);
    }
  })().catch((err) => {
    state.runtimeInitPromise = null;
    throw err;
  });
  return state.runtimeInitPromise;
}

export default function register(api) {
  const log = makeLogger(api);
  const cfg = resolveConfig(api);
  const state = getBridgeState();

  log.info(`sky10: config = ${JSON.stringify(cfg)}`);
  state.client = new Sky10Client(cfg.rpcUrl);

  if (api.registrationMode === "cli-metadata") {
    return;
  }

  try {
    api.registerChannel({ plugin: createChannel(api, cfg, log) });
    log.info("sky10: channel registered");
  } catch (err) {
    log.warn(`sky10: registerChannel failed: ${err?.message ?? err}`);
  }

  if (api.registrationMode !== "full") {
    return;
  }

  try {
    api.registerService({
      id: "sky10-bridge",
      start: async () => {
        state.serviceRefs += 1;
        state.shutdownRequested = false;
        await bootstrapRuntime(log, cfg);
      },
      stop: async () => {
        state.serviceRefs = Math.max(0, state.serviceRefs - 1);
        if (state.serviceRefs === 0) {
          state.shutdownRequested = true;
          stopRuntime();
        }
      },
    });
    log.info("sky10: bridge service registered");
  } catch (err) {
    log.warn(`sky10: registerService failed: ${err?.message ?? err}`);
  }
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
      listAccountIds: () => [cfg.agentName],
      resolveAccount: (_cfg, accountId) => resolveAccount(cfg, accountId),
      defaultAccountId: () => cfg.agentName,
      isEnabled: () => true,
      isConfigured: () => true,
      describeAccount: (account) => ({
        accountId: account.accountId,
        name: account.accountId,
        enabled: true,
        configured: true,
      }),
    },
    outbound: {
      deliveryMode: "direct",
      sendText: async (params) => {
        try {
          await ensureRuntime(log, cfg);
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
    const state = getBridgeState();
    const parsed = JSON.parse(data);
    const msg = parsed.data ?? parsed;

    if (msg.to !== state.agentId && msg.to !== cfg.agentName) {
      return;
    }

    const msgId = msg.id || `${msg.session_id}:${msg.from}:${msg.timestamp ?? ""}`;
    if (state.seenIds.has(msgId)) {
      return;
    }
    state.seenIds.set(msgId, Date.now());
    for (const [key, ts] of state.seenIds) {
      if (Date.now() - ts > DEDUP_TTL_MS) {
        state.seenIds.delete(key);
      }
    }

    const text = msg.content?.text ?? JSON.stringify(msg.content ?? {});
    void dispatchInbound(log, cfg, msg, text);
  } catch (err) {
    log.error(`sky10: SSE parse error: ${err?.message ?? err}`);
  }
}

function startListener(log, cfg) {
  const state = getBridgeState();
  const url = state.client.sseUrl();
  const controller = new AbortController();
  let closed = false;

  state.sseConnection = {
    close() {
      closed = true;
      controller.abort();
    },
  };

  void (async () => {
    const decoder = new TextDecoder();
    while (!closed && !state.shutdownRequested) {
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
        while (!closed && !state.shutdownRequested) {
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
        if (closed || controller.signal.aborted || state.shutdownRequested) {
          return;
        }
        log.warn(`sky10: SSE connection lost: ${err?.message ?? err}; reconnecting in 5s`);
      }

      if (closed || controller.signal.aborted || state.shutdownRequested) {
        return;
      }
      await sleep(5_000);
    }
  })().catch((err) => {
    if (!closed && !controller.signal.aborted && !state.shutdownRequested) {
      log.error(`sky10: SSE loop crashed: ${err?.message ?? err}`);
    }
  });
}
