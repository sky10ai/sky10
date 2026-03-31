import { useState } from "react";
import { Icon } from "../components/Icon";

interface KVKey {
  key: string;
  type: string;
  preview: string;
  active?: boolean;
}

const namespaces = [
  { name: "default", color: "bg-primary-fixed/30 text-primary border-primary/10" },
  { name: "config", color: "bg-tertiary-fixed/30 text-tertiary" },
  { name: "app-state", color: "bg-secondary-fixed/30 text-secondary" },
];

const keys: KVKey[] = [
  { key: "config/api_endpoint", type: "JSON", preview: '{"url": "https://api.sky10.io/v1", "t..."}', active: true },
  { key: "config/max_peers", type: "INT", preview: "128" },
  { key: "keys/p2p_secret", type: "SECRET", preview: "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022" },
  { key: "state/last_sync", type: "TS", preview: "1709241600" },
  { key: "ui/theme_preference", type: "STR", preview: '"ethereal_vault"' },
];

const sampleJSON = `{
  "url": "https://api.sky10.io/v1",
  "timeout": 5000,
  "retry_policy": {
    "max_attempts": 3,
    "backoff_ms": 200
  },
  "headers": {
    "X-Vault-Token": "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022",
    "User-Agent": "sky10-agent/0.4.2"
  }
}`;

