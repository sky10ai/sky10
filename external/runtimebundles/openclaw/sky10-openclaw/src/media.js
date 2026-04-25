import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";

function stagedMediaRoot() {
  const override = typeof process.env.OPENCLAW_SKY10_MEDIA_DIR === "string"
    ? process.env.OPENCLAW_SKY10_MEDIA_DIR.trim()
    : "";
  if (override) {
    return override;
  }
  return path.join(os.homedir(), ".openclaw", "media", "inbound", "sky10");
}

export function sanitizeFilename(name, fallback = "attachment.bin") {
  const raw = typeof name === "string" ? name.trim() : "";
  const base = path.basename(raw || fallback);
  const sanitized = base.replace(/[^\w.-]+/g, "_");
  return sanitized || fallback;
}

export function guessMimeType(value) {
  const lower = String(value || "").toLowerCase();
  if (lower.endsWith(".png")) return "image/png";
  if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) return "image/jpeg";
  if (lower.endsWith(".gif")) return "image/gif";
  if (lower.endsWith(".webp")) return "image/webp";
  if (lower.endsWith(".svg")) return "image/svg+xml";
  if (lower.endsWith(".mp3")) return "audio/mpeg";
  if (lower.endsWith(".wav")) return "audio/wav";
  if (lower.endsWith(".ogg")) return "audio/ogg";
  if (lower.endsWith(".m4a")) return "audio/mp4";
  if (lower.endsWith(".mp4")) return "video/mp4";
  if (lower.endsWith(".mov")) return "video/quicktime";
  if (lower.endsWith(".webm")) return "video/webm";
  if (lower.endsWith(".pdf")) return "application/pdf";
  if (lower.endsWith(".txt")) return "text/plain";
  if (lower.endsWith(".md")) return "text/markdown";
  if (lower.endsWith(".json")) return "application/json";
  return "application/octet-stream";
}

function mediaTypeFromPart(part) {
  const source = part.source && typeof part.source === "object" ? part.source : {};
  const mediaType = String(part.media_type || source.media_type || guessMimeType(part.filename || source.filename || source.url || "")).trim();
  if (part.type === "image" || mediaType.startsWith("image/")) return "image";
  if (part.type === "audio" || mediaType.startsWith("audio/")) return "audio";
  if (part.type === "video" || mediaType.startsWith("video/")) return "video";
  return "document";
}

function outboundPartType(mediaType) {
  if (mediaType === "image") return "image";
  if (mediaType === "audio") return "audio";
  if (mediaType === "video") return "video";
  return "file";
}

function normalizeContentParts(content) {
  if (typeof content === "string") {
    return [{ type: "text", text: content }];
  }
  if (!content || typeof content !== "object") {
    return [];
  }

  if (Array.isArray(content.parts) && content.parts.length > 0) {
    return content.parts
      .filter((part) => part && typeof part === "object")
      .map((part) => {
        const source = part.source && typeof part.source === "object" ? part.source : undefined;
        return {
          type: typeof part.type === "string" && part.type.trim() ? part.type.trim() : "file",
          text: typeof part.text === "string" ? part.text : "",
          filename: typeof part.filename === "string" ? part.filename : "",
          media_type: typeof part.media_type === "string" ? part.media_type : "",
          source: source
            ? {
                type: typeof source.type === "string" ? source.type : "",
                data: typeof source.data === "string" ? source.data : "",
                url: typeof source.url === "string" ? source.url : "",
                filename: typeof source.filename === "string" ? source.filename : "",
                media_type: typeof source.media_type === "string" ? source.media_type : "",
              }
            : undefined,
        };
      });
  }

  if (typeof content.text === "string" && content.text) {
    return [{ type: "text", text: content.text }];
  }

  return [];
}

function stageBase64Part(sessionId, part) {
  const source = part.source && typeof part.source === "object" ? part.source : {};
  const data = String(source.data || "").trim();
  if (!data) {
    return "";
  }

  const filename = sanitizeFilename(
    part.filename || source.filename,
    `${outboundPartType(mediaTypeFromPart(part))}.bin`,
  );
  const dir = path.join(stagedMediaRoot(), sanitizeFilename(sessionId || "main", "main"));
  fs.mkdirSync(dir, { recursive: true });
  const filePath = path.join(dir, `${Date.now()}-${randomUUID()}-${filename}`);
  fs.writeFileSync(filePath, Buffer.from(data, "base64"));
  return filePath;
}

