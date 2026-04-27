#!/usr/bin/env bun
import { readFileSync } from "node:fs";

const ADAPTER_ID = "slack";
const ADAPTER_VERSION = "0.1.0";
const DEFAULT_API_BASE_URL = "https://slack.com/api";
const DEFAULT_CONVERSATION_TYPES = "public_channel,private_channel,im,mpim";
const DEFAULT_HISTORY_LIMIT = 15;

const connections = new Map();
let frameBuffer = Buffer.alloc(0);

process.stdin.on("data", (chunk) => {
  frameBuffer = Buffer.concat([frameBuffer, chunk]);
  for (;;) {
    const headerEnd = frameBuffer.indexOf("\r\n\r\n");
    if (headerEnd < 0) return;

    const header = frameBuffer.subarray(0, headerEnd).toString("utf8");
    const match = /Content-Length:\s*(\d+)/i.exec(header);
    if (!match) {
      throw new Error("missing Content-Length");
    }

    const length = Number(match[1]);
    const bodyStart = headerEnd + 4;
    const bodyEnd = bodyStart + length;
    if (frameBuffer.length < bodyEnd) return;

    const request = JSON.parse(frameBuffer.subarray(bodyStart, bodyEnd).toString("utf8"));
    frameBuffer = frameBuffer.subarray(bodyEnd);
    void handleRequest(request);
  }
});

process.stdin.on("end", () => {
  process.exit(0);
});

async function handleRequest(request) {
  try {
    const params = request.params || {};
    switch (request.method) {
      case "messaging.adapter.describe":
        return writeResult(request.id, describeResult());
      case "messaging.adapter.validateConfig":
        return writeResult(request.id, await validateConfig(params));
      case "messaging.adapter.connect":
        return writeResult(request.id, await connect(params));
      case "messaging.adapter.refresh":
        return writeResult(request.id, await connect({
          connection: params.connection,
          paths: params.paths || {},
          credential: params.credential
        }));
      case "messaging.adapter.listIdentities":
        return writeResult(request.id, listIdentities(params));
      case "messaging.adapter.searchIdentities":
        return writeResult(request.id, await searchIdentities(params));
      case "messaging.adapter.listConversations":
        return writeResult(request.id, await listConversations(params));
      case "messaging.adapter.searchConversations":
        return writeResult(request.id, await searchConversations(params));
      case "messaging.adapter.listMessages":
        return writeResult(request.id, await listMessages(params));
      case "messaging.adapter.getMessage":
        return writeResult(request.id, await getMessage(params));
      case "messaging.adapter.searchMessages":
        return writeResult(request.id, await searchMessages(params));
      case "messaging.adapter.createDraft":
      case "messaging.adapter.updateDraft":
        return writeResult(request.id, { draft: normalizeDraftRecord(params.draft) });
      case "messaging.adapter.deleteDraft":
        return writeResult(request.id, { deleted: true });
      case "messaging.adapter.sendMessage":
        return writeResult(request.id, await sendMessage(params, false));
      case "messaging.adapter.replyMessage":
        return writeResult(request.id, await sendMessage(params, true));
      case "messaging.adapter.health":
        return writeResult(request.id, health(params));
      case "messaging.adapter.poll":
      case "messaging.adapter.handleWebhook":
        return writeError(request.id, -32004, `${request.method} is not supported by the Slack adapter yet`);
      default:
        return writeError(request.id, -32601, "method not found");
    }
  } catch (error) {
    return writeError(request.id, error.rpcCode || -32000, error.message || String(error));
  }
}

