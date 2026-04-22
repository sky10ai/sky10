import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { buildOutboundChatContent, extractInboundMediaContext } from "./media.js";

test("extractInboundMediaContext stages base64 attachments and exposes media metadata", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "openclaw-sky10-media-"));
  process.env.OPENCLAW_SKY10_MEDIA_DIR = tempRoot;

  try {
    const content = {
      text: "look at this",
      parts: [
        { type: "text", text: "look at this" },
        {
          type: "image",
          filename: "diagram.png",
          media_type: "image/png",
          source: {
            type: "base64",
            filename: "diagram.png",
            media_type: "image/png",
            data: Buffer.from([0x89, 0x50, 0x4e, 0x47]).toString("base64"),
          },
        },
        {
          type: "file",
          filename: "notes.txt",
          media_type: "text/plain",
          source: {
            type: "base64",
            filename: "notes.txt",
            media_type: "text/plain",
            data: Buffer.from("hello from attachment").toString("base64"),
          },
        },
      ],
    };

    const inbound = extractInboundMediaContext(content, "session-a");
    assert.equal(inbound.bodyText, "look at this");
    assert.equal(inbound.mediaType, "image");
    assert.equal(inbound.mediaPaths.length, 2);
    assert.deepEqual(inbound.mediaTypes, ["image", "document"]);
    assert.ok(fs.existsSync(inbound.mediaPaths[0]));
    assert.ok(fs.existsSync(inbound.mediaPaths[1]));
    assert.equal(fs.readFileSync(inbound.mediaPaths[1], "utf8"), "hello from attachment");
  } finally {
    delete process.env.OPENCLAW_SKY10_MEDIA_DIR;
    fs.rmSync(tempRoot, { recursive: true, force: true });
  }
});

test("buildOutboundChatContent extracts MEDIA refs into chat parts", () => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "openclaw-sky10-reply-"));
  const imagePath = path.join(tempDir, "artifact.png");
  fs.writeFileSync(imagePath, Buffer.from([0x89, 0x50, 0x4e, 0x47]));

  try {
    const content = buildOutboundChatContent(
      `Here you go.\nMEDIA:${imagePath}\nMEDIA:https://example.com/report.pdf`,
    );

    assert.equal(content.text, "Here you go.");
    assert.equal(content.parts.length, 3);
    assert.deepEqual(content.parts.map((part) => part.type), ["text", "image", "file"]);
    assert.equal(content.parts[1].filename, "artifact.png");
    assert.equal(content.parts[1].source?.type, "base64");
    assert.ok(content.parts[1].source?.data);
    assert.equal(content.parts[2].source?.type, "url");
    assert.equal(content.parts[2].source?.url, "https://example.com/report.pdf");
  } finally {
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
});
