/**
 * OpenClaw sky10 channel plugin.
 *
 * This bundled Lima variant registers the guest on the local sky10 daemon and
 * dispatches inbound messages through OpenClaw's native direct-DM runtime so
 * browser and tool behavior matches normal channel-driven sessions.
 */

import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { createChatChannelPlugin } from "/usr/lib/node_modules/openclaw/dist/plugin-sdk/core.js";
import { dispatchInboundDirectDmWithRuntime } from "/usr/lib/node_modules/openclaw/dist/plugin-sdk/direct-dm.js";

import { Sky10Client } from "./sky10.js";

const CHANNEL_ID = "sky10";
const CHANNEL_LABEL = "Sky10";
const DEFAULT_ACCOUNT_ID = "default";
const DEFAULT_SKILLS = ["code", "shell", "browser", "web-search", "file-ops"];
const GLOBAL_STATE_KEY = Symbol.for("sky10.openclaw.bridge");
const DEDUP_TTL_MS = 30_000;
const CLAIM_PRUNE_INTERVAL_MS = 60_000;
const CLAIM_DIR = path.join(os.homedir(), ".openclaw", ".sky10-bridge-seen");
const SKY10_ACCOUNT_PROPERTIES = {
  enabled: { type: "boolean" },
  rpcUrl: { type: "string" },
  agentName: { type: "string" },
  skills: {
    type: "array",
    items: { type: "string" },
  },
  gatewayToken: { type: "string" },
};
const SKY10_CHANNEL_CONFIG_SCHEMA = {
  schema: {
    type: "object",
    additionalProperties: false,
    properties: {
      enabled: { type: "boolean" },
      rpcUrl: { type: "string" },
      agentName: { type: "string" },
      skills: {
        type: "array",
        items: { type: "string" },
      },
      gatewayToken: { type: "string" },
      defaultAccount: { type: "string" },
      healthMonitor: {
        type: "object",
        additionalProperties: false,
        properties: {
          enabled: { type: "boolean" },
        },
      },
      accounts: {
        type: "object",
        additionalProperties: {
          type: "object",
          additionalProperties: false,
          properties: SKY10_ACCOUNT_PROPERTIES,
        },
      },
    },
  },
};

function getBridgeState() {
  const globalScope = globalThis;
  if (!globalScope[GLOBAL_STATE_KEY]) {
    globalScope[GLOBAL_STATE_KEY] = {
      client: null,
      agentId: null,
      pluginRuntime: null,
      lastClaimPruneAt: 0,
      seenIds: new Map(),
    };
  }
  return globalScope[GLOBAL_STATE_KEY];
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function claimPathFor(msgId) {
  return path.join(CLAIM_DIR, encodeURIComponent(msgId));
}

function pruneClaimFiles(now) {
  const state = getBridgeState();
  if (now - state.lastClaimPruneAt < CLAIM_PRUNE_INTERVAL_MS) {
    return;
  }
  state.lastClaimPruneAt = now;
  try {
    fs.mkdirSync(CLAIM_DIR, { recursive: true });
    for (const name of fs.readdirSync(CLAIM_DIR)) {
      const filePath = path.join(CLAIM_DIR, name);
      const stat = fs.statSync(filePath, { throwIfNoEntry: false });
      if (!stat) continue;
      if (now - stat.mtimeMs > DEDUP_TTL_MS) {
        fs.rmSync(filePath, { force: true });
      }
    }
  } catch {
    // Best-effort cleanup only.
  }
}

function claimMessage(msgId) {
  const now = Date.now();
  const state = getBridgeState();
  pruneClaimFiles(now);
  if (state.seenIds.has(msgId)) {
    return false;
  }
  try {
    fs.mkdirSync(CLAIM_DIR, { recursive: true });
    const fd = fs.openSync(claimPathFor(msgId), "wx");
    fs.closeSync(fd);
  } catch (err) {
    if (err?.code === "EEXIST") {
      return false;
    }
    throw err;
  }
  state.seenIds.set(msgId, now);
  for (const [key, ts] of state.seenIds) {
    if (now - ts > DEDUP_TTL_MS) {
      state.seenIds.delete(key);
    }
  }
  return true;
}

function makeLogger(api) {
  const base = api?.logger ?? console;
  return {
    info: (...args) => (base.info ?? console.log).call(base, ...args),
    warn: (...args) => (base.warn ?? console.warn).call(base, ...args),
    error: (...args) => (base.error ?? console.error).call(base, ...args),
  };
}

function normalizeAccountId(accountId) {
  return typeof accountId === "string" && accountId.trim() ? accountId.trim() : DEFAULT_ACCOUNT_ID;
}

function normalizeSkills(skills) {
  if (!Array.isArray(skills)) {
    return [...DEFAULT_SKILLS];
  }
  const normalized = skills.map((value) => String(value).trim()).filter(Boolean);
  return normalized.length > 0 ? normalized : [...DEFAULT_SKILLS];
}

function resolveSky10ChannelSection(cfg) {
  const section = cfg?.channels?.[CHANNEL_ID];
  return section && typeof section === "object" ? section : {};
}

function resolveMergedAccountConfig(cfg, accountId) {
  const section = resolveSky10ChannelSection(cfg);
  const resolvedAccountId = normalizeAccountId(accountId);
  const { accounts, defaultAccount, healthMonitor, ...base } = section;
  const accountOverrides = accounts && typeof accounts === "object" ? accounts[resolvedAccountId] ?? {} : {};
  return { ...base, ...accountOverrides };
}

function listSky10AccountIds(cfg) {
  const section = resolveSky10ChannelSection(cfg);
  const configured = section.accounts && typeof section.accounts === "object"
    ? Object.keys(section.accounts).filter(Boolean)
    : [];
  return [...new Set([resolveDefaultSky10AccountId(cfg), ...configured])];
}

function resolveDefaultSky10AccountId(cfg) {
  const section = resolveSky10ChannelSection(cfg);
  return normalizeAccountId(section.defaultAccount);
}

function resolveSky10Account({ cfg, accountId }) {
  const section = resolveSky10ChannelSection(cfg);
  const resolvedAccountId = normalizeAccountId(accountId);
  const merged = resolveMergedAccountConfig(cfg, resolvedAccountId);
  const rpcUrl = typeof merged.rpcUrl === "string" && merged.rpcUrl.trim()
    ? merged.rpcUrl.trim()
    : "http://localhost:9101";
  const agentName = typeof merged.agentName === "string" && merged.agentName.trim()
    ? merged.agentName.trim()
    : "openclaw";
  return {
    accountId: resolvedAccountId,
    name: agentName,
    enabled: section.enabled !== false && merged.enabled !== false,
    configured: Boolean(rpcUrl),
    rpcUrl,
    agentName,
    skills: normalizeSkills(merged.skills),
    gatewayToken: typeof merged.gatewayToken === "string" ? merged.gatewayToken.trim() : "",
  };
}

function waitForAbort(signal) {
  if (signal?.aborted) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    signal?.addEventListener("abort", resolve, { once: true });
  });
}

