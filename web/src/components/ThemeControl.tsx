import { useEffect, useRef, useState } from "react";
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

function themeButtonIcon(preference: ThemePreference) {
  return themeOptions.find((option) => option.value === preference)?.icon ?? "brightness_auto";
}

function themeButtonTitle(preference: ThemePreference, resolvedTheme: "light" | "dark") {
  if (preference === "system") {
    return `Theme follows your system appearance. Current appearance: ${resolvedTheme}. Click to choose System, Light, or Dark.`;
  }

  return `Theme is locked to ${preference}. Click to switch themes or return to System.`;
}

export function ThemeControl() {
  const { preference, resolvedTheme, setPreference } = useTheme();
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!open) return;

    const onPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };

    const onEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };

    window.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("keydown", onEscape);
    return () => {
      window.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("keydown", onEscape);
    };
  }, [open]);

  return (
    <div className="relative" ref={containerRef}>
      <button
        aria-label={themeButtonTitle(preference, resolvedTheme)}
        aria-expanded={open}
        aria-haspopup="menu"
        className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 bg-surface-container-high px-3 py-2 text-xs font-medium text-on-surface transition-colors hover:border-primary/20 hover:bg-surface-container-highest"
        onClick={() => setOpen((current) => !current)}
        title={themeButtonTitle(preference, resolvedTheme)}
        type="button"
      >
        <Icon className="text-base text-primary" name={themeButtonIcon(preference)} />
        <Icon className={`text-sm text-outline transition-transform ${open ? "rotate-180" : ""}`} name="expand_more" />
      </button>

      {open && (
        <div className="absolute right-0 top-full z-50 mt-2 w-64 overflow-hidden rounded-2xl border border-outline-variant/20 bg-surface-container-lowest p-1 shadow-2xl">
          {themeOptions.map((option) => {
            const selected = preference === option.value;
            return (
              <button
                className={`flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors ${
                  selected
                    ? "bg-primary/10 text-on-surface"
                    : "text-on-surface hover:bg-surface-container-high"
                }`}
                key={option.value}
                onClick={() => {
                  setPreference(option.value);
                  setOpen(false);
                }}
                type="button"
              >
                <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface-container-high text-primary">
                  <Icon className="text-lg" name={option.icon} />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-sm font-semibold">{option.label}</span>
                    {selected && <Icon className="text-base text-primary" name="check" />}
                  </div>
                  <p className="mt-0.5 text-xs text-secondary">{option.description}</p>
                </div>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
