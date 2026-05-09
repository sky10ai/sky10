export type RootAgentPageArea =
  | "activity"
  | "agents"
  | "apps"
  | "codex"
  | "devices"
  | "drives"
  | "files"
  | "kv"
  | "mailbox"
  | "network"
  | "sandbox"
  | "secrets"
  | "services"
  | "settings"
  | "start"
  | "wallet"
  | "unknown";

export interface RootAgentPageContext {
  area: RootAgentPageArea;
  capturedAt: string;
  controls: string[];
  heading: string;
  headings: string[];
  pageLabel: string;
  route: string;
  selection?: string;
  title: string;
  viewport: string;
  visibleText: string;
}

export interface RootAgentScreenshotContext {
  capturedAt: string;
  filename: string;
  height: number;
  sizeBytes: number;
  width: number;
}

interface RouteDescriptor {
  area: RootAgentPageArea;
  label: string;
  match: (pathname: string) => boolean;
}

const ROUTES: RouteDescriptor[] = [
  { area: "start", label: "Start", match: (path) => path.startsWith("/start") },
  { area: "codex", label: "Codex chat", match: (path) => path === "/codex" },
  { area: "agents", label: "Create agent", match: (path) => path === "/agents/create" },
  { area: "agents", label: "Connect agent", match: (path) => path === "/agents/connect" },
  { area: "agents", label: "Agent chat", match: (path) => /^\/agents\/[^/]+$/.test(path) },
  { area: "agents", label: "Agents", match: (path) => path === "/agents" },
  { area: "files", label: "Drive files", match: (path) => path.startsWith("/drives/") },
  { area: "drives", label: "Drives", match: (path) => path === "/drives" },
  { area: "files", label: "Bucket", match: (path) => path.startsWith("/bucket") },
  { area: "mailbox", label: "Mailbox", match: (path) => path === "/settings/mailbox" || path === "/mailbox" },
  { area: "network", label: "Network", match: (path) => path === "/settings/network" || path === "/network" },
  { area: "kv", label: "KV store", match: (path) => path === "/settings/kv" || path === "/kv" },
  { area: "activity", label: "Activity", match: (path) => path === "/settings/activity" || path === "/activity" },
  { area: "apps", label: "Apps", match: (path) => path === "/settings/apps" },
  { area: "codex", label: "ChatGPT connection", match: (path) => path === "/settings/codex" },
  { area: "settings", label: "Identity", match: (path) => path === "/settings/identity" },
  { area: "devices", label: "Device invite", match: (path) => path === "/settings/devices/invite" || path === "/devices/invite" },
  { area: "devices", label: "Devices", match: (path) => path === "/settings/devices" || path === "/devices" },
  { area: "secrets", label: "Secrets", match: (path) => path === "/settings/secrets" },
  { area: "services", label: "Services", match: (path) => path === "/settings/services" },
  { area: "settings", label: "Visuals", match: (path) => path === "/settings/visuals" },
  { area: "wallet", label: "Wallet", match: (path) => path === "/settings/wallet" || path === "/wallet" },
  { area: "sandbox", label: "Sandbox detail", match: (path) => path.startsWith("/settings/sandboxes/") || path.startsWith("/sandboxes/") },
  { area: "sandbox", label: "Sandboxes", match: (path) => path === "/settings/sandboxes" || path === "/sandboxes" },
  { area: "settings", label: "Settings", match: (path) => path === "/settings" },
];

function compactWhitespace(value: string) {
  return value.replace(/\u00a0/g, " ").replace(/[ \t]+/g, " ").trim();
}

function compactLines(value: string, maxChars: number) {
  const seen = new Set<string>();
  const lines = value
    .split(/\r?\n/)
    .map(compactWhitespace)
    .filter(Boolean)
    .filter((line) => {
      if (seen.has(line)) return false;
      seen.add(line);
      return true;
    });
  const text = lines.join("\n");
  return text.length > maxChars ? `${text.slice(0, maxChars).trim()}...` : text;
}

function elementText(element: Element) {
  if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) {
    return element.value || element.placeholder || element.getAttribute("aria-label") || "";
  }
  return element.textContent || element.getAttribute("aria-label") || element.getAttribute("title") || "";
}

function isElementVisible(element: Element) {
  if (!(element instanceof HTMLElement)) return false;
  const style = window.getComputedStyle(element);
  if (style.display === "none" || style.visibility === "hidden" || style.opacity === "0") return false;
  const rect = element.getBoundingClientRect();
  return rect.bottom >= 0 && rect.right >= 0 && rect.top <= window.innerHeight && rect.left <= window.innerWidth;
}

function routeDescriptor(pathname: string) {
  return ROUTES.find((route) => route.match(pathname)) ?? {
    area: "unknown" as const,
    label: pathname === "/" ? "Home" : pathname,
  };
}