function resolveMessageId(msg) {
  return msg.id || `${msg.session_id}:${msg.from}:${msg.timestamp ?? ""}`;
}

function resolveMessageTimestamp(msg) {
  if (typeof msg.timestamp === "number" && Number.isFinite(msg.timestamp)) {
    return msg.timestamp < 1_000_000_000_000 ? msg.timestamp * 1000 : msg.timestamp;
  }
  if (typeof msg.timestamp === "string") {
    const parsed = Date.parse(msg.timestamp);
    if (!Number.isNaN(parsed)) {
      return parsed;
    }
  }
  return Date.now();
}

function resolveSessionPeerId(msg) {
  return `${msg.from}:${msg.session_id || "main"}`;
}

async function ensureRegistered(log, account, setStatus) {
  const state = getBridgeState();
  state.client ??= new Sky10Client(account.rpcUrl);
  const reg = await state.client.register(account.agentName, account.skills);
  state.agentId = reg.agent_id;
  setStatus({
    accountId: account.accountId,
    agentId: state.agentId,
    enabled: account.enabled,
    configured: account.configured,
    running: true,
    rpcUrl: account.rpcUrl,
  });
  log.info(`sky10: registered as ${state.agentId} (${account.agentName})`);
}

function startHeartbeat(log, account, setStatus, abortSignal) {
  const tick = async () => {
    const state = getBridgeState();
    if (!state.agentId) {
      return;
    }
    try {
      await state.client.heartbeat(state.agentId);
    } catch (err) {
      log.warn(`sky10: heartbeat failed, re-registering: ${err?.message ?? err}`);
      await ensureRegistered(log, account, setStatus);
    }
  };

  const timer = setInterval(() => {
    if (abortSignal?.aborted) {
      return;
    }
    void tick().catch((err) => {
      log.warn(`sky10: heartbeat tick failed: ${err?.message ?? err}`);
    });
  }, 25_000);

  return () => clearInterval(timer);
}

