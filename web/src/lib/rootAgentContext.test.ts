import { describe, expect, test } from "bun:test";
import {
  buildRootAgentTroubleshootingDoc,
  describeRootAgentRoute,
  formatRootAgentContextForPrompt,
  rootAgentSuggestionsForContext,
  type RootAgentPageContext,
} from "./rootAgentContext";

function context(overrides: Partial<RootAgentPageContext> = {}): RootAgentPageContext {
  return {
    area: "agents",
    browserState: "",
    capturedAt: "2026-05-09T00:00:00.000Z",
    controls: ["Create agent", "Ask AI"],
    heading: "Agents",
    headings: ["Agents", "Create an agent"],
    html: "<main><h1>Agents</h1></main>",
    pageLabel: "Agents",
    route: "/agents",
    routeParams: {},
    title: "sky10",
    viewport: "1440x900",
    visibleText: "Agents\nCreate an agent\nNo agents yet.",
    ...overrides,
  };
}

describe("rootAgentContext", () => {
  test("labels explicit agent routes before the dynamic chat route", () => {
    expect(describeRootAgentRoute("/agents/create")).toEqual({
      area: "agents",
      label: "Create agent",
    });
    expect(describeRootAgentRoute("/agents/connect")).toEqual({
      area: "agents",
      label: "Connect agent",
    });
    expect(describeRootAgentRoute("/agents/A-local")).toEqual({
      area: "agents",
      label: "Agent chat",
    });
  });

  test("formats page and screenshot context for RootAgent prompts", () => {
    const text = formatRootAgentContextForPrompt(context({ selection: "Selected row" }), {
      capturedAt: "2026-05-09T00:01:00.000Z",
      filename: "sky10-context.png",
      height: 900,
      sizeBytes: 12345,
      width: 1440,
    });

    expect(text).toContain("Page: Agents");
    expect(text).toContain("Selected text:\nSelected row");
    expect(text).toContain("Rendered React HTML:");
    expect(text).toContain("Screenshot captured: sky10-context.png");
  });

  test("builds troubleshooting docs with answer and trace data", () => {
    const doc = buildRootAgentTroubleshootingDoc(
      context(),
      "The agent registry is empty.",
      [
        {
          detail: "0 registered agents",
          rpcMethod: "agent.list",
          status: "complete",
          title: "List agents",
        },
      ],
      null,
    );

    expect(doc).toContain("# sky10 troubleshooting context");
    expect(doc).toContain("The agent registry is empty.");
    expect(doc).toContain("List agents (agent.list): complete - 0 registered agents");
  });

  test("returns route-specific suggestions", () => {
    expect(rootAgentSuggestionsForContext(context())).toContain(
      "Show me my agents and where they run.",
    );
    expect(rootAgentSuggestionsForContext(context({ area: "network" }))).toContain(
      "Check whether my network is healthy.",
    );
  });
});
