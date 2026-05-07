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
import { executeRootAgentPrompt } from "../lib/rootAgent";
import { agent, rootAgent, type AgentSpec } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

const MEDIA_ACCENT_PROMPT =
  "make me an ai agent that can process media files to change the accent to british";

const AGENT_IDEAS = [
  {
    label: "Process media files...",
    prompt: MEDIA_ACCENT_PROMPT,
  },
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

function uniq(values: string[]) {
  return [...new Set(values.filter(Boolean))];
}

function displayPricing(spec: AgentSpec) {
  const pricing = spec.commerce.enabled
    ? spec.commerce.default_pricing
    : spec.tools[0]?.pricing;
  if (!pricing || pricing.model === "free") return "Free";
  if (pricing.model === "variable") {
    return `${pricing.rate || "0"} ${pricing.payment_asset?.symbol || ""} / ${pricing.unit || "unit"}`;
  }
  return `${pricing.amount || "0"} ${pricing.payment_asset?.symbol || ""}`;
}

function applyCommerce(spec: AgentSpec, enabled: boolean): AgentSpec {
  const pricing = enabled
    ? {
        model: "variable",
        unit: "audio_minutes",
        rate: spec.commerce.default_pricing.rate || "2.00",
        payment_asset: {
          chain_id: "eip155:8453",
          asset_id:
            "eip155:8453/erc20:0x833589fCD6EDB6E08f4c7C32D4f71b54bdA02913",
          symbol: "USDC",
          decimals: 6,
        },
      }
    : { model: "free" };
  const paymentEffects = enabled ? ["payment.charge"] : [];
  return {
    ...spec,
    commerce: {
      ...spec.commerce,
      enabled,
      default_pricing: pricing,
    },
    permissions: uniq([
      ...spec.permissions.filter((item) => item !== "payment.charge"),
      ...paymentEffects,
    ]),
    tools: spec.tools.map((tool) => ({
      ...tool,
      effects: uniq([
        ...(tool.effects ?? []).filter((item) => item !== "payment.charge"),
        ...paymentEffects,
      ]),
      pricing,
    })),
  };
}

function SpecReview({
  busy,
  onApprove,
  onChange,
  onDiscard,
  onSave,
  spec,
}: {
  busy: string;
  onApprove: () => void;
  onChange: (spec: AgentSpec) => void;
  onDiscard: () => void;
  onSave: () => void;
  spec: AgentSpec;
}) {
  const editable = spec.status === "draft" && !busy;
  const tool = spec.tools[0];
  const secretList = spec.secrets ?? [];
  const updateToolPricingRate = (rate: string) => {
    const next = {
      ...spec,
      commerce: {
        ...spec.commerce,
        default_pricing: {
          ...spec.commerce.default_pricing,
          rate,
        },
      },
      tools: spec.tools.map((item) => ({
        ...item,
        pricing:
          item.pricing.model === "variable"
            ? { ...item.pricing, rate }
            : item.pricing,
      })),
    };
    onChange(next);
  };

  return (
    <section
      className="space-y-4 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm"
      id="agent-spec-review"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-semibold text-on-surface">
              Spec review
            </h2>
            <span className="rounded-full bg-surface-container-high px-3 py-1 text-xs font-semibold uppercase text-secondary">
              {spec.status}
            </span>
          </div>
          <p className="max-w-3xl text-sm text-secondary">{spec.prompt}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface disabled:opacity-50"
            disabled={!editable}
            onClick={onSave}
            type="button"
          >
            <Icon name="save" className="text-base" />
            Save
          </button>
          <button
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-error transition-colors hover:bg-error-container/20 disabled:opacity-50"
            disabled={!editable}
            onClick={onDiscard}
            type="button"
          >
            <Icon name="close" className="text-base" />
            Discard
          </button>
          <button
            className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary shadow-sm transition-colors hover:bg-primary/90 disabled:opacity-50"
            disabled={!editable}
            onClick={onApprove}
            type="button"
          >
            <Icon name="check" className="text-base" />
            Approve
          </button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-[minmax(0,0.75fr)_minmax(0,1.25fr)]">
        <input
          className="min-w-0 rounded-xl border border-outline-variant/15 bg-surface px-4 py-3 text-sm font-semibold text-on-surface outline-none transition-colors focus:border-primary/35 disabled:opacity-70"
          disabled={!editable}
          onChange={(event) => onChange({ ...spec, name: event.target.value })}
          value={spec.name}
        />
        <textarea
          className="min-h-[92px] min-w-0 resize-none rounded-xl border border-outline-variant/15 bg-surface px-4 py-3 text-sm leading-6 text-on-surface outline-none transition-colors focus:border-primary/35 disabled:opacity-70"
          disabled={!editable}
          onChange={(event) =>
            onChange({ ...spec, description: event.target.value })
          }
          value={spec.description}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-3 rounded-xl bg-surface p-4">
          <h3 className="text-sm font-semibold text-on-surface">Runtime</h3>
          <div className="space-y-2 text-sm text-secondary">
            <p>
              {spec.runtime.target}
              {spec.runtime.template ? ` · ${spec.runtime.template}` : ""}
              {spec.runtime.harness ? ` · ${spec.runtime.harness}` : ""}
            </p>
            {spec.runtime.packages && spec.runtime.packages.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {spec.runtime.packages.map((item) => (
                  <span
                    className="rounded-full bg-surface-container px-2.5 py-1 text-xs"
                    key={item}
                  >
                    {item}
                  </span>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="space-y-3 rounded-xl bg-surface p-4">
          <h3 className="text-sm font-semibold text-on-surface">Tool</h3>
          <div className="space-y-2 text-sm text-secondary">
            <p className="font-mono text-xs text-on-surface">
              {tool?.name ?? "agent.run"}
            </p>
            <p>{tool?.description}</p>
            <p>
              {tool?.audience}/{tool?.scope} · {displayPricing(spec)}
            </p>
          </div>
        </div>

        <div className="space-y-3 rounded-xl bg-surface p-4">
          <h3 className="text-sm font-semibold text-on-surface">Commerce</h3>
          <label className="flex items-center gap-3 text-sm text-secondary">
            <input
              checked={spec.commerce.enabled}
              className="h-4 w-4 accent-primary"
              disabled={!editable}
              onChange={(event) =>
                onChange(applyCommerce(spec, event.target.checked))
              }
              type="checkbox"
            />
            Charge for this tool
          </label>
          {spec.commerce.enabled && (
            <input
              className="w-full rounded-xl border border-outline-variant/15 bg-surface-container px-3 py-2 text-sm text-on-surface outline-none focus:border-primary/35"
              disabled={!editable}
              onChange={(event) => updateToolPricingRate(event.target.value)}
              value={spec.commerce.default_pricing.rate ?? ""}
            />
          )}
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-3">
          <h3 className="text-sm font-semibold text-on-surface">Inputs</h3>
          {spec.inputs.map((item) => (
            <p className="rounded-xl bg-surface px-4 py-3 text-sm text-secondary" key={`${item.kind}-${item.description}`}>
              {item.description}
            </p>
          ))}
        </div>
        <div className="space-y-3">
          <h3 className="text-sm font-semibold text-on-surface">Outputs</h3>
          {spec.outputs.map((item) => (
            <p className="rounded-xl bg-surface px-4 py-3 text-sm text-secondary" key={`${item.kind}-${item.description}`}>
              {item.description}
            </p>
          ))}
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-3">
          <h3 className="text-sm font-semibold text-on-surface">Secrets</h3>
          {secretList.length === 0 ? (
            <p className="text-sm text-secondary">No provider secrets requested.</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {secretList.map((secret) => (
                <span
                  className="rounded-full bg-surface px-3 py-2 text-xs text-secondary"
                  key={`${secret.name}-${secret.env}`}
                >
                  <span className="font-mono text-on-surface">{secret.env}</span>{" "}
                  {secret.required ? "required" : "optional"}
                </span>
              ))}
            </div>
          )}
        </div>
        <div className="space-y-3">
          <h3 className="text-sm font-semibold text-on-surface">Effects</h3>
          <div className="flex flex-wrap gap-2">
            {spec.permissions.map((permission) => (
              <span
                className="rounded-full bg-surface px-3 py-2 text-xs text-secondary"
                key={permission}
              >
                {permission}
              </span>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}

export default function Agents() {
  const navigate = useNavigate();
  const { data, loading, error } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const {
    data: specData,
    error: specError,
    refetch: refetchSpecs,
  } = useRPC(() => agent.spec.list({ limit: 8 }), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const [prompt, setPrompt] = useState("");
  const [selectedSpec, setSelectedSpec] = useState<AgentSpec | null>(null);
  const [run, setRun] = useState<WorkspaceRun | null>(null);
  const [builderStatus, setBuilderStatus] = useState("");
  const [reviewBusy, setReviewBusy] = useState("");
  const [hiddenSpecIDs, setHiddenSpecIDs] = useState<Set<string>>(
    () => new Set(),
  );

  const agents = data?.agents ?? [];
  const specs = (specData?.specs ?? []).filter(
    (spec) => !hiddenSpecIDs.has(spec.id),
  );

  const deviceSet = new Set(agents.map((a) => a.device_id));

  function viewSpec(spec: AgentSpec) {
    setSelectedSpec(spec);
    window.setTimeout(() => {
      document
        .getElementById("agent-spec-review")
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, 0);
  }

  async function saveRun(nextRun: WorkspaceRun) {
    try {
      await rootAgent.runSave({ run: nextRun });
    } catch {
      setBuilderStatus("Save failed.");
    }
  }

  async function submitAssistantPrompt(nextPrompt: string) {
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
      const result = await executeRootAgentPrompt(
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
              const existing = current.toolTraces.find(
                (item) => item.id === trace.id,
              );
              const toolTraces = existing
                ? current.toolTraces.map((item) =>
                    item.id === trace.id ? trace : item,
                  )
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

  async function submitPrompt(nextPrompt: string) {
    const trimmed = nextPrompt.trim();
    if (!trimmed) return;

    setBuilderStatus("Creating spec...");
    try {
      const result = await agent.spec.create({ prompt: trimmed });
      setSelectedSpec(result.spec);
      setPrompt("");
      setBuilderStatus("Spec saved.");
      refetchSpecs({ background: true });
    } catch (submitError) {
      setBuilderStatus(
        submitError instanceof Error ? submitError.message : "Spec failed",
      );
    }
  }

  async function saveSelectedSpec() {
    if (!selectedSpec) return;
    setReviewBusy("Saving...");
    try {
      const result = await agent.spec.update({ spec: selectedSpec });
      setSelectedSpec(result.spec);
      setBuilderStatus("Spec updated.");
      refetchSpecs({ background: true });
    } catch (saveError) {
      setBuilderStatus(
        saveError instanceof Error ? saveError.message : "Save failed",
      );
    } finally {
      setReviewBusy("");
    }
  }

  async function approveSelectedSpec() {
    if (!selectedSpec) return;
    setReviewBusy("Approving...");
    try {
      const result = await agent.spec.approve({ id: selectedSpec.id });
      setSelectedSpec(result.spec);
      setBuilderStatus("Spec approved.");
      refetchSpecs({ background: true });
    } catch (approveError) {
      setBuilderStatus(
        approveError instanceof Error ? approveError.message : "Approve failed",
      );
    } finally {
      setReviewBusy("");
    }
  }

  async function discardSelectedSpec() {
    if (!selectedSpec) return;
    setReviewBusy("Discarding...");
    try {
      const result = await agent.spec.discard({ id: selectedSpec.id });
      setSelectedSpec(result.spec);
      setBuilderStatus("Spec discarded.");
      refetchSpecs({ background: true });
    } catch (discardError) {
      setBuilderStatus(
        discardError instanceof Error ? discardError.message : "Discard failed",
      );
    } finally {
      setReviewBusy("");
    }
  }

  async function deleteSpec(spec: AgentSpec) {
    setHiddenSpecIDs((current) => {
      const next = new Set(current);
      next.add(spec.id);
      return next;
    });
    if (selectedSpec?.id === spec.id) {
      setSelectedSpec(null);
    }
    setReviewBusy("Deleting...");
    try {
      await agent.spec.delete({ id: spec.id });
      setBuilderStatus("Spec deleted.");
      refetchSpecs({ background: true });
    } catch (deleteError) {
      setHiddenSpecIDs((current) => {
        const next = new Set(current);
        next.delete(spec.id);
        return next;
      });
      setBuilderStatus(
        deleteError instanceof Error ? deleteError.message : "Delete failed",
      );
    } finally {
      setReviewBusy("");
    }
  }

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 px-6 pb-12 pt-6 sm:px-8 sm:pt-7 lg:px-10">
      {(error || specError) && (
        <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
          {error || specError}
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
          {(builderStatus || reviewBusy) && (
            <p className="truncate text-xs text-secondary">
              {reviewBusy || builderStatus}
            </p>
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
              placeholder={MEDIA_ACCENT_PROMPT}
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
                Create agent
              </button>
              <button
                className="inline-flex shrink-0 items-center justify-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container disabled:opacity-50"
                disabled={!prompt.trim()}
                onClick={() => void submitAssistantPrompt(prompt)}
                type="button"
              >
                <Icon name="auto_awesome" className="text-base" />
                Ask AI
              </button>
            </div>
          </div>
        </div>

        {run && <RunCard onPromptSelect={setPrompt} run={run} />}

        {selectedSpec && (
          <SpecReview
            busy={reviewBusy}
            onApprove={() => void approveSelectedSpec()}
            onChange={setSelectedSpec}
            onDiscard={() => void discardSelectedSpec()}
            onSave={() => void saveSelectedSpec()}
            spec={selectedSpec}
          />
        )}

        {specs.length > 0 && (
          <div className="space-y-3">
            <h2 className="text-lg font-semibold text-on-surface">
              Recent specs
            </h2>
            <div className="grid gap-3 md:grid-cols-2">
              {specs.map((specItem) => (
                <div
                  className={`min-w-0 rounded-xl bg-surface-container-lowest p-4 text-left shadow-sm ring-1 transition-colors ${
                    selectedSpec?.id === specItem.id
                      ? "ring-primary/35"
                      : "ring-outline-variant/10 hover:ring-primary/20"
                  }`}
                  key={specItem.id}
                >
                  <div className="mb-2 flex items-center justify-between gap-3">
                    <p className="truncate text-sm font-semibold text-on-surface">
                      {specItem.name}
                    </p>
                    <span className="rounded-full bg-surface-container px-2.5 py-1 text-[10px] font-bold uppercase text-secondary">
                      {specItem.status}
                    </span>
                  </div>
                  <p className="line-clamp-2 text-xs leading-5 text-secondary">
                    {specItem.description}
                  </p>
                  <div className="mt-4 flex flex-wrap justify-end gap-2">
                    <button
                      className="inline-flex items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-1.5 text-xs font-semibold text-on-surface transition-colors hover:bg-surface-container"
                      onClick={() => viewSpec(specItem)}
                      type="button"
                    >
                      <Icon name="visibility" className="text-sm" />
                      View
                    </button>
                    <button
                      className="inline-flex items-center gap-1.5 rounded-full border border-outline-variant/20 px-3 py-1.5 text-xs font-semibold text-error transition-colors hover:bg-error-container/20 disabled:opacity-50"
                      disabled={reviewBusy === "Deleting..."}
                      onClick={() => void deleteSpec(specItem)}
                      type="button"
                    >
                      <Icon name="delete" className="text-sm" />
                      Delete
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
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

      <div className="border-t border-outline-variant/10 pt-8">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="text-2xl font-bold tracking-tight text-on-surface">
            sky10 Network
          </h2>
          <button
            onClick={() => navigate("/agents/connect")}
            className="flex items-center gap-1 text-sm font-medium text-primary hover:underline"
          >
            <Icon name="add" className="text-base" />
            Connect Existing...
          </button>
        </div>
        <p className="text-sm text-secondary">
          Browse agents on the sky10 network. Coming soon.
        </p>
      </div>
    </section>
  );
}
