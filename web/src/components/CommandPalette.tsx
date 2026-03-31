import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { Icon } from "./Icon";

interface Command {
  id: string;
  label: string;
  icon: string;
  action: string; // route path or special action
  shortcut?: string;
  section: string;
}

const commands: Command[] = [
  {
    id: "drives",
    label: "Go to Drives",
    icon: "folder_open",
    action: "/drives",
    shortcut: "⌘ 1",
    section: "Suggestions",
  },
  {
    id: "new-key",
    label: "New Key",
    icon: "vpn_key",
    action: "/kv",
    shortcut: "⌘ N",
    section: "Suggestions",
  },
  {
    id: "invite",
    label: "Invite Device",
    icon: "person_add",
    action: "/devices/invite",
    shortcut: "⌘ I",
    section: "Suggestions",
  },
  {
    id: "network",
    label: "Network Dashboard",
    icon: "hub",
    action: "/network",
    shortcut: "⌘ 4",
    section: "System",
  },
  {
    id: "settings",
    label: "Settings",
    icon: "settings",
    action: "/settings",
    shortcut: "⌘ 5",
    section: "System",
  },
];

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const navigate = useNavigate();

  const filtered = query
    ? commands.filter((c) =>
        c.label.toLowerCase().includes(query.toLowerCase())
      )
    : commands;

  const sections = [...new Set(filtered.map((c) => c.section))];

  const run = useCallback(
    (cmd: Command) => {
      setOpen(false);
      setQuery("");
      navigate(cmd.action);
    },
    [navigate]
  );

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen((prev) => !prev);
      }
      if (e.key === "Escape") {
        setOpen(false);
        setQuery("");
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[100] flex items-start justify-center pt-[20vh]">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-on-surface/20 backdrop-blur-sm"
        onClick={() => {
          setOpen(false);
          setQuery("");
        }}
      />

      {/* Palette */}
      <div className="relative w-full max-w-lg bg-surface-container-lowest rounded-xl shadow-2xl overflow-hidden border border-outline-variant/20">
        {/* Search input */}
        <div className="flex items-center gap-3 px-4 py-3 border-b border-outline-variant/10">
          <Icon name="search" className="text-outline text-lg" />
          <input
            autoFocus
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="flex-1 bg-transparent border-none outline-none text-sm text-on-surface placeholder:text-outline focus:ring-0"
            placeholder="Type a command or search..."
          />
          <kbd className="text-[10px] font-mono text-outline bg-surface-container-high px-1.5 py-0.5 rounded">
            ⌘K
          </kbd>
        </div>

        {/* Results */}
        <div className="max-h-80 overflow-y-auto py-2">
          {sections.map((section) => (
            <div key={section}>
              <p className="px-4 pt-3 pb-1 text-[10px] font-bold uppercase tracking-widest text-outline">
                {section}
              </p>
              {filtered
                .filter((c) => c.section === section)
                .map((cmd) => (
                  <button
                    key={cmd.id}
                    onClick={() => run(cmd)}
                    className="w-full flex items-center gap-3 px-4 py-2.5 text-sm text-on-surface hover:bg-primary/5 transition-colors cursor-pointer"
                  >
                    <Icon
                      name={cmd.icon}
                      className="text-on-surface-variant text-lg"
                    />
                    <span className="flex-1 text-left">{cmd.label}</span>
                    {cmd.shortcut && (
                      <span className="text-[10px] font-mono text-outline">
                        {cmd.shortcut}
                      </span>
                    )}
                  </button>
                ))}
            </div>
          ))}
          {filtered.length === 0 && (
            <p className="px-4 py-8 text-center text-sm text-outline">
              No results found.
            </p>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center gap-4 px-4 py-2 border-t border-outline-variant/10 text-[10px] text-outline">
          <span>
            <kbd className="bg-surface-container-high px-1 py-0.5 rounded mr-1">
              ↑↓
            </kbd>{" "}
            navigate
          </span>
          <span>
            <kbd className="bg-surface-container-high px-1 py-0.5 rounded mr-1">
              ↵
            </kbd>{" "}
            select
          </span>
          <span className="ml-auto">
            <kbd className="bg-surface-container-high px-1 py-0.5 rounded mr-1">
              esc
            </kbd>{" "}
            close
          </span>
        </div>
      </div>
    </div>
  );
}