function describeResult() {
  return {
    protocol: {
      name: "sky10.messaging.adapter",
      version: "v1alpha1",
      compatible_versions: ["v1alpha1"],
      transport: "stdio-jsonrpc"
    },
    adapter: {
      id: ADAPTER_ID,
      display_name: "Slack",
      version: ADAPTER_VERSION,
      description: "Sky10 external Slack messaging adapter",
      auth_methods: ["bot_token", "bearer_token", "oauth2"],
      capabilities: {
        receive_messages: true,
        send_messages: true,
        create_drafts: true,
        update_drafts: true,
        delete_drafts: true,
        list_conversations: true,
        list_messages: true,
        search_identities: true,
        search_conversations: true,
        search_messages: true,
        threading: true
      }
    }
  };
}

async function validateConfig(params) {
  const issues = [];
  try {
    const config = parseConfig(params.connection, params.credential);
    await slackAPI(config, "auth.test", {}, { method: "POST" });
  } catch (error) {
    issues.push({
      severity: "error",
      code: "invalid_config",
      message: error.message || String(error)
    });
  }
  return { issues };
}

async function connect(params) {
  const connection = requireConnection(params.connection);
  const config = parseConfig(connection, params.credential);
  const auth = await slackAPI(config, "auth.test", {}, { method: "POST" });
  const identity = identityFromAuth(connection.id, auth);
  const state = {
    config,
    identity,
    auth,
    conversations: new Map()
  };
  connections.set(connection.id, state);
  return {
    status: "connected",
    identities: [identity],
    metadata: cleanStringMap({
      slack_team_id: auth.team_id || config.teamID,
      slack_team: auth.team,
      slack_url: auth.url,
      slack_user_id: auth.user_id,
      slack_bot_id: auth.bot_id
    })
  };
}

function listIdentities(params) {
  const state = requireState(params.connection_id);
  return { identities: [state.identity] };
}

async function searchIdentities(params) {
  const state = requireState(params.connection_id);
  const query = normalizeQuery(params.query);
  const data = await slackAPI(state.config, "users.list", {
    cursor: params.cursor || "",
    limit: pageLimit(params.limit, 200)
  });
  const members = Array.isArray(data.members) ? data.members : [];
  const hits = members
    .filter((member) => userMatches(member, query))
    .map((member) => ({
      participant: participantFromUser(member, state.identity),
      matched_fields: userMatchedFields(member, query),
      source: "remote",
      metadata: cleanStringMap({
        slack_team_id: state.auth.team_id || state.config.teamID,
        slack_deleted: String(Boolean(member.deleted))
      })
    }));
  return {
    hits,
    count: hits.length,
    source: "remote",
    next_cursor: nextCursor(data)
  };
}

async function listConversations(params) {
  const state = requireState(params.connection_id);
  const data = await slackAPI(state.config, "conversations.list", {
    cursor: params.cursor || "",
    limit: pageLimit(params.limit, 200),
    exclude_archived: true,
    types: state.config.conversationTypes,
    team_id: state.config.teamID
  });
  const channels = Array.isArray(data.channels) ? data.channels : [];
  const conversations = channels.map((channel) => conversationFromChannel(state, channel));
  for (const conversation of conversations) {
    state.conversations.set(conversation.id, conversation);
  }
  return {
    conversations,
    next_cursor: nextCursor(data)
  };
}

async function searchConversations(params) {
  const state = requireState(params.connection_id);
  const query = normalizeQuery(params.query);
  const listed = await listConversations({
    connection_id: params.connection_id,
    cursor: params.cursor || "",
    limit: params.limit || 200
  });
  const hits = listed.conversations
    .filter((conversation) => conversationMatches(conversation, query))
    .map((conversation) => ({
      conversation,
      matched_fields: conversationMatchedFields(conversation, query),
      source: "remote",
      metadata: cleanStringMap({
        slack_remote_id: conversation.remote_id
      })
    }));
  return {
    hits,
    count: hits.length,
    source: "remote",
    next_cursor: listed.next_cursor || ""
  };
}

