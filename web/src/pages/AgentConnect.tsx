import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";

const GUIDES = [
  {
    id: "openclaw",
    icon: "psychology",
    label: "OpenClaw",
    description: "Full tool access — code, shell, browser, file ops",
    steps: [
      "Install the sky10 channel plugin:",
      null,
      "Add to your OpenClaw config (~/.openclaw/openclaw.json):",
      null,
      "Enable the gateway HTTP API in the same config:",
      null,
      "Restart the OpenClaw gateway. The agent will auto-register on sky10.",
    ],
    codeBlocks: [
      `openclaw plugins install github:sky10ai/openclaw-sky10-channel
cd ~/.openclaw/plugins/sky10 && npm i eventsource`,
      `"plugins": {
  "entries": {
    "sky10": {
      "enabled": true,
      "config": {
        "agentName": "my-agent",
        "skills": ["code", "shell", "web-search", "file-ops"],
        "gatewayUrl": "http://localhost:18789",
        "gatewayToken": "<your gateway auth token>"
      }
    }
  }
}`,
      `"gateway": {
  "http": {
    "endpoints": {
      "responses": { "enabled": true }
    }
  }
}`,
    ],
  },
  {
    id: "claude-code",
    icon: "terminal",
    label: "Claude Code",
    description: "Connect Claude Code as a sky10 agent via MCP",
    steps: [
      "Add the sky10 MCP server to your Claude Code config:",
      null,
      "Claude Code will register as an agent and listen for messages over sky10.",
    ],
    codeBlocks: [
      `// .claude/settings.json
{
  "mcpServers": {
    "sky10": {
      "command": "sky10",
      "args": ["mcp", "--agent-name", "claude-code"]
    }
  }
}`,
    ],
  },
  {
    id: "custom",
    icon: "code",
    label: "Custom Agent",
    description: "Any language — just HTTP calls to the daemon",
    steps: [
      "Register your agent:",
      null,
      "Listen for messages via SSE:",
      null,
      "Send responses back:",
      null,
    ],
    codeBlocks: [
      `curl -X POST localhost:9101/rpc \\
  -H "Content-Type: application/json" \\
  -d '{"jsonrpc":"2.0","method":"agent.register",
       "params":{"name":"my-agent",
                 "skills":["code","test"]},"id":1}'`,
      `curl -N localhost:9101/rpc/events
# Listen for "agent.message" events`,
      `curl -X POST localhost:9101/rpc \\
  -H "Content-Type: application/json" \\
  -d '{"jsonrpc":"2.0","method":"agent.send",
       "params":{"to":"<sender>",
                 "session_id":"<session>",
                 "type":"text",
                 "content":{"text":"Hello!"}},"id":1}'`,
    ],
  },
];

export default function AgentConnect() {
  const navigate = useNavigate();

  return (
    <div className="p-12 max-w-5xl mx-auto">
      <div className="flex items-center gap-4 mb-8">
        <button
          onClick={() => navigate("/agents")}
          className="text-secondary hover:text-on-surface transition-colors"
        >
          <Icon name="arrow_back" />
        </button>
        <div>
          <h1 className="text-3xl font-bold tracking-tight text-on-surface">
            Connect an Agent
          </h1>
          <p className="text-secondary text-sm">
            Register an AI agent on your sky10 network. Pick your platform.
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {GUIDES.map((guide) => (
          <div
            key={guide.id}
            className="rounded-xl p-6 bg-surface-container-lowest ring-1 ring-outline-variant/10"
          >
            <div className="flex items-center gap-3 mb-4">
              <div className="w-10 h-10 rounded-xl flex items-center justify-center bg-primary-fixed/20 text-primary">
                <Icon name={guide.icon} className="text-xl" />
              </div>
              <div>
                <h3 className="text-base font-bold text-on-surface">
                  {guide.label}
                </h3>
                <p className="text-xs text-secondary">{guide.description}</p>
              </div>
            </div>
            <div className="space-y-3">
              {(() => {
                let codeIdx = 0;
                return guide.steps.map((step, i) => {
                  if (step === null) {
                    const code = guide.codeBlocks[codeIdx++];
                    return (
                      <pre
                        key={i}
                        className="bg-surface-container rounded-lg p-3 text-[11px] font-mono text-on-surface-variant overflow-x-auto whitespace-pre-wrap"
                      >
                        {code}
                      </pre>
                    );
                  }
                  return (
                    <p key={i} className="text-xs text-secondary">
                      {step}
                    </p>
                  );
                });
              })()}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