function collectTextParts(content) {
  if (content && typeof content === "object" && typeof content.text === "string" && content.text.trim()) {
    return content.text.trim();
  }
  const joined = normalizeContentParts(content)
    .filter((part) => part.type === "text" && typeof part.text === "string" && part.text.trim())
    .map((part) => part.text.trim())
    .join("\n\n");
  return joined;
}

export function extractInboundMediaContext(content, sessionId) {
  const mediaPaths = [];
  const mediaUrls = [];
  const mediaTypes = [];

  for (const part of normalizeContentParts(content)) {
    if (part.type === "text") {
      continue;
    }

    const source = part.source && typeof part.source === "object" ? part.source : {};
    const sourceType = String(source.type || "").trim();
    const mediaType = mediaTypeFromPart(part);
    if (sourceType === "base64" && String(source.data || "").trim()) {
      const mediaPath = stageBase64Part(sessionId, part);
      if (mediaPath) {
        mediaPaths.push(mediaPath);
        mediaTypes.push(mediaType);
      }
      continue;
    }
    if (sourceType === "url" && String(source.url || "").trim()) {
      mediaUrls.push(String(source.url).trim());
      mediaTypes.push(mediaType);
    }
  }

  return {
    bodyText: collectTextParts(content),
    mediaPath: mediaPaths[0],
    mediaUrl: mediaUrls[0],
    mediaType: mediaTypes[0],
    mediaPaths,
    mediaUrls,
    mediaTypes,
  };
}

function mediaPartFromUrl(ref) {
  let parsed;
  try {
    parsed = new URL(ref);
  } catch {
    return null;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return null;
  }
  const filename = sanitizeFilename(path.basename(parsed.pathname), "attachment.bin");
  const mediaType = guessMimeType(filename);
  const kind = outboundPartType(mediaType.startsWith("image/") ? "image" : mediaType.startsWith("audio/") ? "audio" : mediaType.startsWith("video/") ? "video" : "document");
  return {
    type: kind,
    filename,
    media_type: mediaType,
    source: {
      type: "url",
      url: ref,
      filename,
      media_type: mediaType,
    },
  };
}

function mediaPartFromFile(ref) {
  const resolved = ref.startsWith("file://")
    ? new URL(ref).pathname
    : ref;
  if (!fs.existsSync(resolved)) {
    return null;
  }
  const stat = fs.statSync(resolved, { throwIfNoEntry: false });
  if (!stat || !stat.isFile()) {
    return null;
  }
  const filename = sanitizeFilename(path.basename(resolved), "attachment.bin");
  const mediaType = guessMimeType(filename);
  const normalizedMediaType = mediaType.startsWith("image/")
    ? "image"
    : mediaType.startsWith("audio/")
      ? "audio"
      : mediaType.startsWith("video/")
        ? "video"
        : "document";
  return {
    type: outboundPartType(normalizedMediaType),
    filename,
    media_type: mediaType,
    source: {
      type: "base64",
      data: fs.readFileSync(resolved).toString("base64"),
      filename,
      media_type: mediaType,
    },
  };
}

export function buildOutboundChatContent(payloadText) {
  const sourceText = typeof payloadText === "string" ? payloadText : String(payloadText || "");
  const bodyLines = [];
  const mediaParts = [];

  for (const line of sourceText.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (trimmed.startsWith("MEDIA:")) {
      const ref = trimmed.slice("MEDIA:".length).trim();
      if (!ref) {
        continue;
      }
      const mediaPart = mediaPartFromUrl(ref) ?? mediaPartFromFile(ref);
      if (mediaPart) {
        mediaParts.push(mediaPart);
        continue;
      }
    }
    bodyLines.push(line);
  }

  const bodyText = bodyLines.join("\n").trim();
  const parts = [];
  if (bodyText) {
    parts.push({ type: "text", text: bodyText });
  }
  parts.push(...mediaParts);

  return {
    text: bodyText || undefined,
    parts,
  };
}
