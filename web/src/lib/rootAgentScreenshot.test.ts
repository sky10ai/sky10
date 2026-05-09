import { describe, expect, test } from "bun:test";
import { buildDebugScreenshotUpload, type CapturedScreenshot } from "./rootAgentScreenshot";
import type { RootAgentPageContext } from "./rootAgentContext";

function context(overrides: Partial<RootAgentPageContext> = {}): RootAgentPageContext {
  return {
    area: "agents",
    capturedAt: "2026-05-09T00:00:00.000Z",
    controls: ["Create agent"],
    heading: "Agents",
    headings: ["Agents"],
    pageLabel: "Agents",
    route: "/agents",
    title: "sky10",
    viewport: "1440x900",
    visibleText: "Agents",
    ...overrides,
  };
}

describe("rootAgentScreenshot", () => {
  test("builds debug screenshot upload params from a captured PNG", async () => {
    const blob = new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" });
    const screenshot: CapturedScreenshot = {
      blob,
      capturedAt: "2026-05-09T00:01:00.000Z",
      contentType: "image/png",
      filename: "sky10-context.png",
      height: 900,
      sizeBytes: blob.size,
      url: "blob:test",
      width: 1440,
    };

    const upload = await buildDebugScreenshotUpload(screenshot, context());

    expect(upload.captured_at).toBe(screenshot.capturedAt);
    expect(upload.content_type).toBe("image/png");
    expect(upload.data_base64).toBe("AQID");
    expect(upload.filename).toBe("sky10-context.png");
    expect(upload.page_context).toMatchObject({ route: "/agents" });
    expect(upload.size_bytes).toBe(3);
  });
});
