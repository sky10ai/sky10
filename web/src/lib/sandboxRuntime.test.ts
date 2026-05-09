import { describe, expect, test } from "bun:test";
import { sandboxRuntimeView } from "./sandboxRuntime";
import type { HealthResult, SandboxRuntimeStatusResult } from "./rpc";

describe("sandboxRuntimeView", () => {
  test("uses a warning tone when the guest runtime is stale", () => {
    const view = sandboxRuntimeView(
      {
        name: "linux-dev",
        slug: "linux-dev",
        template: "ubuntu",
        reachable: true,
        version: "v0.46.0",
      } satisfies SandboxRuntimeStatusResult,
      {
        version: "v0.47.0",
      } as HealthResult,
    );

    expect(view?.label).toBe("Guest stale");
    expect(view?.tone).toBe("warning");
  });
});