async function listMessages(params) {
  const state = requireState(params.connection_id);
  const conversationID = requireText(params.conversation_id, "conversation_id");
  const channelID = remoteChannelID(state, conversationID);
  const data = await slackAPI(state.config, "conversations.history", {
    channel: channelID,
    cursor: params.cursor || "",
    limit: historyLimit(state, params.limit)
  });
  const messages = (Array.isArray(data.messages) ? data.messages : [])
    .filter(isVisibleSlackMessage)
    .map((message) => slackMessageRecord(state, channelID, conversationID, message));
  return {
    messages,
    next_cursor: nextCursor(data)
  };
}

async function getMessage(params) {
  const state = requireState(params.connection_id);
  const conversationID = params.conversation_id || conversationIDFromMessageID(params.message_id);
  const channelID = remoteChannelID(state, conversationID);
  const ts = params.remote_id || remoteMessageTS(params.message_id);
  if (!ts) {
    throw rpcError(-32602, "remote_id or message_id is required");
  }
  const data = await slackAPI(state.config, "conversations.history", {
    channel: channelID,
    latest: ts,
    oldest: ts,
    inclusive: true,
    limit: 1
  });
  const message = (Array.isArray(data.messages) ? data.messages : []).find((item) => item.ts === ts) || data.messages?.[0];
  if (!message) {
    throw rpcError(-32004, `Slack message ${ts} was not found`);
  }
  return { message: slackMessageRecord(state, channelID, conversationID, message) };
}

async function searchMessages(params) {
  const state = requireState(params.connection_id);
  const query = requireText(params.query, "query");
  const data = await slackAPI(state.config, "search.messages", {
    query,
    count: pageLimit(params.limit, 20),
    page: cursorPage(params.cursor)
  });
  const matches = Array.isArray(data.messages?.matches) ? data.messages.matches : [];
  const hits = matches.map((match) => searchMessageHit(state, match));
  const paging = data.messages?.pagination || {};
  return {
    hits,
    count: hits.length,
    source: "remote",
    next_cursor: paging.page && paging.page < paging.page_count ? String(paging.page + 1) : ""
  };
}

async function sendMessage(params, reply) {
  const record = normalizeDraftRecord(params.draft);
  const draft = record.draft;
  const state = requireState(draft.connection_id);
  const channelID = remoteChannelID(state, draft.conversation_id);
  const text = draftText(draft);
  const payload = {
    channel: channelID,
    text
  };
  const replyTo = params.reply_to_remote_id || draft.reply_to_remote_id || draft.metadata?.slack_thread_ts;
  if (reply || replyTo) {
    payload.thread_ts = requireText(replyTo, "reply_to_remote_id");
  }
  if (params.idempotency_key) {
    payload.client_msg_id = params.idempotency_key;
  }

  const data = await slackAPI(state.config, "chat.postMessage", payload, { method: "POST" });
  const ts = data.ts || data.message?.ts;
  const message = {
    ts,
    text,
    user: state.identity.remote_id,
    bot_id: state.identity.metadata?.slack_bot_id,
    thread_ts: payload.thread_ts,
    __outbound: true
  };
  return {
    message: slackMessageRecord(state, channelID, draft.conversation_id, message),
    status: "sent"
  };
}

function health(params) {
  if (!params.connection_id) {
    return {
      health: {
        ok: true,
        status: "connected",
        message: "Slack adapter process is running",
        checked_at: new Date().toISOString()
      }
    };
  }
  const ok = connections.has(params.connection_id);
  return {
    health: {
      ok,
      status: ok ? "connected" : "auth_required",
      message: ok ? "Slack connection is active" : "Slack connection is not connected",
      checked_at: new Date().toISOString()
    }
  };
}

