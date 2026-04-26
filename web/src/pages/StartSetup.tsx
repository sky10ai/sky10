import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router";
import { Icon } from "../components/Icon";
import { StatusBadge } from "../components/StatusBadge";

type AgentAudience = "for_me" | "for_others";

const AUDIENCE_META: Record<AgentAudience, { label: string; subtitle: string }> = {
  for_me: {
    label: "For me",
    subtitle: "Automate your daily life",
  },
  for_others: {
    label: "For others",
    subtitle: "Create services people can use",
  },
};

function parseAudience(value: string | null): AgentAudience {
  return value === "for_others" ? "for_others" : "for_me";
}

function transitionDelayMs() {
  if (
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches
  ) {
    return 0;
  }
  return 420;
}

export default function StartSetup() {
  const [searchParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const audience = parseAudience(searchParams.get("audience"));
  const meta = AUDIENCE_META[audience];
  const [showOther, setShowOther] = useState(false);
  const [leavingBack, setLeavingBack] = useState(false);
  const timerRef = useRef<number | null>(null);
  const [revealCompensation, setRevealCompensation] = useState(0);
  const otherTriggerRef = useRef<HTMLButtonElement | null>(null);
  const anchorTopRef = useRef<number | null>(null);
  const shouldAnimateIn = Boolean(location.state && typeof location.state === "object" && "fromStart" in location.state);

  useEffect(() => {
    return () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    };
  }, []);

  useLayoutEffect(() => {
    if (anchorTopRef.current === null || !otherTriggerRef.current) {
      return;
    }
    const nextTop = otherTriggerRef.current.getBoundingClientRect().top;
    const delta = anchorTopRef.current - nextTop;
    anchorTopRef.current = null;
    if (Math.abs(delta) < 0.5) return;
    setRevealCompensation((current) => current + delta);
  }, [showOther]);

  const links = useMemo(
    () => ({
      chatgpt: `/settings/codex?audience=${audience}`,
      lookAround: `/agents`,
      apiKey: `/settings/secrets?kind=api-key&audience=${audience}#store-secret`,
      local: `/settings/apps?audience=${audience}`,
      wallet: `/settings/wallet`,
    }),
    [audience]
  );
  const otherOptions = useMemo(
    () => [
      {
        description: "Bring your own provider.",
        href: links.apiKey,
        title: "API key",
      },
      {
        description: "Set up local runtimes.",
        href: links.local,
        title: "Local LLM",
      },
      {
        description: "Prepare x402-style payments.",
        href: links.wallet,
        title: "Fund wallet",
      },
    ],
    [links.apiKey, links.local, links.wallet]
  );

  function handleBack() {
    if (leavingBack) return;
    setLeavingBack(true);
    timerRef.current = window.setTimeout(() => {
      navigate("/start", {
        state: { audience, fromSetup: true },
      });
    }, transitionDelayMs());
  }

  function handleToggleOther() {
    anchorTopRef.current = otherTriggerRef.current?.getBoundingClientRect().top ?? null;
    setShowOther((current) => !current);
  }

  return (
    <section className="relative flex min-h-screen items-start justify-center overflow-x-hidden bg-surface px-6 py-10 sm:items-center sm:px-10 sm:py-12">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(19,147,123,0.12),transparent_34%),radial-gradient(circle_at_bottom_right,rgba(0,112,235,0.1),transparent_30%)]" />

      <div
        className={`relative w-full max-w-3xl onboarding-stage ${
          leavingBack
            ? "onboarding-page-turn-out-reverse"
            : shouldAnimateIn
              ? "onboarding-page-turn-in"
              : ""
        }`}
      >
        <div
          style={revealCompensation > 0 ? { transform: `translateY(${revealCompensation}px)` } : undefined}
        >
          <div className="mx-auto flex w-fit flex-col items-center gap-3">
            <div className="flex h-16 w-16 items-center justify-center rounded-[1.4rem] text-white shadow-lg shadow-primary/15 lithic-gradient">
              <Icon className="text-[34px]" filled name="cloud" />
            </div>
            <p className="text-sm font-semibold tracking-[0.22em] text-on-surface">
              sky10
            </p>
          </div>

          <div className="mt-8 flex items-center justify-between gap-4">
            <button
              className="inline-flex items-center gap-2 text-sm font-medium text-secondary transition-colors hover:text-on-surface"
              onClick={handleBack}
              type="button"
            >
              <Icon className="text-base" name="arrow_back" />
              Back
            </button>
            <StatusBadge tone="neutral">
              {meta.label} · {meta.subtitle}
            </StatusBadge>
          </div>

          <div className="mt-6 rounded-[2.4rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm sm:p-10">
            <div className="mx-auto max-w-2xl text-center">
              <h1 className="text-4xl font-semibold tracking-tight text-on-surface sm:text-5xl">
                How should we set up your agent?
              </h1>
              <p className="mt-3 text-base text-secondary">
                Start simple, then switch later if you want more control.
              </p>
            </div>

            <div className="mt-8 flex flex-col gap-4">
              <Link
                className="group flex items-center justify-between gap-4 rounded-[1.75rem] border border-outline-variant/10 bg-surface px-6 py-5 text-left transition-all hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-sm"
                to={links.chatgpt}
              >
                <div className="flex items-center gap-4">
                  <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                    <Icon className="text-[26px]" name="chat" />
                  </div>
                  <div>
                    <p className="text-lg font-semibold text-on-surface">Connect ChatGPT</p>
                    <p className="mt-1 text-sm text-secondary">Fastest way to power your first agent.</p>
                  </div>
                </div>
                <Icon className="text-secondary transition-transform group-hover:translate-x-0.5 group-hover:text-on-surface" name="arrow_forward" />
              </Link>

              <div className="rounded-[1.75rem] border border-outline-variant/10 bg-surface px-6 py-5">
                <button
                  ref={otherTriggerRef}
                  className="group flex w-full items-center justify-between gap-4 text-left"
                  onClick={handleToggleOther}
                  type="button"
                >
                  <div className="flex items-center gap-4">
                    <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-surface-container text-primary">
                      <Icon className="text-[26px]" name="tune" />
                    </div>
                    <div>
                      <p className="text-lg font-semibold text-on-surface">Other...</p>
                      <p className="mt-1 text-sm text-secondary">API key, local LLM, or wallet setup.</p>
                    </div>
                  </div>
                  <Icon
                    className={`text-secondary transition-all group-hover:text-on-surface ${showOther ? "rotate-90" : ""}`}
                    name="chevron_right"
                  />
                </button>
              </div>

              {showOther && (
                <div className="onboarding-reveal grid gap-3 sm:grid-cols-3">
                  {otherOptions.map((option) => (
                    <Link
                      key={option.title}
                      className="rounded-2xl border border-outline-variant/10 bg-surface-container-low px-4 py-4 text-left transition-colors hover:border-primary/20 hover:bg-surface will-change-transform"
                      to={option.href}
                    >
                      <p className="font-semibold text-on-surface">{option.title}</p>
                      <p className="mt-1 text-sm text-secondary">{option.description}</p>
                    </Link>
                  ))}
                </div>
              )}

              <Link
                className="group flex items-center justify-between gap-4 rounded-[1.75rem] border border-outline-variant/10 bg-surface px-6 py-5 text-left transition-all hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-sm"
                to={links.lookAround}
              >
                <div className="flex items-center gap-4">
                  <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-surface-container text-primary">
                    <Icon className="text-[26px]" name="explore" />
                  </div>
                  <div>
                    <p className="text-lg font-semibold text-on-surface">Skip, just look around</p>
                    <p className="mt-1 text-sm text-secondary">See the app first and wire providers later.</p>
                  </div>
                </div>
                <Icon className="text-secondary transition-transform group-hover:translate-x-0.5 group-hover:text-on-surface" name="arrow_forward" />
              </Link>
            </div>
          </div>
        </div>
      </div>

      <div aria-hidden="true" className="pointer-events-none fixed left-[-200vw] top-0 opacity-0">
        <div className="grid w-[40rem] gap-3 sm:grid-cols-3">
          {otherOptions.map((option) => (
            <div
              key={`warm-${option.title}`}
              className="rounded-2xl border border-outline-variant/10 bg-surface-container-low px-4 py-4 text-left will-change-transform"
            >
              <p className="font-semibold text-on-surface">{option.title}</p>
              <p className="mt-1 text-sm text-secondary">{option.description}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
