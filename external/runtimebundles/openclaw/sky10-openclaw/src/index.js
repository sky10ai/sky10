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
import { createHash, randomUUID } from "node:crypto";
import { pathToFileURL } from "node:url";

import { createChatChannelPlugin } from "/usr/lib/node_modules/openclaw/dist/plugin-sdk/core.js";
import { createChannelReplyPipeline } from "/usr/lib/node_modules/openclaw/dist/plugin-sdk/channel-reply-pipeline.js";

import { Sky10Client } from "./sky10.js";
import { buildOutboundChatContent, extractInboundMediaContext } from "./media.js";
import { createX402Client, deriveX402WsUrl, formatX402PromptContext, installX402Helper } from "./x402.js";

const CHANNEL_ID = "sky10";
const CHANNEL_LABEL = "Sky10";
const DEFAULT_ACCOUNT_ID = "default";
const DEFAULT_SKILLS = ["code", "shell", "browser", "web-search", "file-ops"];
const DEFAULT_MANIFEST_PATH = "/shared/agent-manifest.json";
const DEFAULT_X402_HELPER_PATH = path.join(os.homedir(), ".openclaw", "sky10-x402.mjs");
const X402_CONTEXT_TTL_MS = 30_000;
const GLOBAL_STATE_KEY = Symbol.for("sky10.openclaw.bridge");
const DEDUP_TTL_MS = 30_000;
const CLAIM_PRUNE_INTERVAL_MS = 60_000;
const CLAIM_DIR = path.join(os.homedir(), ".openclaw", ".sky10-bridge-seen");
const DEFAULT_JOB_OUTPUT_ROOT = "/shared/jobs";
const SKY10_ACCOUNT_PROPERTIES = {
  enabled: { type: "boolean" },
  rpcUrl: { type: "string" },
  agentName: { type: "string" },
  skills: {
    type: "array",
    items: { type: "string" },
  },
  tools: {
    type: "array",
    items: { type: "object" },
  },
  manifestPath: { type: "string" },
  gatewayToken: { type: "string" },
  x402WsUrl: { type: "string" },
  x402HelperPath: { type: "string" },
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
      tools: {
        type: "array",
        items: { type: "object" },
      },
      manifestPath: { type: "string" },
      gatewayToken: { type: "string" },
      x402WsUrl: { type: "string" },
      x402HelperPath: { type: "string" },
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
      x402: null,
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

function normalizeTools(tools) {
  if (!Array.isArray(tools)) {
    return [];
  }
  return tools
    .filter((tool) => tool && typeof tool === "object")
    .map((tool) => ({
      ...tool,
      name: typeof tool.name === "string" ? tool.name.trim() : "",
      capability: typeof tool.capability === "string" ? tool.capability.trim() : "",
    }))
    .filter((tool) => tool.name);
}

function skillsFromTools(tools) {
  const skills = [];
  const seen = new Set();
  for (const tool of tools) {
    for (const value of [tool.capability, tool.name]) {
      const skill = typeof value === "string" ? value.trim() : "";
      if (!skill || seen.has(skill)) continue;
      seen.add(skill);
      skills.push(skill);
    }
  }
  return skills;
}

function resolveSkills(skills, tools) {
  if (Array.isArray(skills)) {
    const normalized = skills.map((value) => String(value).trim()).filter(Boolean);
    if (normalized.length > 0) {
      return normalized;
    }
  }
  const toolSkills = skillsFromTools(tools);
  return toolSkills.length > 0 ? toolSkills : [...DEFAULT_SKILLS];
}

function readAgentManifest(manifestPath) {
  const resolvedPath = typeof manifestPath === "string" && manifestPath.trim()
    ? manifestPath.trim()
    : DEFAULT_MANIFEST_PATH;
  try {
    if (!fs.existsSync(resolvedPath)) {
      return {};
    }
    return JSON.parse(fs.readFileSync(resolvedPath, "utf8"));
  } catch {
    return {};
  }
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
  const manifestPath = typeof merged.manifestPath === "string" && merged.manifestPath.trim()
    ? merged.manifestPath.trim()
    : DEFAULT_MANIFEST_PATH;
  const manifest = readAgentManifest(manifestPath);
  const tools = normalizeTools(merged.tools ?? manifest.tools);
  const rpcUrl = typeof merged.rpcUrl === "string" && merged.rpcUrl.trim()
    ? merged.rpcUrl.trim()
    : "http://localhost:9101";
  const agentName = typeof merged.agentName === "string" && merged.agentName.trim()
    ? merged.agentName.trim()
    : typeof manifest.name === "string" && manifest.name.trim()
      ? manifest.name.trim()
      : "openclaw";
  return {
    accountId: resolvedAccountId,
    name: agentName,
    enabled: section.enabled !== false && merged.enabled !== false,
    configured: Boolean(rpcUrl),
    rpcUrl,
    agentName,
    skills: resolveSkills(merged.skills, tools),
    tools,
    prompt: typeof manifest.prompt === "string" ? manifest.prompt.trim() : "",
    description: typeof manifest.description === "string" ? manifest.description.trim() : "",
    manifest,
    manifestPath,
    gatewayToken: typeof merged.gatewayToken === "string" ? merged.gatewayToken.trim() : "",
    x402WsUrl: typeof merged.x402WsUrl === "string" ? merged.x402WsUrl.trim() : "",
    x402HelperPath: typeof merged.x402HelperPath === "string" && merged.x402HelperPath.trim()
      ? merged.x402HelperPath.trim()
      : DEFAULT_X402_HELPER_PATH,
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

function extractClientRequestID(content) {
  if (!content || typeof content !== "object") {
    return "";
  }
  return typeof content.client_request_id === "string" ? content.client_request_id.trim() : "";
}

function resolveReplyText(payload) {
  if (typeof payload === "string") {
    return payload;
  }
  if (payload && typeof payload === "object" && typeof payload.text === "string") {
    return payload.text;
  }
  return "";
}

function resolveIncrementalReplyText(nextText, previousText) {
  if (!nextText) {
    return "";
  }
  if (!previousText) {
    return nextText;
  }
  if (nextText.startsWith(previousText)) {
    return nextText.slice(previousText.length);
  }
  return nextText;
}

function buildStreamContent(text, streamId, clientRequestID) {
  const content = {
    text,
    stream_id: streamId,
  };
  if (clientRequestID) {
    content.client_request_id = clientRequestID;
  }
  return content;
}

function resolveJobOutputDir(content, jobId) {
  const payload = content && typeof content === "object" ? content : {};
  const jobContext = payload.job_context && typeof payload.job_context === "object" ? payload.job_context : {};
  const configured = typeof jobContext.output_dir === "string" ? jobContext.output_dir.trim() : "";
  if (configured) {
    return configured;
  }
  const root = typeof process.env.SKY10_JOB_OUTPUT_ROOT === "string" && process.env.SKY10_JOB_OUTPUT_ROOT.trim()
    ? process.env.SKY10_JOB_OUTPUT_ROOT.trim()
    : DEFAULT_JOB_OUTPUT_ROOT;
  return path.join(root, jobId, "outputs");
}

function fileDigest(filePath) {
  return new Promise((resolve, reject) => {
    const hash = createHash("sha256");
    const stream = fs.createReadStream(filePath);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("error", reject);
    stream.on("end", () => resolve(`sha256:${hash.digest("hex")}`));
  });
}

function mimeTypeForPath(filePath) {
  const lower = filePath.toLowerCase();
  if (lower.endsWith(".mp4")) return "video/mp4";
  if (lower.endsWith(".mov")) return "video/quicktime";
  if (lower.endsWith(".webm")) return "video/webm";
  if (lower.endsWith(".mp3")) return "audio/mpeg";
  if (lower.endsWith(".wav")) return "audio/wav";
  if (lower.endsWith(".m4a")) return "audio/mp4";
  if (lower.endsWith(".srt")) return "application/x-subrip";
  if (lower.endsWith(".vtt")) return "text/vtt";
  if (lower.endsWith(".txt")) return "text/plain";
  if (lower.endsWith(".json")) return "application/json";
  return "application/octet-stream";
}

async function collectOutputRefs(outputDir) {
  if (!fs.existsSync(outputDir)) {
    return [];
  }
  const refs = [];
  const visit = async (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const fullPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        await visit(fullPath);
        continue;
      }
      if (!entry.isFile()) {
        continue;
      }
      const stat = fs.statSync(fullPath);
      refs.push({
        kind: "file",
        key: path.relative(outputDir, fullPath).split(path.sep).join("/"),
        uri: pathToFileURL(fullPath).href,
        mime_type: mimeTypeForPath(fullPath),
        size: stat.size,
        digest: await fileDigest(fullPath),
      });
    }
  };
  await visit(outputDir);
  refs.sort((left, right) => left.key.localeCompare(right.key));
  return refs;
}

function buildAgentContractPrompt(manifest) {
  const spec = manifest && typeof manifest === "object" ? manifest : {};
  const lines = [];
  if (typeof spec.prompt === "string" && spec.prompt.trim()) {
    lines.push("Original user prompt:");
    lines.push(spec.prompt.trim());
    lines.push("");
  }
  if (typeof spec.description === "string" && spec.description.trim()) {
    lines.push(`Agent purpose: ${spec.description.trim()}`);
  }
  if (Array.isArray(spec.tools) && spec.tools.length > 0) {
    lines.push("Exported tools:");
    for (const tool of spec.tools) {
      if (!tool || typeof tool !== "object") continue;
      const name = typeof tool.name === "string" ? tool.name.trim() : "";
      const capability = typeof tool.capability === "string" ? tool.capability.trim() : "";
      const description = typeof tool.description === "string" ? tool.description.trim() : "";
      if (name || capability || description) {
        lines.push(`- ${name || capability}${capability && capability !== name ? ` (${capability})` : ""}: ${description}`);
      }
    }
  }
  if (Array.isArray(spec.inputs) && spec.inputs.length > 0) {
    lines.push("Expected inputs:");
    for (const input of spec.inputs) {
      if (!input || typeof input !== "object") continue;
      lines.push(`- ${input.kind || "input"}: ${input.description || ""}`);
    }
  }
  if (Array.isArray(spec.outputs) && spec.outputs.length > 0) {
    lines.push("Expected outputs:");
    for (const output of spec.outputs) {
      if (!output || typeof output !== "object") continue;
      lines.push(`- ${output.kind || "artifact"}: ${output.description || ""}`);
    }
  }
  if (Array.isArray(spec.secret_bindings) && spec.secret_bindings.length > 0) {
    lines.push("Available secret bindings:");
    for (const binding of spec.secret_bindings) {
      if (!binding || typeof binding !== "object") continue;
      lines.push(`- ${binding.env || ""}${binding.required ? " (required)" : " (optional)"}`);
    }
  }
  return lines.join("\n").trim();
}

function buildToolCallPrompt(content, outputDir, account = {}, x402Context) {
  const payload = content && typeof content === "object" ? content : {};
  const jobContext = payload.job_context && typeof payload.job_context === "object" ? payload.job_context : {};
  const payloadRefs = [];
  if (payload.payload_ref && typeof payload.payload_ref === "object") {
    payloadRefs.push(payload.payload_ref);
  }
  if (Array.isArray(payload.payload_refs)) {
    payloadRefs.push(...payload.payload_refs);
  }
  const toolCall = {
    job_id: typeof payload.job_id === "string" && payload.job_id.trim() ? payload.job_id.trim() : String(jobContext.job_id ?? ""),
    tool: typeof payload.tool === "string" && payload.tool.trim() ? payload.tool.trim() : "tool",
    capability: typeof payload.capability === "string" ? payload.capability.trim() : "",
    input: Object.prototype.hasOwnProperty.call(payload, "input") ? payload.input : {},
    payload_refs: payloadRefs,
    output_dir: outputDir,
    job_context: jobContext,
    budget: payload.budget,
    bid_id: payload.bid_id,
  };
  const manifest = account?.manifest && typeof account.manifest === "object"
    ? account.manifest
    : {
        prompt: account?.prompt,
        description: account?.description,
        tools: account?.tools,
      };
  const contractPrompt = buildAgentContractPrompt(manifest);
  const lines = [
    contractPrompt ? "You are this sky10 agent. Follow the agent contract below." : "You are a sky10 durable agent.",
  ];
  if (contractPrompt) {
    lines.push(contractPrompt, "");
  }
  lines.push(
    "You are fulfilling a sky10 durable agent tool call.",
    "Complete the requested tool call autonomously using the tools and credentials available in this VM.",
    "Infer the workflow yourself from the original prompt, exported tool contract, input payloads, available files, installed packages, and configured provider secrets.",
    `Treat payload_refs as input handles and write generated artifacts under this directory: ${outputDir}`,
    "Use job_context.update_methods when you need to report progress, completion, or failure through sky10.",
    "Respect any budget, pricing, or payment context attached to the tool call.",
    "Return a concise result summary and include any output artifact paths or URIs.",
  );
  const x402Prompt = formatX402PromptContext(x402Context);
  if (x402Prompt) {
    lines.push("", x402Prompt);
  }
  lines.push(
    "",
    "Tool call:",
    JSON.stringify(toolCall, null, 2),
  );
  return lines.join("\n");
}

function resolveToolCallJobID(content, msg) {
  const payload = content && typeof content === "object" ? content : {};
  const jobContext = payload.job_context && typeof payload.job_context === "object" ? payload.job_context : {};
  return String(payload.job_id || jobContext.job_id || msg.session_id || "").trim();
}

function resolveToolCallName(content) {
  const payload = content && typeof content === "object" ? content : {};
  return String(payload.tool || payload.capability || "tool").trim() || "tool";
}

async function ensureRegistered(log, account, setStatus) {
  const state = getBridgeState();
  state.client ??= new Sky10Client(account.rpcUrl);
  const reg = await state.client.register(account.agentName, account.skills, account.tools);
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
  await refreshX402RuntimeContext(log, account, { force: true });
}

async function refreshX402RuntimeContext(log, account, opts = {}) {
  const state = getBridgeState();
  const now = Date.now();
  if (!opts.force && state.x402 && now - state.x402.refreshedAt < X402_CONTEXT_TTL_MS) {
    return state.x402;
  }

  const next = {
    wsUrl: "",
    helperPath: account.x402HelperPath || DEFAULT_X402_HELPER_PATH,
    services: [],
    error: "",
    refreshedAt: now,
  };

  try {
    next.wsUrl = deriveX402WsUrl({
      wsUrl: account.x402WsUrl,
      rpcUrl: account.rpcUrl,
      agentName: account.agentName,
    });
    next.helperPath = installX402Helper({
      helperPath: account.x402HelperPath,
      wsUrl: next.wsUrl,
      rpcUrl: account.rpcUrl,
      agentName: account.agentName,
    });
    const client = createX402Client({ wsUrl: next.wsUrl, timeoutMs: 5_000 });
    const listed = await client.listServices();
    next.services = Array.isArray(listed?.services) ? listed.services : [];
    log.info(`sky10: x402 approved services available: ${next.services.length}`);
  } catch (err) {
    next.error = err?.message ?? String(err);
    log.warn(`sky10: x402 service discovery unavailable: ${next.error}`);
  }

  state.x402 = next;
  return next;
}

function resolveInboundRouteEnvelope(runtime, cfg, accountId, peer, conversationLabel, rawBody, timestamp) {
  const route = runtime.channel.routing.resolveAgentRoute({
    cfg,
    channel: CHANNEL_ID,
    accountId,
    peer,
  });
  const storePath = runtime.channel.session.resolveStorePath(cfg.session?.store, { agentId: route.agentId });
  const previousTimestamp = runtime.channel.session.readSessionUpdatedAt({
    storePath,
    sessionKey: route.sessionKey,
  });
  const envelope = runtime.channel.reply.resolveEnvelopeFormatOptions(cfg);
  const body = runtime.channel.reply.formatAgentEnvelope({
    channel: CHANNEL_LABEL,
    from: conversationLabel,
    timestamp,
    previousTimestamp,
    envelope,
    body: rawBody,
  });
  return { route, storePath, body };
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

async function dispatchInbound(log, ctx, account, msg, inbound, handlers = {}) {
  const state = getBridgeState();
  const runtime = state.pluginRuntime;
  if (!runtime?.channel) {
    throw new Error("sky10 runtime not initialized");
  }

  const peer = {
    kind: "direct",
    id: resolveSessionPeerId(msg),
  };
  const sessionId = msg.session_id || "main";
  const conversationLabel = `${msg.from} (${sessionId})`;
  const messageId = resolveMessageId(msg);
  const timestamp = resolveMessageTimestamp(msg);
  const rawBody = inbound.bodyText || "";
  const { route, storePath, body } = resolveInboundRouteEnvelope(
    runtime,
    ctx.cfg,
    account.accountId,
    peer,
    conversationLabel,
    rawBody,
    timestamp,
  );
  const ctxPayload = runtime.channel.reply.finalizeInboundContext({
    Body: body,
    BodyForAgent: rawBody,
    RawBody: rawBody,
    CommandBody: rawBody,
    From: `sky10:${msg.from}`,
    To: `sky10:${state.agentId ?? account.agentName}`,
    SessionKey: route.sessionKey,
    AccountId: route.accountId ?? account.accountId,
    ChatType: "direct",
    ConversationLabel: conversationLabel,
    SenderId: msg.from,
    Provider: CHANNEL_ID,
    Surface: CHANNEL_ID,
    MessageSid: messageId,
    MessageSidFull: messageId,
    Timestamp: timestamp,
    CommandAuthorized: true,
    OriginatingChannel: CHANNEL_ID,
    OriginatingTo: `sky10:${state.agentId ?? account.agentName}`,
    Sky10SessionId: sessionId,
    Sky10SenderId: msg.from,
    ...(inbound.mediaPath ? { MediaPath: inbound.mediaPath } : {}),
    ...(inbound.mediaUrl ? { MediaUrl: inbound.mediaUrl } : {}),
    ...(inbound.mediaType ? { MediaType: inbound.mediaType } : {}),
    ...(inbound.mediaPaths.length > 0 ? { MediaPaths: inbound.mediaPaths } : {}),
    ...(inbound.mediaUrls.length > 0 ? { MediaUrls: inbound.mediaUrls } : {}),
    ...(inbound.mediaTypes.length > 0 ? { MediaTypes: inbound.mediaTypes } : {}),
  });
  await runtime.channel.session.recordInboundSession({
    storePath,
    sessionKey: ctxPayload.SessionKey ?? route.sessionKey,
    ctx: ctxPayload,
    onRecordError: (err) => {
      log.error(`sky10: failed recording inbound session: ${err?.message ?? err}`);
    },
  });

  const clientRequestID = extractClientRequestID(msg.content);
  const streamId = randomUUID();
  let replyError = null;
  let finalDelivered = false;
  const partialReplyState = {
    lastText: "",
  };
  const { onModelSelected, ...replyPipeline } = createChannelReplyPipeline({
    cfg: ctx.cfg,
    agentId: route.agentId,
    channel: CHANNEL_ID,
    accountId: route.accountId ?? account.accountId,
  });
  await runtime.channel.reply.dispatchReplyWithBufferedBlockDispatcher({
    ctx: ctxPayload,
    cfg: ctx.cfg,
    dispatcherOptions: {
      ...replyPipeline,
      deliver: async (payload, meta) => {
        const kind = meta?.kind ?? "final";
        const replyText = resolveReplyText(payload);
        if (kind === "block") {
          if (handlers.onBlockReply) {
            await handlers.onBlockReply({
              payload,
              meta,
              replyText,
              streamId,
              clientRequestID,
              sessionId,
            });
            return;
          }
          if (partialReplyState.lastText) {
            return;
          }
          await state.client.sendDelta(msg.from, sessionId, replyText, msg.from, streamId, clientRequestID);
          return;
        }
        if (kind !== "final" || !replyText.trim()) {
          return;
        }
        const replyContent = buildOutboundChatContent(replyText);
        if (handlers.onFinalReply) {
          await handlers.onFinalReply({
            payload,
            meta,
            replyText,
            replyContent,
            streamId,
            clientRequestID,
            sessionId,
          });
          finalDelivered = true;
          return;
        }
        const hasMedia = replyContent.parts.some((part) => part.type !== "text");
        await state.client.sendContent(
          msg.from,
          sessionId,
          hasMedia
            ? {
                ...replyContent,
                stream_id: streamId,
                client_request_id: clientRequestID || undefined,
              }
            : buildStreamContent(replyText, streamId, clientRequestID),
          msg.from,
          hasMedia ? "chat" : "text",
        );
        finalDelivered = true;
        log.info("sky10: reply sent");
      },
      onError: (err, info) => {
        log.error(`sky10: ${info.kind} reply failed: ${err?.message ?? err}`);
        replyError ??= err ?? new Error(`${info.kind} reply failed`);
      },
    },
    replyOptions: {
      onModelSelected,
      onAssistantMessageStart: () => {
        partialReplyState.lastText = "";
      },
      onPartialReply: async (payload) => {
        const nextText = resolveReplyText(payload);
        const deltaText = resolveIncrementalReplyText(nextText, partialReplyState.lastText);
        partialReplyState.lastText = nextText;
        if (!deltaText) {
          return;
        }
        if (handlers.onPartialReply) {
          await handlers.onPartialReply({
            payload,
            nextText,
            deltaText,
            streamId,
            clientRequestID,
            sessionId,
          });
          return;
        }
        await state.client.sendDelta(msg.from, sessionId, deltaText, msg.from, streamId, clientRequestID);
      },
    },
  });
  if (handlers.failOnError && replyError && !finalDelivered) {
    throw replyError;
  }
}

async function dispatchToolCall(log, ctx, account, msg) {
  const state = getBridgeState();
  const content = msg.content && typeof msg.content === "object" ? msg.content : {};
  const jobId = resolveToolCallJobID(content, msg);
  if (!jobId) {
    log.error("sky10: tool_call missing job_id");
    return;
  }
  const tool = resolveToolCallName(content);
  const outputDir = resolveJobOutputDir(content, jobId);
  try {
    fs.mkdirSync(outputDir, { recursive: true });
    await state.client.updateJobStatus(jobId, "running", `Running ${tool}`);
    const x402Context = await refreshX402RuntimeContext(log, account);
    const toolMsg = {
      ...msg,
      session_id: jobId,
      content: {
        text: buildToolCallPrompt(content, outputDir, account, x402Context),
      },
    };
    const inbound = extractInboundMediaContext(toolMsg.content, jobId);
    await dispatchInbound(log, ctx, account, toolMsg, inbound, {
      failOnError: true,
      onPartialReply: async () => {},
      onBlockReply: async () => {},
      onFinalReply: async ({ replyText }) => {
        const artifacts = await collectOutputRefs(outputDir);
        await state.client.completeJob(
          jobId,
          {
            summary: replyText,
            text: replyText,
            artifacts,
          },
          artifacts,
          "Tool call completed.",
        );
        log.info(`sky10: tool_call completed for job ${jobId}`);
      },
    });
  } catch (err) {
    const message = String(err?.message ?? err);
    log.error(`sky10: tool_call failed for job ${jobId}: ${message}`);
    try {
      await state.client.failJob(jobId, "runtime_error", message);
    } catch (failErr) {
      log.error(`sky10: failed marking job ${jobId} failed: ${failErr?.message ?? failErr}`);
    }
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

    if (String(msg.type || "").trim() === "tool_call") {
      void dispatchToolCall(log, ctx, account, msg).catch((err) => {
        log.error(`sky10: tool_call dispatch failed: ${err?.message ?? err}`);
      });
      return;
    }

    const inbound = extractInboundMediaContext(msg.content, msg.session_id || "main");
    void dispatchInbound(log, ctx, account, msg, inbound).catch((err) => {
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