function parseConfig(connection, credential) {
  connection = requireConnection(connection);
  if (connection.adapter_id !== ADAPTER_ID) {
    throw rpcError(-32602, `connection adapter_id ${JSON.stringify(connection.adapter_id)} does not match slack`);
  }
  if (!["bot_token", "bearer_token", "oauth2", "session"].includes(connection.auth?.method || "")) {
    throw rpcError(-32602, `auth method ${JSON.stringify(connection.auth?.method || "")} is not supported by slack`);
  }
  const payload = readCredentialPayload(credential);
  const sessionToken = firstNonEmpty(
    payload.slack_session_token,
    payload.session_token,
    payload.xoxc_token
  );
  const sessionCookie = firstNonEmpty(
    payload.slack_session_cookie,
    payload.session_cookie,
    payload.xoxd_cookie,
    payload.cookie_d
  );
  const sessionCookieDS = firstNonEmpty(
    payload.slack_session_cookie_d_s,
    payload.session_cookie_d_s,
    payload.cookie_d_s,
    payload.xoxd_s_cookie
  );
  const botToken = firstNonEmpty(payload.bot_token, payload.access_token, payload.token, payload.slack_bot_token);
  let token;
  let cookie = "";
  if (sessionToken) {
    token = sessionToken;
    if (!sessionCookie) {
      throw rpcError(-32602, "Slack session token requires a paired session cookie (xoxd-...)");
    }
    const dPart = sessionCookie.startsWith("d=") ? sessionCookie : `d=${sessionCookie}`;
    if (sessionCookieDS) {
      const dsPart = sessionCookieDS.startsWith("d-s=") ? sessionCookieDS : `d-s=${sessionCookieDS}`;
      cookie = `${dPart}; ${dsPart}`;
    } else {
      cookie = dPart;
    }
  } else if (botToken) {
    token = botToken;
  } else {
    throw rpcError(-32602, "Slack token is required in credential payload (bot_token or slack_session_token)");
  }
  const metadata = connection.metadata || {};
  const historyLimitValue = Number(firstNonEmpty(metadata.slack_history_limit, payload.history_limit, String(DEFAULT_HISTORY_LIMIT)));
  return {
    connection,
    token,
    cookie,
    apiBaseURL: firstNonEmpty(metadata.slack_api_base_url, payload.api_base_url, DEFAULT_API_BASE_URL),
    teamID: firstNonEmpty(metadata.slack_team_id, payload.team_id, connection.auth?.tenant_id),
    conversationTypes: firstNonEmpty(metadata.slack_conversation_types, payload.conversation_types, DEFAULT_CONVERSATION_TYPES),
    historyLimit: Number.isFinite(historyLimitValue) && historyLimitValue > 0 ? Math.floor(historyLimitValue) : DEFAULT_HISTORY_LIMIT
  };
}

function readCredentialPayload(credential) {
  const localPath = credential?.blob?.local_path;
  if (!localPath) {
    throw rpcError(-32602, "credential blob local_path is required");
  }
  try {
    return JSON.parse(readFileSync(localPath, "utf8"));
  } catch (error) {
    throw rpcError(-32602, `read Slack credential payload: ${error.message || error}`);
  }
}

async function slackAPI(config, method, params = {}, options = {}) {
  const httpMethod = (options.method || "GET").toUpperCase();
  const url = new URL(`${trimTrailingSlash(config.apiBaseURL)}/${method}`);
  const headers = {
    Authorization: `Bearer ${config.token}`
  };
  if (config.cookie) {
    headers["Cookie"] = config.cookie;
  }
  const request = { method: httpMethod, headers };
  if (httpMethod === "GET") {
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== null && String(value) !== "") {
        url.searchParams.set(key, String(value));
      }
    }
  } else {
    headers["Content-Type"] = "application/json; charset=utf-8";
    request.body = JSON.stringify(params || {});
  }
  const response = await fetch(url, request);
  let data;
  try {
    data = await response.json();
  } catch (error) {
    throw rpcError(-32002, `Slack ${method} returned non-JSON response: ${response.status}`);
  }
  if (!response.ok || data.ok === false) {
    const detail = data.error || response.statusText || `http_${response.status}`;
    throw rpcError(slackErrorCode(detail), `Slack ${method} failed: ${detail}`);
  }
  return data;
}

