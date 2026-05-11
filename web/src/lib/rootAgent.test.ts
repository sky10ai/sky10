import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { executeRootAgentPrompt } from "./rootAgent";
import type { RootAgentPageContext } from "./rootAgentContext";

const originalFetch = globalThis.fetch;
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");

function agentPageContext(): RootAgentPageContext {
  return {
    area: "agents",
    browserState: "",
    capturedAt: "2026-05-11T03:36:00.000Z",
    controls: ["Message custom-agent...", "send"],
    heading: "custom-agent",
    headings: ["custom-agent"],
    html: "<main><h2>custom-agent</h2><span>Queued</span></main>",
    pageLabel: "Agent chat",
    route: "/agents/A-ql96d7d5gwldgfgj",
    routeParams: { agentId: "A-ql96d7d5gwldgfgj" },
    title: "sky10",
    viewport: "1440x900",
    visibleText: "custom-agent\nQueued via private mailbox",
  };
}

function rpcResult(id: number, result: unknown) {
  return new Response(JSON.stringify({ jsonrpc: "2.0", id, result }), {
    headers: { "Content-Type": "application/json" },
  });
}

describe("executeRootAgentPrompt", () => {
  beforeEach(() => {
    Object.defineProperty(globalThis, "window", {
      configurable: true,
      value: {
        setTimeout: (callback: () => void) => setTimeout(callback, 0),
      },
    });

    globalThis.fetch = async (_url, init) => {
      const body = JSON.parse(String(init?.body ?? "{}")) as {
        id: number;
        method: string;
      };
      switch (body.method) {
        case "system.health":
          return rpcResult(body.id, {
            conflict_drives: 0,
            conflict_files: 0,
            drives: 1,
            drives_running: 1,
            outbox_pending: 0,
            path_issue_drives: 0,
            status: "ok",
            sync_error_drives: 0,
            transfer_pending: 0,
            uptime: "1h",
            version: "test",
          });
        case "agent.list":
          return rpcResult(body.id, {
            agents: [{
              connected_at: "2026-05-11T03:38:05Z",
              device_id: "D-qy8f8x55",
              device_name: "lima-custom-agent",
              id: "A-ql96d7d5gwldgfgj",
              name: "custom-agent",
              sandbox: {
                name: "custom-agent",
                provider: "lima",
                slug: "custom-agent",
                status: "ready",
                template: "openclaw-docker",
              },
              skills: ["agent.run"],
              status: "connected",
              tools: [],
            }],
            count: 1,
          });
        case "agent.job.list":
          return rpcResult(body.id, {
            jobs: [{
              agent_id: "A-ql96d7d5gwldgfgj",
              agent_name: "custom-agent",
              buyer: "sky10://buyer",
              created_at: "2026-05-07T23:11:21Z",
              delivery: {
                durable_transport: "private_mailbox",
                durable_used: true,
                last_error: "device D-qy8f8x55 not connected",
                last_event: "delivery_failed",
                last_transport: "skylink",
                live_attempted: true,
                live_transport: "skylink",
                mailbox_item_id: "501a9f02",
                mailbox_state: "failed",
                policy: "mailbox_backed",
                scope: "private_network",
                status: "queued",
              },
              job_id: "j_cde5f272-f168-4fe2-a837-c84a45098954",
              payment_state: "none",
              seller: "sky10://A-ql96d7d5gwldgfgj",
              tool: "agent.run",
              updated_at: "2026-05-07T23:11:21Z",
              work_state: "accepted",
            }],
            count: 1,
          });
        default:
          throw new Error(`unexpected RPC method ${body.method}`);
      }
    };
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
    if (originalWindow) {
      Object.defineProperty(globalThis, "window", originalWindow);
    } else {
      delete (globalThis as { window?: unknown }).window;
    }
  });

  test("explains queued labels from agent job delivery state", async () => {
    const result = await executeRootAgentPrompt(
      "why does the message show queued on this page",
      {},
      { pageContext: agentPageContext() },
    );

    expect(result.status).toBe("complete");
    expect(result.answer).toContain("older agent job/message record");
    expect(result.answer).toContain("device D-qy8f8x55 not connected");
    expect(result.answer).toContain("currently reports `connected`");
  });
});