async function dispatchInbound(log, ctx, account, msg, text) {
  const state = getBridgeState();
  const runtime = state.pluginRuntime;
  if (!runtime?.channel) {
    throw new Error("sky10 runtime not initialized");
  }

  await dispatchInboundDirectDmWithRuntime({
    cfg: ctx.cfg,
    runtime,
    channel: CHANNEL_ID,
    channelLabel: CHANNEL_LABEL,
    accountId: account.accountId,
    peer: {
      kind: "direct",
      id: resolveSessionPeerId(msg),
    },
    senderId: msg.from,
    senderAddress: `sky10:${msg.from}`,
    recipientAddress: `sky10:${state.agentId ?? account.agentName}`,
    conversationLabel: `${msg.from} (${msg.session_id || "main"})`,
    rawBody: text,
    messageId: resolveMessageId(msg),
    timestamp: resolveMessageTimestamp(msg),
    commandAuthorized: true,
    deliver: async (payload) => {
      const outboundText = payload && typeof payload === "object" && "text" in payload ? payload.text ?? "" : "";
      if (!outboundText.trim()) {
        return;
      }
      await state.client.send(msg.from, msg.session_id, outboundText, msg.from);
      log.info("sky10: reply sent");
    },
    onRecordError: (err) => {
      log.error(`sky10: failed recording inbound session: ${err?.message ?? err}`);
    },
    onDispatchError: (err, info) => {
      log.error(`sky10: ${info.kind} reply failed: ${err?.message ?? err}`);
    },
    extraContext: {
      Sky10SessionId: msg.session_id,
      Sky10SenderId: msg.from,
    },
  });
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

function handleAgentMessage(log, ctx, account, data) {
  try {
    const state = getBridgeState();
    const parsed = JSON.parse(data);
    const msg = parsed.data ?? parsed;

    if (msg.to !== state.agentId && msg.to !== account.agentName) {
      return;
    }

    const msgId = resolveMessageId(msg);
    if (!claimMessage(msgId)) {
      return;
    }

    const text = msg.content?.text ?? JSON.stringify(msg.content ?? {});
    void dispatchInbound(log, ctx, account, msg, text).catch((err) => {
      log.error(`sky10: inbound dispatch failed: ${err?.message ?? err}`);
    });
  } catch (err) {
    log.error(`sky10: SSE parse error: ${err?.message ?? err}`);
  }
}

function startListener(log, ctx, account) {
  const state = getBridgeState();
  const url = state.client.sseUrl();
  const controller = new AbortController();
  let closed = false;

  void (async () => {
    const decoder = new TextDecoder();
    while (!closed && !ctx.abortSignal.aborted) {
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
        while (!closed && !ctx.abortSignal.aborted) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n").replace(/\r/g, "\n");
          buffer = drainSSEBuffer(buffer, (eventName, data) => {
            if (eventName === "agent.message") {
              handleAgentMessage(log, ctx, account, data);
            }
          });
        }
        reader.releaseLock?.();
      } catch (err) {
        if (closed || controller.signal.aborted || ctx.abortSignal.aborted) {
          return;
        }
        log.warn(`sky10: SSE connection lost: ${err?.message ?? err}; reconnecting in 5s`);
      }

      if (closed || controller.signal.aborted || ctx.abortSignal.aborted) {
        return;
      }
      await sleep(5_000);
    }
  })().catch((err) => {
    if (!closed && !controller.signal.aborted && !ctx.abortSignal.aborted) {
      log.error(`sky10: SSE loop crashed: ${err?.message ?? err}`);
    }
  });

  return () => {
    closed = true;
    controller.abort();
  };
}

async function startSky10GatewayAccount(ctx) {
  const log = ctx.log ?? console;
  const account = ctx.account;
  const state = getBridgeState();

  if (!account.configured) {
    throw new Error(`sky10 channel is not configured for account "${account.accountId}"`);
  }
  if (!state.pluginRuntime?.channel) {
    throw new Error("sky10 channel runtime is not initialized");
  }

  state.client = new Sky10Client(account.rpcUrl);
  await ensureRegistered(log, account, ctx.setStatus);

  const stopHeartbeat = startHeartbeat(log, account, ctx.setStatus, ctx.abortSignal);
  const stopListener = startListener(log, ctx, account);

  try {
    await waitForAbort(ctx.abortSignal);
  } finally {
    stopListener();
    stopHeartbeat();
    ctx.setStatus({
      accountId: account.accountId,
      running: false,
    });
  }
}

const sky10ChannelPlugin = createChatChannelPlugin({
  base: {
    id: CHANNEL_ID,
    meta: {
      id: CHANNEL_ID,
      label: CHANNEL_LABEL,
      selectionLabel: CHANNEL_LABEL,
      docsPath: "/channels/sky10",
      docsLabel: "sky10",
      blurb: "Direct sandbox bridge to the local sky10 daemon.",
      order: 999,
    },
    capabilities: {
      chatTypes: ["direct"],
    },
    reload: {
      configPrefixes: ["channels.sky10", "plugins.entries.sky10"],
    },
    configSchema: SKY10_CHANNEL_CONFIG_SCHEMA,
    setup: {
      applyAccountConfig: ({ cfg }) => cfg,
    },
    config: {
      listAccountIds: (cfg) => listSky10AccountIds(cfg),
      resolveAccount: (cfg, accountId) => resolveSky10Account({ cfg, accountId }),
      defaultAccountId: (cfg) => resolveDefaultSky10AccountId(cfg),
      isConfigured: (account) => account.configured,
    },
    gateway: {
      startAccount: async (ctx) => {
        await startSky10GatewayAccount(ctx);
      },
    },
  },
});

export default function register(api) {
  if (api.registrationMode === "cli-metadata") {
    return;
  }

  getBridgeState().pluginRuntime = api.runtime ?? getBridgeState().pluginRuntime;
  api.registerChannel({ plugin: sky10ChannelPlugin });
}
