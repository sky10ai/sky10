import { useEffect, useState } from "react";
import { RunCard } from "../components/assistant/RunCard";
import { WorkspaceHero } from "../components/assistant/WorkspaceHero";
import { WorkspaceSidebar } from "../components/assistant/WorkspaceSidebar";
import {
  AUDIENCE_PLACEHOLDERS,
  SUGGESTED_PROMPTS,
  type WorkspaceRun,
} from "../components/assistant/workspaceTypes";
import { Icon } from "../components/Icon";
import { AGENT_EVENT_TYPES, SANDBOX_STATE_EVENT_TYPES, STORAGE_EVENT_TYPES } from "../lib/events";
import { executeRootAssistantPrompt, type AgentAudience } from "../lib/rootAssistant";
import { agent, identity, sandbox, skyfs, skylink } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

const STORAGE_KEY = "sky10:ai-workspace:runs:v1";

function createRun(prompt: string, audience: AgentAudience): WorkspaceRun {
  return {
    audience,
    id:
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now()}-${Math.random().toString(16).slice(2)}`,
    prompt,
    answer: "",
    status: "running",
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    toolTraces: [],
  };
}

function loadRuns(raw: string | null): WorkspaceRun[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as WorkspaceRun[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export default function AIWorkspace() {
  const [audience, setAudience] = useState<AgentAudience>("for_me");
  const [prompt, setPrompt] = useState("");
  const [runs, setRuns] = useState<WorkspaceRun[]>(() =>
    loadRuns(localStorage.getItem(STORAGE_KEY))
  );
  const [statusLine, setStatusLine] = useState(
    "Describe the agent you want to create. sky10 will inspect the node with read-only tools and draft the next step."
  );

  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: drives } = useRPC(() => skyfs.driveList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: devices } = useRPC(() => identity.deviceList(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: agents } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: sandboxes } = useRPC(() => sandbox.list(), [], {
    live: SANDBOX_STATE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(runs.slice(0, 12)));
    } catch {
      // ignore localStorage failures
    }
  }, [runs]);

  const recentAgents = (agents?.agents ?? []).slice(0, 4);
  const latestRun = runs[0] ?? null;
  const previousRuns = runs.slice(1, 5);
  const sandboxNeedsAttention = (sandboxes?.sandboxes ?? []).filter((item) =>
    item.status.includes("error") || item.status.includes("failed")
  ).length;
  const suggestions = SUGGESTED_PROMPTS[audience];

  async function submitPrompt(nextPrompt: string) {
    const trimmed = nextPrompt.trim();
    if (!trimmed) return;

    const run = createRun(trimmed, audience);
    setPrompt("");
    setStatusLine("Starting a read-only task against the live node.");
    setRuns((prev) => [run, ...prev]);

    const patchRun = (updater: (current: WorkspaceRun) => WorkspaceRun) => {
      setRuns((prev) =>
        prev.map((item) => (item.id === run.id ? updater(item) : item))
      );
    };

    try {
      const result = await executeRootAssistantPrompt(
        trimmed,
        {
          onStatus(value) {
            setStatusLine(value);
          },
          onText(value) {
            patchRun((current) => ({
              ...current,
              answer: value,
              updatedAt: new Date().toISOString(),
            }));
          },
          onTool(trace) {
            patchRun((current) => {
              const existing = current.toolTraces.find((item) => item.id === trace.id);
              const toolTraces = existing
                ? current.toolTraces.map((item) => (item.id === trace.id ? trace : item))
                : [...current.toolTraces, trace];
              return {
                ...current,
                toolTraces,
                updatedAt: new Date().toISOString(),
              };
            });
          },
        },
        { audience }
      );

      patchRun((current) => ({
        ...current,
        answer: result.answer,
        followUps: result.followUps,
        status: result.status,
        updatedAt: new Date().toISOString(),
      }));
      setStatusLine("Task complete. Ask another question or inspect the trace.");
    } catch (error) {
      const message = error instanceof Error ? error.message : "Assistant run failed";
      patchRun((current) => ({
        ...current,
        answer: current.answer
          ? `${current.answer}\n\n${message}`
          : message,
        status: "error",
        updatedAt: new Date().toISOString(),
      }));
      setStatusLine("The last task failed. Inspect the tool trace and try again.");
    }
  }

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 p-12">
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div className="space-y-6">
          <WorkspaceHero
            audience={audience}
            onAudienceChange={(value) => {
              setAudience(value);
              setPrompt("");
            }}
            onPromptSelect={setPrompt}
            onSubmit={() => void submitPrompt(prompt)}
            placeholder={AUDIENCE_PLACEHOLDERS[audience]}
            prompt={prompt}
            runCount={runs.length}
            setPrompt={setPrompt}
            statusLine={statusLine}
            suggestions={suggestions}
          />

          <section className="space-y-4 rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
            <div className="flex items-center justify-between gap-4">
              <div>
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Latest Draft
                </p>
                <h2 className="mt-2 text-2xl font-semibold text-on-surface">
                  Agent draft and trace
                </h2>
              </div>
            </div>

            {!latestRun ? (
              <div className="rounded-3xl border border-dashed border-outline-variant/20 bg-surface p-10 text-center">
                <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                  <Icon className="text-3xl" name="forum" />
                </div>
                <h3 className="mt-4 text-lg font-semibold text-on-surface">
                  No tasks yet
                </h3>
                <p className="mt-2 text-sm text-secondary">
                  Start with an agent idea like “Create an agent that watches my
                  Downloads folder and organizes receipts.” sky10 will draft the
                  next step and show the live inspection tools it used.
                </p>
              </div>
            ) : (
              <RunCard onPromptSelect={setPrompt} run={latestRun} />
            )}
          </section>

          {previousRuns.length > 0 && (
            <section className="rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
              <div>
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Earlier Drafts
                </p>
                <h2 className="mt-2 text-xl font-semibold text-on-surface">
                  Previous drafts
                </h2>
              </div>
              <div className="mt-4 space-y-3">
                {previousRuns.map((run) => (
                  <button
                    key={run.id}
                    className="flex w-full items-center justify-between rounded-2xl border border-outline-variant/10 bg-surface px-4 py-3 text-left transition-colors hover:border-primary/20"
                    onClick={() => setPrompt(run.prompt)}
                    type="button"
                  >
                    <div className="min-w-0">
                      <p className="truncate font-medium text-on-surface">{run.prompt}</p>
                      <p className="mt-1 text-xs text-secondary">
                        {run.toolTraces.length} tool call{run.toolTraces.length === 1 ? "" : "s"} · {run.status}
                      </p>
                    </div>
                    <span className="text-xs text-secondary">Reuse ask</span>
                  </button>
                ))}
              </div>
            </section>
          )}
        </div>

        <WorkspaceSidebar
          audience={audience}
          devices={devices}
          drives={drives}
          health={health}
          linkStatus={linkStatus}
          onPromptSelect={setPrompt}
          recentAgents={recentAgents}
          sandboxNeedsAttention={sandboxNeedsAttention}
          sandboxes={sandboxes}
          suggestions={suggestions}
        />
      </div>
    </section>
  );
}
