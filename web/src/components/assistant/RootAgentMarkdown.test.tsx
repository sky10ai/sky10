import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { RootAgentMarkdown } from "./RootAgentMarkdown";

describe("RootAgentMarkdown", () => {
  test("renders common markdown structures", () => {
    const html = renderToStaticMarkup(
      <RootAgentMarkdown
        text={[
          "## Summary",
          "",
          "- **Healthy** network",
          "- `agent.run` is available",
          "",
          "| State | Count |",
          "| --- | ---: |",
          "| ready | 2 |",
        ].join("\n")}
      />,
    );

    expect(html).toContain("<h2");
    expect(html).toContain("<ul");
    expect(html).toContain("<strong");
    expect(html).toContain("<code");
    expect(html).toContain("<table");
  });

  test("escapes raw html from model output", () => {
    const html = renderToStaticMarkup(
      <RootAgentMarkdown text={"<script>alert(1)</script>\n\n**safe**"} />,
    );

    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;alert(1)&lt;/script&gt;");
    expect(html).toContain("<strong");
  });
});
