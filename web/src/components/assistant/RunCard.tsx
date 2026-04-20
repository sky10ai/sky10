import { StatusBadge } from "../StatusBadge";
import { timeAgo } from "../../lib/useRPC";
import { audienceLabel, runTone, toolTone, type WorkspaceRun } from "./workspaceTypes";

interface RunCardProps {
  onPromptSelect: (value: string) => void;
  run: WorkspaceRun;
}

export function RunCard({ onPromptSelect, run }: RunCardProps) {
  return (
    <article className="rounded-[1.75rem] border border-outline-variant/10 bg-surface p-5 shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="max-w-3xl space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Request
            </p>
            <StatusBadge tone="neutral">{audienceLabel(run.audience)}</StatusBadge>
          </div>
          <h3 className="text-lg font-semibold text-on-surface">{run.prompt}</h3>
        </div>
        <div className="flex flex-wrap gap-2">
          <StatusBadge tone={runTone(run.status)}>{run.status}</StatusBadge>
          <StatusBadge tone="neutral">{timeAgo(run.updatedAt)}</StatusBadge>
        </div>
      </div>

      <div className="mt-5 grid gap-4 lg:grid-cols-[minmax(0,1.2fr)_minmax(300px,0.9fr)]">
        <div className="rounded-[1.5rem] border border-outline-variant/10 bg-surface-container-low p-5">
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Assistant Answer
          </p>
          <div className="mt-3 whitespace-pre-wrap text-sm leading-7 text-on-surface">
            {run.answer || "Inspecting the node..."}
          </div>
          {run.followUps && run.followUps.length > 0 && (
            <div className="mt-4 flex flex-wrap gap-2">
              {run.followUps.map((item) => (
                <button
                  key={item}
                  className="rounded-full border border-outline-variant/20 bg-surface px-3 py-2 text-xs text-secondary transition-colors hover:border-primary/20 hover:text-on-surface"
                  onClick={() => onPromptSelect(item)}
                  type="button"
                >
                  {item}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="rounded-[1.5rem] border border-outline-variant/10 bg-surface-container-low p-5">
          <div className="flex items-center justify-between gap-3">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Tool Trace
            </p>
            <StatusBadge tone="neutral">
              {run.toolTraces.length} call{run.toolTraces.length === 1 ? "" : "s"}
            </StatusBadge>
          </div>
          <div className="mt-4 space-y-3">
            {run.toolTraces.length === 0 ? (
              <p className="text-sm text-secondary">Waiting for the first tool call.</p>
            ) : (
              run.toolTraces.map((trace) => (
                <div
                  key={trace.id}
                  className="rounded-2xl border border-outline-variant/10 bg-surface px-4 py-3"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <p className="text-sm font-semibold text-on-surface">
                        {trace.title}
                      </p>
                      <p className="mt-1 font-mono text-[11px] text-secondary">
                        {trace.tool} → {trace.rpcMethod}
                      </p>
                    </div>
                    <StatusBadge tone={toolTone(trace.status)}>
                      {trace.status}
                    </StatusBadge>
                  </div>
                  <p className="mt-3 text-sm text-secondary">{trace.detail}</p>
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </article>
  );
}
