import { Icon } from "./Icon";
import { useTheme, type ThemePreference } from "./ThemeProvider";

const themeOptions: Array<{
  description: string;
  icon: string;
  label: string;
  value: ThemePreference;
}> = [
  {
    value: "system",
    label: "System",
    description: "Follow the current OS appearance.",
    icon: "brightness_auto",
  },
  {
    value: "light",
    label: "Light",
    description: "Always use the light interface.",
    icon: "light_mode",
  },
  {
    value: "dark",
    label: "Dark",
    description: "Always use the dark interface.",
    icon: "dark_mode",
  },
];

function themeButtonLabel(preference: ThemePreference) {
  return themeOptions.find((option) => option.value === preference)?.label ?? "System";
}

function resolvedThemeLabel(preference: ThemePreference, resolvedTheme: "light" | "dark") {
  if (preference === "system") {
    return `Following system appearance: ${resolvedTheme}`;
  }

  return `Locked to ${preference}`;
}

export function ThemeControl() {
  const { preference, resolvedTheme, setPreference } = useTheme();

  return (
    <div className="grid gap-4 md:grid-cols-3">
      {themeOptions.map((option) => {
        const selected = preference === option.value;
        return (
          <button
            aria-pressed={selected}
            className={`group flex min-h-52 flex-col rounded-3xl border p-6 text-left transition-all ${
              selected
                ? "border-primary/30 bg-primary/10 shadow-[0_24px_48px_-36px_rgba(37,99,235,0.6)]"
                : "border-outline-variant/10 bg-surface-container-lowest hover:-translate-y-0.5 hover:border-primary/20 hover:shadow-lg"
            }`}
            key={option.value}
            onClick={() => setPreference(option.value)}
            type="button"
          >
            <div className="flex items-start justify-between gap-4">
              <div className={`flex h-12 w-12 items-center justify-center rounded-2xl ${
                selected
                  ? "bg-primary text-white"
                  : "bg-surface-container text-primary"
              }`}>
                <Icon className="text-2xl" name={option.icon} />
              </div>
              <span
                className={`inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-[10px] font-bold uppercase tracking-[0.18em] ${
                  selected
                    ? "bg-primary text-white"
                    : "bg-surface-container-high text-outline"
                }`}
              >
                {selected ? "Selected" : "Available"}
              </span>
            </div>

            <div className="mt-6 space-y-2">
              <h3 className="text-xl font-semibold text-on-surface">{option.label}</h3>
              <p className="text-sm text-secondary">{option.description}</p>
            </div>

            <div className="mt-auto pt-8">
              <p className="text-xs font-medium text-outline">
                {option.value === "system"
                  ? resolvedThemeLabel(option.value, resolvedTheme)
                  : selected
                    ? resolvedThemeLabel(option.value, resolvedTheme)
                    : `Switch from ${themeButtonLabel(preference)} to ${option.label}`}
              </p>
            </div>
          </button>
        );
      })}
    </div>
  );
}
