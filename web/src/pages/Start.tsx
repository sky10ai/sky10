import { Link } from "react-router";
import { Icon } from "../components/Icon";

const OPTIONS = [
  {
    id: "for_me",
    title: "For me",
    subtitle: "Automate your daily life",
    examples: [
      "Safely review Slack and email and draft follow-ups",
      "Summarize files, notes, and meeting recordings daily",
      "Intelligently create photo albums from family moments",
    ],
    icon: "person",
    accent: "from-primary/18 via-primary/8 to-transparent",
    href: "/ai?audience=for_me",
    label: "Private",
  },
  {
    id: "for_others",
    title: "For others",
    subtitle: "Create services people can use",
    examples: [
      "Transcribe interviews and charge clients per upload",
      "Analyze support tickets and draft client-ready updates",
      "Turn podcasts into shorts for paying customers",
    ],
    icon: "groups",
    accent: "from-secondary-container/75 via-primary/8 to-transparent",
    href: "/ai?audience=for_others",
    label: "Service",
  },
] as const;

export default function Start() {
  return (
    <section className="relative flex min-h-screen items-center justify-center overflow-hidden bg-surface px-6 py-10 sm:px-10">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(19,147,123,0.12),transparent_34%),radial-gradient(circle_at_bottom_right,rgba(0,112,235,0.1),transparent_30%)]" />

      <div className="relative w-full max-w-5xl">
        <div className="mx-auto flex w-fit flex-col items-center gap-3">
          <div className="flex h-16 w-16 items-center justify-center rounded-[1.4rem] text-white shadow-lg shadow-primary/15 lithic-gradient">
            <Icon className="text-[34px]" filled name="cloud" />
          </div>
          <p className="text-sm font-semibold tracking-[0.22em] text-on-surface">
            sky10
          </p>
        </div>

        <h1 className="mx-auto mt-6 max-w-3xl text-center text-4xl font-semibold tracking-tight text-on-surface sm:text-5xl">
          Who is the agent for?
        </h1>

        <div className="mt-10 grid gap-5 lg:grid-cols-2">
          {OPTIONS.map((option) => (
            <Link
              key={option.id}
              className="group relative overflow-hidden rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm transition-all hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-md sm:p-10"
              to={option.href}
            >
              <div className={`pointer-events-none absolute inset-0 bg-gradient-to-br ${option.accent} opacity-90 transition-opacity group-hover:opacity-100`} />

              <div className="relative flex h-full flex-col">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-surface text-primary shadow-sm">
                    <Icon className="text-[28px]" name={option.icon} />
                  </div>
                  <span className="rounded-full bg-surface px-3 py-1 text-[10px] font-bold uppercase tracking-[0.18em] text-secondary">
                    {option.label}
                  </span>
                </div>

                <div className="mt-8">
                  <h2 className="text-3xl font-semibold tracking-tight text-on-surface">
                    {option.title}
                  </h2>
                  <p className="mt-2 text-base text-secondary">
                    {option.subtitle}
                  </p>
                </div>

                <div className="mt-6 space-y-2 text-sm leading-6 text-secondary">
                  {option.examples.map((example) => (
                    <p key={example}>• {example}</p>
                  ))}
                </div>

                <div className="mt-8 inline-flex items-center gap-2 self-start rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg shadow-primary/20 transition-colors group-hover:bg-primary/90">
                  Create {option.title.toLowerCase()}
                  <Icon className="text-base transition-transform group-hover:translate-x-0.5" name="arrow_forward" />
                </div>
              </div>
            </Link>
          ))}
        </div>
      </div>
    </section>
  );
}
