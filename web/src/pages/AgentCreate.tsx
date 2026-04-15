import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";

const OPTIONS = [
  {
    id: "openclaw",
    icon: "deployed_code",
    title: "OpenClaw + Lima",
    description: "Managed Ubuntu VM with guest-local sky10, OpenClaw, browser tooling, and automatic local agent registration.",
    action: "Create OpenClaw",
    detail: "Best when you want a sky10-connected agent immediately after provisioning.",
  },
  {
    id: "hermes",
    icon: "terminal",
    title: "Hermes + Lima",
    description: "Managed Ubuntu VM with Hermes Agent preconfigured from shared secrets and launched directly in the embedded terminal.",
    action: "Create Hermes",
    detail: "Best when you want to work in Hermes's native TUI without extra shell setup.",
  },
] as const;

export default function AgentCreate() {
  const navigate = useNavigate();

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-1 flex-col gap-8 p-12">
      <div className="flex items-center gap-4">
        <button
          className="text-secondary transition-colors hover:text-on-surface"
          onClick={() => navigate("/agents")}
          type="button"
        >
          <Icon name="arrow_back" />
        </button>
        <div className="space-y-1">
          <h1 className="text-3xl font-bold tracking-tight text-on-surface">
            Create Agent
          </h1>
          <p className="text-sm text-secondary">
            Pick a managed runtime and sky10 will take you to the right sandbox template.
          </p>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        {OPTIONS.map((option) => (
          <button
            key={option.id}
            className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-8 text-left shadow-sm transition-all hover:border-primary/20 hover:bg-surface-container hover:shadow-md"
            onClick={() => navigate(`/settings/sandboxes?template=${option.id}`)}
            type="button"
          >
            <div className="flex items-start justify-between gap-4">
              <div className="space-y-4">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                  <Icon className="text-2xl" name={option.icon} />
                </div>
                <div className="space-y-2">
                  <h2 className="text-2xl font-semibold text-on-surface">{option.title}</h2>
                  <p className="text-sm text-secondary">{option.description}</p>
                </div>
                <p className="text-xs font-medium uppercase tracking-[0.18em] text-outline">
                  {option.detail}
                </p>
              </div>
              <Icon className="text-secondary" name="arrow_forward" />
            </div>

            <div className="mt-8 inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg">
              <Icon className="text-base" name="add" />
              {option.action}
            </div>
          </button>
        ))}
      </div>
    </div>
  );
}