function identityFromAuth(connectionID, auth) {
  const remoteID = auth.user_id || auth.bot_id || auth.user || "slack-bot";
  return {
    id: identityID(connectionID, remoteID),
    connection_id: connectionID,
    kind: "bot",
    remote_id: remoteID,
    address: auth.url || auth.team_id || remoteID,
    display_name: firstNonEmpty(auth.user, auth.bot_id, "Slack Bot"),
    can_receive: true,
    can_send: true,
    is_default: true,
    metadata: cleanStringMap({
      slack_team_id: auth.team_id,
      slack_team: auth.team,
      slack_url: auth.url,
      slack_user_id: auth.user_id,
      slack_bot_id: auth.bot_id
    })
  };
}

function conversationFromChannel(state, channel) {
  const id = conversationID(state.config.connection.id, channel.id);
  const title = firstNonEmpty(channel.name_normalized, channel.name, channel.user, channel.id);
  return {
    id,
    connection_id: state.config.connection.id,
    local_identity_id: state.identity.id,
    kind: conversationKind(channel),
    remote_id: channel.id,
    title,
    participants: participantsFromChannel(channel),
    metadata: cleanStringMap({
      slack_channel_id: channel.id,
      slack_channel_name: channel.name || channel.name_normalized,
      slack_conversation_type: slackConversationType(channel),
      slack_is_archived: String(Boolean(channel.is_archived)),
      slack_is_member: String(Boolean(channel.is_member)),
      slack_num_members: channel.num_members === undefined ? "" : String(channel.num_members)
    })
  };
}

function participantsFromChannel(channel) {
  if (channel.is_im && channel.user) {
    return [{
      kind: "user",
      remote_id: channel.user,
      display_name: channel.user
    }];
  }
  return [];
}

function participantFromUser(user, localIdentity) {
  const profile = user.profile || {};
  const remoteID = user.id || user.name || user.real_name || "unknown";
  const displayName = firstNonEmpty(profile.display_name, profile.real_name, user.real_name, user.name, remoteID);
  return {
    id: remoteID,
    kind: user.is_bot ? "bot" : "user",
    remote_id: remoteID,
    address: profile.email || "",
    display_name: displayName,
    identity_id: localIdentity?.remote_id === remoteID ? localIdentity.id : "",
    is_local: localIdentity?.remote_id === remoteID,
    metadata: cleanStringMap({
      slack_user_id: remoteID,
      slack_team_id: user.team_id,
      slack_deleted: String(Boolean(user.deleted))
    })
  };
}

function slackMessageRecord(state, channelID, conversationIDValue, slackMessage) {
  const ts = slackMessage.ts || new Date().getTime().toString();
  const text = firstNonEmpty(slackMessage.text, slackMessage.__outbound ? "" : "[Slack message]");
  const senderRemoteID = slackMessage.user || slackMessage.bot_id || slackMessage.username || "slack";
  const isLocal = slackMessage.__outbound || senderRemoteID === state.identity.remote_id || senderRemoteID === state.identity.metadata?.slack_bot_id;
  return {
    message: {
      id: messageID(state.config.connection.id, channelID, ts),
      connection_id: state.config.connection.id,
      conversation_id: conversationIDValue,
      local_identity_id: state.identity.id,
      remote_id: ts,
      direction: isLocal ? "outbound" : "inbound",
      sender: {
        kind: slackMessage.bot_id ? "bot" : "user",
        remote_id: senderRemoteID,
        display_name: slackMessage.username || senderRemoteID,
        identity_id: isLocal ? state.identity.id : "",
        is_local: isLocal,
        metadata: cleanStringMap({
          slack_user_id: slackMessage.user,
          slack_bot_id: slackMessage.bot_id
        })
      },
      parts: [{
        kind: "text",
        content_type: "text/plain",
        text: text || "[Slack message]"
      }],
      created_at: slackTimestampToISO(ts),
      reply_to_remote_id: slackMessage.thread_ts && slackMessage.thread_ts !== ts ? slackMessage.thread_ts : "",
      status: isLocal ? "sent" : "received",
      metadata: cleanStringMap({
        slack_channel_id: channelID,
        slack_ts: ts,
        slack_thread_ts: slackMessage.thread_ts,
        slack_subtype: slackMessage.subtype,
        slack_type: slackMessage.type
      })
    }
  };
}

