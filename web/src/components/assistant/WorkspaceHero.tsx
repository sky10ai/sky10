import { Icon } from "../Icon";
import { StatusBadge } from "../StatusBadge";
import { AUDIENCE_OPTIONS, type AgentAudience } from "./workspaceTypes";

interface WorkspaceHeroProps {
  audience: AgentAudience;
  onAudienceChange: (value: AgentAudience) => void;
  onPromptSelect: (value: string) => void;
  onSubmit: () => void;
  placeholder: string;
  prompt: string;
  runCount: number;
  setPrompt: (value: string) => void;
  statusLine: string;
  suggestions: readonly string[];
}

export function WorkspaceHero({
  audience,
  onAudienceChange,
  onPromptSelect,
  onSubmit,
  placeholder,
  prompt,
  runCount,
  setPrompt,
  statusLine,
  suggestions,
}: WorkspaceHeroProps) {
  return (
    <section className="relative overflow-hidden rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(19,147,123,0.18),transparent_34%),radial-gradient(circle_at_bottom_right,rgba(232,180,64,0.16),transparent_30%)]" />
      <div className="relative space-y-6">
        <div className="max-w-3xl space-y-3">
          <div className="inline-flex items-center gap-2 rounded-full bg-surface-container px-3 py-1 text-[10px] font-bold uppercase tracking-[0.22em] text-secondary">
            <Icon className="text-sm text-primary" name="auto_awesome" />
            Root Assistant
          </div>
          <h2 className="text-3xl font-semibold tracking-tight text-on-surface sm:text-[2.5rem]">
            What kind of agent do you want to create?
          </h2>
          <p className="max-w-2xl text-sm text-secondary sm:text-base">
            Pick whether the agent is for your own automation or for serving
            other people. The assistant uses that choice to frame the draft,
            risk model, and next questions.
          </p>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <StatusBadge icon="visibility" tone="processing">
              Read-Only MVP
            </StatusBadge>
            <StatusBadge pulse tone="live">
              Live RPC
            </StatusBadge>
            <span className="text-secondary">{statusLine}</span>
          </div>
        </div>

        <div className="grid gap-3 md:grid-cols-2">
          {AUDIENCE_OPTIONS.map((option) => {
            const active = option.id === audience;
            return (
              <button
                key={option.id}
                className={`rounded-[1.5rem] border px-5 py-4 text-left transition-colors ${
                  active
                    ? "border-primary/30 bg-primary/10 shadow-sm"
                    : "border-outline-variant/10 bg-surface hover:border-primary/15 hover:bg-surface-container"
                }`}
                onClick={() => onAudienceChange(option.id)}
                type="button"
              >
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="text-lg font-semibold text-on-surface">
                      {option.title}
                    </p>
                    <p className="mt-2 text-sm text-secondary">
                      {option.description}
                    </p>
                  </div>
                  {active && <StatusBadge tone="processing">Selected</StatusBadge>}
                </div>
              </button>
            );
          })}
        </div>

        <div className="rounded-[1.75rem] border border-outline-variant/10 bg-surface p-5 shadow-sm">
          <label className="mb-3 block text-[10px] font-bold uppercase tracking-[0.22em] text-outline">
            {audience === "for_me"
              ? "What do you want an agent to handle for you?"
              : "What do you want an agent to offer to others?"}
          </label>
          <div className="flex flex-col gap-4">
            <textarea
              className="min-h-[128px] resize-none rounded-[1.4rem] border border-outline-variant/20 bg-surface-container-lowest px-5 py-4 text-sm leading-6 text-on-surface outline-none transition-colors focus:border-primary/35"
              onChange={(event) => setPrompt(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault();
                  onSubmit();
                }
              }}
              placeholder={placeholder}
              value={prompt}
            />
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex flex-wrap gap-2">
                {suggestions.slice(0, 3).map((suggestion) => (
                  <button
                    key={suggestion}
                    className="rounded-full border border-outline-variant/20 bg-surface-container-low px-3 py-2 text-xs text-secondary transition-colors hover:border-primary/20 hover:text-on-surface"
                    onClick={() => onPromptSelect(suggestion)}
                    type="button"
                  >
                    {suggestion}
                  </button>
                ))}
              </div>
              <button
                className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg shadow-primary/20 transition-colors hover:bg-primary/90 disabled:opacity-50"
                disabled={!prompt.trim()}
                onClick={onSubmit}
                type="button"
              >
                <Icon className="text-base" name="send" />
                Draft with sky10
              </button>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2 text-[11px] text-secondary">
          <span>{runCount} saved task{runCount === 1 ? "" : "s"}</span>
          <span className="text-outline">•</span>
          <span>7 inspection tools</span>
          <span className="text-outline">•</span>
          <span>Submit with `⌘/Ctrl + Enter`</span>
        </div>
      </div>
    </section>
  );
}