export function describeRootAgentRoute(pathname: string) {
  const descriptor = routeDescriptor(pathname);
  return {
    area: descriptor.area,
    label: descriptor.label,
  };
}

export function collectRootAgentPageContext(): RootAgentPageContext {
  const route = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  const descriptor = routeDescriptor(window.location.pathname);
  const root = document.querySelector("main") ?? document.body;
  const headings = Array.from(root.querySelectorAll("h1, h2"))
    .map((element) => compactWhitespace(elementText(element)))
    .filter(Boolean)
    .slice(0, 8);
  const controls = Array.from(root.querySelectorAll("button, a, input, textarea, select"))
    .filter(isElementVisible)
    .map((element) => compactWhitespace(elementText(element)))
    .filter(Boolean)
    .slice(0, 18);
  const selectedText = compactLines(window.getSelection()?.toString() ?? "", 1200);
  const visibleText = compactLines(root.innerText || root.textContent || "", 3600);

  return {
    area: descriptor.area,
    capturedAt: new Date().toISOString(),
    controls,
    heading: headings[0] ?? descriptor.label,
    headings,
    pageLabel: descriptor.label,
    route,
    selection: selectedText || undefined,
    title: document.title || "sky10",
    viewport: `${window.innerWidth}x${window.innerHeight}`,
    visibleText,
  };
}

export function rootAgentSuggestionsForContext(context: RootAgentPageContext) {
  switch (context.area) {
    case "agents":
      return [
        "Show me my agents and where they run.",
        "Draft the next agent from this page.",
        "Tell me what needs attention here.",
      ];
    case "drives":
    case "files":
      return [
        "Which drives or files need attention?",
        "Make a troubleshooting note for this view.",
        "Tell me what needs attention here.",
      ];
    case "network":
      return [
        "Check whether my network is healthy.",
        "Make a troubleshooting note for this view.",
        "Tell me what needs attention right now.",
      ];
    case "sandbox":
      return [
        "Summarize sandbox state.",
        "Tell me what needs attention here.",
        "Make a troubleshooting note for this view.",
      ];
    case "devices":
      return [
        "Summarize device membership.",
        "Tell me what needs attention here.",
        "Make a troubleshooting note for this view.",
      ];
    default:
      return [
        "Explain this page.",
        "Tell me what needs attention here.",
        "Make a troubleshooting note for this view.",
      ];
  }
}

export function formatRootAgentContextForPrompt(
  context: RootAgentPageContext,
  screenshot?: RootAgentScreenshotContext | null,
) {
  const lines = [
    "Current sky10 UI context:",
    `Page: ${context.pageLabel}`,
    `Route: ${context.route}`,
    `Heading: ${context.heading}`,
    `Viewport: ${context.viewport}`,
  ];

  if (context.selection) lines.push(`Selected text:\n${context.selection}`);
  if (context.headings.length > 0) lines.push(`Visible headings:\n${context.headings.join("\n")}`);
  if (context.controls.length > 0) lines.push(`Visible controls:\n${context.controls.join(", ")}`);
  if (context.visibleText) lines.push(`Visible text sample:\n${context.visibleText}`);
  if (screenshot) {
    lines.push(
      `Screenshot captured: ${screenshot.filename} (${screenshot.width}x${screenshot.height}, ${screenshot.sizeBytes} bytes)`,
    );
  }

  return lines.join("\n\n");
}

export function buildRootAgentTroubleshootingDoc(
  context: RootAgentPageContext,
  answer: string,
  traces: readonly { detail: string; rpcMethod: string; status: string; title: string }[],
  screenshot?: RootAgentScreenshotContext | null,
) {
  const lines = [
    "# sky10 troubleshooting context",
    "",
    `- Captured: ${context.capturedAt}`,
    `- Page: ${context.pageLabel}`,
    `- Route: ${context.route}`,
    `- Heading: ${context.heading}`,
    `- Viewport: ${context.viewport}`,
  ];

  if (screenshot) {
    lines.push(`- Screenshot: ${screenshot.filename} (${screenshot.width}x${screenshot.height})`);
  }

  if (context.selection) {
    lines.push("", "## Selected Text", "", context.selection);
  }

  if (answer.trim()) {
    lines.push("", "## RootAgent Notes", "", answer.trim());
  }

  if (traces.length > 0) {
    lines.push("", "## Tool Trace", "");
    for (const trace of traces) {
      lines.push(`- ${trace.title} (${trace.rpcMethod}): ${trace.status} - ${trace.detail}`);
    }
  }

  if (context.visibleText) {
    lines.push("", "## Visible Page Text", "", context.visibleText);
  }

  return `${lines.join("\n")}\n`;
}
