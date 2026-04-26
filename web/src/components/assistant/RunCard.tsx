import { StatusBadge } from "../StatusBadge";
import { timeAgo } from "../../lib/useRPC";
import { runTone, toolTone, type WorkspaceRun } from "./workspaceTypes";

interface RunCardProps {
  onPromptSelect: (value: string) => void;
  run: WorkspaceRun;
}

export function RunCard({ onPromptSelect, run }: RunCardProps) {
  return (
    <article className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 max-w-3xl space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Draft
            </p>
          </div>
          <h3 className="text-base font-semibold leading-6 text-on-surface">
            {run.prompt}
          </h3>
        </div>
        <div className="flex flex-wrap gap-2">
          <StatusBadge tone={runTone(run.status)}>{run.status}</StatusBadge>
          <StatusBadge tone="neutral">{timeAgo(run.updatedAt)}</StatusBadge>
        </div>
      </div>

      <div className="mt-5 border-t border-outline-variant/10 pt-5">
        <div className="whitespace-pre-wrap text-sm leading-7 text-on-surface">
          {run.answer || "..."}
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

        <details className="mt-5 rounded-2xl border border-outline-variant/10 bg-surface px-4 py-3">
          <summary className="cursor-pointer text-sm font-semibold text-on-surface">
            Trace
          </summary>
          <div className="mt-4 space-y-3">
            {run.toolTraces.length === 0 ? (
              <p className="text-sm text-secondary">...</p>
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
        </details>
      </div>
    </article>
  );
}