function searchMessageHit(state, match) {
  const channel = match.channel || {};
  const channelID = channel.id || match.channel_id || match.channel || "";
  const conversationIDValue = conversationID(state.config.connection.id, channelID);
  const messageRecord = slackMessageRecord(state, channelID, conversationIDValue, {
    type: "message",
    user: match.user,
    username: match.username,
    text: match.text,
    ts: match.ts
  });
  return {
    message: messageRecord,
    conversation: {
      id: conversationIDValue,
      connection_id: state.config.connection.id,
      local_identity_id: state.identity.id,
      kind: "channel",
      remote_id: channelID,
      title: firstNonEmpty(channel.name, channelID),
      metadata: cleanStringMap({
        slack_channel_id: channelID
      })
    },
    matched_fields: ["text"],
    source: "remote",
    metadata: cleanStringMap({
      slack_permalink: match.permalink
    })
  };
}

function normalizeDraftRecord(record) {
  if (!record || !record.draft) {
    throw rpcError(-32602, "draft record is required");
  }
  return record;
}

function requireConnection(connection) {
  if (!connection || !connection.id) {
    throw rpcError(-32602, "connection is required");
  }
  return connection;
}

function requireState(connectionID) {
  connectionID = requireText(connectionID, "connection_id");
  const state = connections.get(connectionID);
  if (!state) {
    throw rpcError(-32004, `connection ${connectionID} is not connected`);
  }
  return state;
}

function requireText(value, field) {
  value = String(value || "").trim();
  if (!value) {
    throw rpcError(-32602, `${field} is required`);
  }
  return value;
}

function remoteChannelID(state, conversationIDValue) {
  conversationIDValue = requireText(conversationIDValue, "conversation_id");
  const known = state.conversations.get(conversationIDValue);
  if (known?.remote_id) return known.remote_id;
  const marker = "/conversation/";
  const idx = conversationIDValue.lastIndexOf(marker);
  if (idx >= 0) return conversationIDValue.slice(idx + marker.length);
  return conversationIDValue;
}

function remoteMessageTS(messageIDValue) {
  if (!messageIDValue) return "";
  const marker = "/message/";
  const idx = String(messageIDValue).lastIndexOf(marker);
  if (idx < 0) return "";
  const parts = String(messageIDValue).slice(idx + marker.length).split("/");
  return parts.length > 1 ? parts.slice(1).join("/") : "";
}

function conversationIDFromMessageID(messageIDValue) {
  if (!messageIDValue) return "";
  const marker = "/message/";
  const idx = String(messageIDValue).lastIndexOf(marker);
  if (idx < 0) return "";
  const base = String(messageIDValue).slice(0, idx);
  const parts = String(messageIDValue).slice(idx + marker.length).split("/");
  const channelID = parts[0] || "";
  return channelID ? `${base}/conversation/${channelID}` : "";
}

function conversationKind(channel) {
  if (channel.is_im) return "direct";
  if (channel.is_mpim) return "group";
  return "channel";
}

function slackConversationType(channel) {
  if (channel.is_im) return "im";
  if (channel.is_mpim) return "mpim";
  if (channel.is_private || channel.is_group) return "private_channel";
  return "public_channel";
}

function isVisibleSlackMessage(message) {
  return message && message.type !== "message_deleted" && !message.hidden;
}

function userMatches(user, query) {
  if (!query) return true;
  return searchableUserFields(user).some((value) => value.includes(query));
}

