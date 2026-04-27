import { useState } from "react";
import { useNavigate } from "react-router";
import { RunCard } from "../components/assistant/RunCard";
import type { WorkspaceRun } from "../components/assistant/workspaceTypes";
import { Icon } from "../components/Icon";
import {
  PageDescription,
  PageHeader,
  PageTitle,
} from "../components/PageHeader";
import { RelativeTime } from "../components/RelativeTime";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES } from "../lib/events";
import { executeRootAssistantPrompt } from "../lib/rootAssistant";
import { agent, rootAssistant } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

const AGENT_IDEAS = [
  {
    label: "Watch my Downloads...",
    prompt: "Watch my Downloads folder and organize receipts.",
  },
  {
    label: "Summarize new meetings...",
    prompt: "Summarize new meeting recordings and save action items.",
  },
  {
    label: "Transcribe podcast uploads...",
    prompt: "Transcribe podcast uploads and prepare show notes.",
  },
  {
    label: "Check sync health...",
    prompt: "Check sync health each morning and tell me what needs attention.",
  },
] as const;

function createAgentRun(prompt: string): WorkspaceRun {
  return {
    audience: "for_me",
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

export default function Agents() {
  const navigate = useNavigate();
  const { data, loading, error } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const [prompt, setPrompt] = useState("");
  const [run, setRun] = useState<WorkspaceRun | null>(null);
  const [builderStatus, setBuilderStatus] = useState("");

  const agents = data?.agents ?? [];

  // Count unique devices hosting agents.
  const deviceSet = new Set(agents.map((a) => a.device_id));

  async function saveRun(nextRun: WorkspaceRun) {
    try {
      await rootAssistant.runSave({ run: nextRun });
    } catch {
      setBuilderStatus("Save failed.");
    }
  }

  async function submitPrompt(nextPrompt: string) {
    const trimmed = nextPrompt.trim();
    if (!trimmed) return;

    let currentRun = createAgentRun(trimmed);
    setPrompt("");
    setBuilderStatus("Reading...");
    setRun(currentRun);
    void saveRun(currentRun);

    const patchRun = (updater: (current: WorkspaceRun) => WorkspaceRun) => {
      currentRun = updater(currentRun);
      setRun(currentRun);
      void saveRun(currentRun);
    };

    try {
      const result = await executeRootAssistantPrompt(
        trimmed,
        {
          onStatus(value) {
            setBuilderStatus(value);
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
        { audience: "for_me", intent: "agent_create" },
      );

      patchRun((current) => ({
        ...current,
        answer: result.answer,
        followUps: result.followUps,
        status: result.status,
        updatedAt: new Date().toISOString(),
      }));
      setBuilderStatus("Done.");
    } catch (submitError) {
      const message =
        submitError instanceof Error ? submitError.message : "Agent draft failed";
      patchRun((current) => ({
        ...current,
        answer: current.answer ? `${current.answer}\n\n${message}` : message,
        status: "error",
        updatedAt: new Date().toISOString(),
      }));
      setBuilderStatus("Failed.");
    }
  }

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 px-6 pb-12 pt-6 sm:px-8 sm:pt-7 lg:px-10">
      {error && (
        <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
          {error}
        </div>
      )}

      <PageHeader
        actions={
          <button
            onClick={() => navigate("/agents/connect")}
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
            type="button"
          >
            <Icon name="add" className="text-base" />
            Connect Existing...
          </button>
        }
      >
        <PageTitle>Agents</PageTitle>
        <PageDescription>
          {agents.length} agent{agents.length !== 1 ? "s" : ""} across{" "}
          {deviceSet.size} device{deviceSet.size !== 1 ? "s" : ""}
        </PageDescription>
      </PageHeader>

      <section className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-on-surface">
            Create an agent
          </h2>
          {builderStatus && (
            <p className="truncate text-xs text-secondary">{builderStatus}</p>
          )}
        </div>

        <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
          <div className="space-y-3">
            <textarea
              className="min-h-[112px] w-full resize-none rounded-2xl border border-outline-variant/15 bg-surface px-4 py-3 text-sm leading-6 text-on-surface outline-none transition-colors placeholder:text-secondary/80 focus:border-primary/35"
              onChange={(event) => setPrompt(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault();
                  void submitPrompt(prompt);
                }
              }}
              placeholder="Describe the agent you want..."
              value={prompt}
            />
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex min-w-0 flex-wrap gap-2">
                {AGENT_IDEAS.map((idea) => (
                  <button
                    key={idea.label}
                    className="max-w-full truncate rounded-full border border-outline-variant/15 bg-surface px-3 py-2 text-xs text-secondary transition-colors hover:border-primary/20 hover:text-on-surface"
                    onClick={() => setPrompt(idea.prompt)}
                    type="button"
                  >
                    {idea.label}
                  </button>
                ))}
              </div>
              <button
                className="inline-flex shrink-0 items-center justify-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
                disabled={!prompt.trim()}
                onClick={() => void submitPrompt(prompt)}
                type="button"
              >
                <Icon name="send" className="text-base" />
                Create
              </button>
            </div>
          </div>
        </div>
        {run && <RunCard onPromptSelect={setPrompt} run={run} />}
      </section>

      <section className="border-t border-outline-variant/10 pt-6">
        <div className="mb-4 flex items-center justify-between gap-3">
          <h2 className="text-xl font-semibold text-on-surface">My Agents</h2>
          <button
            onClick={() => navigate("/agents/create")}
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
            type="button"
          >
            <Icon name="deployed_code" className="text-base" />
            Manual Create
          </button>
        </div>

        {loading && agents.length === 0 && (
          <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
            {[1, 2].map((i) => (
              <div
                key={i}
                className="h-[280px] animate-pulse rounded-xl bg-surface-container-lowest p-6"
              />
            ))}
          </div>
        )}

        <div className="mb-16 grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
          {!loading && agents.length === 0 && (
            <div className="rounded-xl border border-dashed border-outline-variant/20 bg-surface-container-lowest p-6 text-sm text-secondary">
              No agents yet.
            </div>
          )}
          {agents.map((a) => (
            <div
              key={`${a.device_id}-${a.id}`}
              onClick={() => navigate(`/agents/${a.id}`, { state: { agent: a } })}
              className="cursor-pointer rounded-xl bg-surface-container-lowest p-6 shadow-sm ring-1 ring-outline-variant/10 transition-all duration-500 hover:shadow-xl active:scale-[0.98]"
            >
              <div className="mb-3 flex h-5 items-center justify-between">
                <StatusBadge pulse tone="live">
                  Connected
                </StatusBadge>
              </div>

              <div className="mb-6 flex items-start gap-4">
                <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-tertiary-fixed/30 text-tertiary">
                  <Icon name="smart_toy" className="text-3xl" />
                </div>
                <div className="min-w-0 flex-1">
                  <h3 className="truncate text-xl font-bold text-on-surface">
                    {a.name}
                  </h3>
                  <p className="flex items-center gap-1 text-xs text-secondary">
                    <Icon name="dns" className="text-xs" />
                    {a.device_name}
                    <span className="text-outline">({a.device_id})</span>
                  </p>
                </div>
              </div>

              <div className="space-y-4">
                {a.skills && a.skills.length > 0 && (
                  <div>
                    <label className="mb-1.5 block text-[10px] font-bold uppercase tracking-widest text-secondary">
                      Skills
                    </label>
                    <div className="flex flex-wrap gap-1.5">
                      {a.skills.map((skill) => (
                        <span
                          key={skill}
                          className="rounded-full bg-primary-fixed/20 px-2 py-0.5 text-[10px] font-semibold text-primary"
                        >
                          {skill}
                        </span>
                      ))}
                    </div>
                  </div>
                )}

                <div className="flex items-center justify-between py-2 text-xs">
                  <span className="font-medium text-secondary">Connected</span>
                  <RelativeTime
                    className="font-semibold text-on-surface"
                    value={a.connected_at}
                  />
                </div>
              </div>
            </div>
          ))}
        </div>
      </section>

      {/* sky10 Network Agents — placeholder */}
      <div className="border-t border-outline-variant/10 pt-8">
        <div className="flex items-center justify-between mb-2">
          <h2 className="text-2xl font-bold tracking-tight text-on-surface">
            sky10 Network
          </h2>
          <button
            onClick={() => navigate("/agents/connect")}
            className="text-primary text-sm font-medium hover:underline flex items-center gap-1"
          >
            <Icon name="add" className="text-base" />
            Connect Existing...
          </button>
        </div>
        <p className="text-secondary text-sm">
          Browse agents on the sky10 network. Coming soon.
        </p>
      </div>
    </section>
  );
}