export default function KVStore() {
  const [activeNs, setActiveNs] = useState("default");
  const [selectedKey, setSelectedKey] = useState(0);

  return (
    <div className="flex flex-col flex-1 overflow-hidden">
      {/* Namespace tabs */}
      <div className="px-8 pt-6 pb-2 flex items-center gap-2 border-b border-transparent">
        {namespaces.map((ns) => (
          <button
            key={ns.name}
            onClick={() => setActiveNs(ns.name)}
            className={`px-4 py-2 rounded-lg font-medium text-xs transition-colors ${
              activeNs === ns.name
                ? `${ns.color} border`
                : "hover:bg-surface-container-high text-outline"
            }`}
          >
            {ns.name}
          </button>
        ))}
        <button className="px-4 py-2 rounded-lg hover:bg-surface-container-high text-outline text-xs transition-colors">
          <Icon name="add" className="text-sm" />
        </button>
        <div className="ml-auto flex items-center gap-3">
          <div className="flex items-center gap-2 px-3 py-1 bg-green-50 rounded-full">
            <span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
            <span className="text-[10px] font-bold text-green-700 tracking-wider uppercase">
              Live Skylink
            </span>
          </div>
        </div>
      </div>

      {/* Split view */}
      <div className="flex flex-1 overflow-hidden">
        {/* Key list */}
        <div className="w-80 flex flex-col bg-surface-container-low/50 border-r border-transparent">
          <div className="p-4 border-b border-transparent flex gap-2">
            <button className="flex-1 py-1.5 px-3 bg-surface-container-lowest rounded-md text-[11px] font-semibold flex items-center justify-center gap-2 shadow-sm border border-black/5">
              <Icon name="filter_list" className="text-xs" />
              Sort: A-Z
            </button>
            <button className="p-1.5 bg-surface-container-lowest rounded-md shadow-sm border border-black/5 hover:bg-surface transition-colors">
              <Icon name="checklist" className="text-sm" />
            </button>
          </div>
          <div className="flex-1 overflow-y-auto px-2 py-4 space-y-1">
            {keys.map((k, i) => (
              <button
                key={k.key}
                onClick={() => setSelectedKey(i)}
                className={`w-full text-left px-3 py-3 rounded-xl cursor-pointer transition-all ${
                  selectedKey === i
                    ? "bg-surface-container-lowest shadow-sm border border-transparent hover:border-primary/20"
                    : "hover:bg-surface-container-highest/50"
                }`}
              >
                <div className="flex justify-between items-start mb-1">
                  <span
                    className={`font-mono text-xs font-semibold truncate ${selectedKey === i ? "text-primary" : "text-on-surface"}`}
                  >
                    {k.key}
                  </span>
                  <span className="text-[9px] text-outline px-1.5 py-0.5 bg-surface-container-high rounded uppercase font-bold ml-2 shrink-0">
                    {k.type}
                  </span>
                </div>
                <p className="text-[11px] text-secondary truncate font-mono">
                  {k.preview}
                </p>
              </button>
            ))}
          </div>
        </div>

        {/* Value detail */}
        <div className="flex-1 flex flex-col bg-surface overflow-hidden">
          <div className="p-6 flex items-center justify-between">
            <div>
              <h2 className="text-xl font-semibold tracking-tight mb-1">
                Value Details
              </h2>
              <div className="flex items-center gap-4">
                <div className="flex items-center gap-1.5 text-xs text-secondary">
                  <Icon name="history" className="text-[14px]" />
                  <span>Modified 2m ago</span>
                </div>
                <div className="flex items-center gap-1.5 text-xs text-secondary">
                  <Icon name="memory" className="text-[14px]" />
                  <span>Agent-04X</span>
                </div>
                <div className="flex items-center gap-1.5 text-xs text-secondary">
                  <Icon name="database" className="text-[14px]" />
                  <span>1.2 KB</span>
                </div>
              </div>
            </div>
            <div className="flex gap-2">
              <button className="px-4 py-2 text-sm font-medium text-error hover:bg-error-container/20 rounded-lg transition-colors flex items-center gap-2">
                <Icon name="delete" className="text-sm" />
                Delete
              </button>
              <button className="px-6 py-2 bg-gradient-to-br from-primary to-primary-container text-white rounded-full font-medium text-sm shadow-lg shadow-primary/10 hover:opacity-90 transition-all flex items-center gap-2">
                <Icon name="save" className="text-sm" />
                Save Changes
              </button>
            </div>
          </div>

          <div className="flex-1 px-8 pb-8 flex flex-col gap-6">
            {/* Key input */}
            <div className="space-y-2">
              <label className="text-[10px] font-bold text-outline uppercase tracking-widest px-1">
                Key Namespace Path
              </label>
              <div className="flex items-center bg-surface-container-lowest p-3 rounded-xl shadow-sm border border-transparent focus-within:border-primary/20 transition-all">
                <span className="text-primary font-mono text-sm px-2">
                  config/
                </span>
                <input
                  className="flex-1 bg-transparent border-none focus:ring-0 font-mono text-sm p-0"
                  type="text"
                  defaultValue="api_endpoint"
                />
                <Icon
                  name="link"
                  className="text-outline cursor-pointer px-2"
                />
              </div>
            </div>

            {/* Code editor */}
            <div className="flex-1 flex flex-col space-y-2">
              <label className="text-[10px] font-bold text-outline uppercase tracking-widest px-1">
                Value Payload
              </label>
              <div className="flex-1 bg-surface-container-lowest rounded-xl shadow-sm border border-transparent overflow-hidden flex flex-col">
                <div className="bg-surface-container-high px-4 py-2 flex items-center justify-between">
                  <div className="flex gap-1.5">
                    <div className="w-2.5 h-2.5 rounded-full bg-error/20" />
                    <div className="w-2.5 h-2.5 rounded-full bg-tertiary/20" />
                    <div className="w-2.5 h-2.5 rounded-full bg-primary/20" />
                  </div>
                  <span className="text-[10px] font-mono text-secondary uppercase tracking-tight">
                    application/json
                  </span>
                </div>
                <div className="flex-1 p-6 font-mono text-sm leading-relaxed overflow-y-auto">
                  <pre className="whitespace-pre-wrap text-on-surface">
                    {sampleJSON}
                  </pre>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* FAB */}
      <div className="fixed bottom-8 right-8">
        <button className="w-14 h-14 bg-on-surface text-surface rounded-full shadow-2xl flex items-center justify-center hover:scale-105 active:scale-95 transition-transform group">
          <Icon
            name="add"
            className="text-2xl group-hover:rotate-90 transition-transform duration-300"
          />
        </button>
      </div>
    </div>
  );
}
