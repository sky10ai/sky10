import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

export type ThemePreference = "system" | "light" | "dark";
export type ResolvedTheme = "light" | "dark";

const THEME_STORAGE_KEY = "sky10:theme";

function isThemePreference(value: string | null): value is ThemePreference {
  return value === "system" || value === "light" || value === "dark";
}

function getSystemTheme(): ResolvedTheme {
  if (typeof window === "undefined") return "light";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function resolveTheme(
  preference: ThemePreference,
  systemTheme: ResolvedTheme,
): ResolvedTheme {
  return preference === "system" ? systemTheme : preference;
}

function getInitialPreference(): ThemePreference {
  if (typeof document !== "undefined") {
    const current = document.documentElement.dataset.themePreference ?? null;
    if (isThemePreference(current)) return current;
  }

  if (typeof window !== "undefined") {
    try {
      const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
      if (isThemePreference(stored)) return stored;
    } catch {
      // Ignore localStorage read errors.
    }
  }

  return "system";
}

function applyTheme(preference: ThemePreference, systemTheme: ResolvedTheme) {
  const resolved = resolveTheme(preference, systemTheme);
  const root = document.documentElement;
  root.classList.toggle("dark", resolved === "dark");
  root.dataset.themePreference = preference;
  root.dataset.theme = resolved;
  root.style.colorScheme = resolved;
  return resolved;
}

interface ThemeContextValue {
  preference: ThemePreference;
  resolvedTheme: ResolvedTheme;
  setPreference: (preference: ThemePreference) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreference] = useState<ThemePreference>(getInitialPreference);
  const [systemTheme, setSystemTheme] = useState<ResolvedTheme>(getSystemTheme);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (event: MediaQueryListEvent) => {
      setSystemTheme(event.matches ? "dark" : "light");
    };

    setSystemTheme(media.matches ? "dark" : "light");
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);

  useEffect(() => {
    applyTheme(preference, systemTheme);

    try {
      if (preference === "system") {
        window.localStorage.removeItem(THEME_STORAGE_KEY);
      } else {
        window.localStorage.setItem(THEME_STORAGE_KEY, preference);
      }
    } catch {
      // Ignore localStorage write errors.
    }
  }, [preference, systemTheme]);

  const value = useMemo<ThemeContextValue>(
    () => ({
      preference,
      resolvedTheme: resolveTheme(preference, systemTheme),
      setPreference,
    }),
    [preference, systemTheme],
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  const context = useContext(ThemeContext);
  if (!context) {
    throw new Error("useTheme must be used within ThemeProvider");
  }
  return context;
}