function userMatchedFields(user, query) {
  if (!query) return [];
  const fields = [];
  const profile = user.profile || {};
  if (String(user.id || "").toLowerCase().includes(query)) fields.push("id");
  if (String(user.name || "").toLowerCase().includes(query)) fields.push("name");
  if (String(user.real_name || "").toLowerCase().includes(query)) fields.push("real_name");
  if (String(profile.display_name || "").toLowerCase().includes(query)) fields.push("display_name");
  if (String(profile.email || "").toLowerCase().includes(query)) fields.push("email");
  return fields;
}

function searchableUserFields(user) {
  const profile = user.profile || {};
  return [user.id, user.name, user.real_name, profile.display_name, profile.real_name, profile.email]
    .map((value) => String(value || "").toLowerCase());
}

function conversationMatches(conversation, query) {
  if (!query) return true;
  return [conversation.id, conversation.remote_id, conversation.title, conversation.metadata?.slack_channel_name]
    .some((value) => String(value || "").toLowerCase().includes(query));
}

function conversationMatchedFields(conversation, query) {
  if (!query) return [];
  const fields = [];
  if (String(conversation.remote_id || "").toLowerCase().includes(query)) fields.push("remote_id");
  if (String(conversation.title || "").toLowerCase().includes(query)) fields.push("title");
  if (String(conversation.metadata?.slack_channel_name || "").toLowerCase().includes(query)) fields.push("name");
  return fields;
}

function draftText(draft) {
  const text = (draft.parts || [])
    .filter((part) => ["text", "markdown", "html"].includes(part.kind))
    .map((part) => part.text || "")
    .join("\n")
    .trim();
  return requireText(text, "draft text");
}

function identityID(connectionID, remoteID) {
  return `${connectionID}/identity/${remoteID}`;
}

function conversationID(connectionID, channelID) {
  return `${connectionID}/conversation/${channelID}`;
}

function messageID(connectionID, channelID, ts) {
  return `${connectionID}/message/${channelID}/${ts}`;
}

function historyLimit(state, requested) {
  return Math.max(1, Math.min(pageLimit(requested, state.config.historyLimit), state.config.historyLimit));
}

function pageLimit(requested, fallback) {
  const value = Number(requested || fallback);
  if (!Number.isFinite(value) || value <= 0) return fallback;
  return Math.floor(value);
}

function cursorPage(cursor) {
  const value = Number(cursor || 1);
  return Number.isFinite(value) && value > 0 ? Math.floor(value) : 1;
}

function nextCursor(data) {
  return data?.response_metadata?.next_cursor || "";
}

function normalizeQuery(query) {
  return String(query || "").trim().toLowerCase();
}

function slackTimestampToISO(ts) {
  const seconds = Number(ts);
  if (!Number.isFinite(seconds)) return new Date().toISOString();
  return new Date(seconds * 1000).toISOString();
}

function cleanStringMap(values) {
  const out = {};
  for (const [key, value] of Object.entries(values || {})) {
    if (value === undefined || value === null || String(value) === "") continue;
    out[key] = String(value);
  }
  return out;
}

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = String(value || "").trim();
    if (text) return text;
  }
  return "";
}

function trimTrailingSlash(value) {
  return String(value || DEFAULT_API_BASE_URL).replace(/\/+$/, "");
}

function slackErrorCode(detail) {
  switch (detail) {
    case "not_authed":
    case "invalid_auth":
    case "token_expired":
    case "token_revoked":
      return -32001;
    case "ratelimited":
    case "rate_limited":
      return -32003;
    default:
      return -32002;
  }
}

function rpcError(code, message) {
  const error = new Error(message);
  error.rpcCode = code;
  return error;
}

function writeResult(id, result) {
  writeFrame({
    jsonrpc: "2.0",
    id,
    result
  });
}

function writeError(id, code, message) {
  writeFrame({
    jsonrpc: "2.0",
    id,
    error: {
      code,
      message
    }
  });
}

function writeFrame(payload) {
  const body = JSON.stringify(payload);
  process.stdout.write(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
}
