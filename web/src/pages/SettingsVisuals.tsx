import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { ThemeControl } from "../components/ThemeControl";
import { useTheme, type ThemePreference } from "../components/ThemeProvider";

function preferenceSummary(preference: ThemePreference) {
  if (preference === "system") {
    return "System-controlled";
  }

  return `${preference.charAt(0).toUpperCase()}${preference.slice(1)} locked`;
}

export default function SettingsVisuals() {
  const { preference, resolvedTheme } = useTheme();

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-10 p-12">
      <PageHeader
        eyebrow="Settings"
        title="Visuals"
        description="Choose whether sky10 follows your system theme or stays fixed in light or dark mode."
      />

      <section className="grid gap-6 xl:grid-cols-[minmax(0,2.2fr)_minmax(20rem,1fr)]">
        <div className="space-y-6 rounded-[2rem] border border-outline-variant/10 bg-surface-container/40 p-8 shadow-sm">
          <div className="space-y-2">
            <p className="text-[10px] font-bold uppercase tracking-[0.22em] text-outline">
              Appearance
            </p>
            <h2 className="text-2xl font-semibold text-on-surface">
              Theme mode
            </h2>
            <p className="max-w-2xl text-sm text-secondary">
              Pick a fixed appearance for sky10, or leave it on System to mirror the OS-level light or dark preference automatically.
            </p>
          </div>

          <ThemeControl />
        </div>

        <aside className="space-y-6 rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
          <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary">
            <Icon className="text-3xl" name="palette" />
          </div>

          <div className="space-y-2">
            <p className="text-[10px] font-bold uppercase tracking-[0.22em] text-outline">
              Current state
            </p>
            <h2 className="text-2xl font-semibold text-on-surface">
              {preferenceSummary(preference)}
            </h2>
            <p className="text-sm text-secondary">
              The interface is currently rendering in <span className="font-semibold text-on-surface">{resolvedTheme}</span> mode.
            </p>
          </div>

          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container p-5">
            <p className="text-sm font-semibold text-on-surface">Behavior</p>
            <p className="mt-2 text-sm text-secondary">
              System mode switches automatically with your OS preference. Light and Dark stay fixed until you change them here.
            </p>
          </div>
        </aside>
      </section>
    </div>
  );
}
