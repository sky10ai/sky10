import { useCallback, useState } from "react";
import { Icon } from "../components/Icon";
import { skykv } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function KVStore() {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [editValue, setEditValue] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const [showNew, setShowNew] = useState(false);

  const {
    data: allData,
    loading,
    error,
    refetch,
  } = useRPC(() => skykv.getAll());
  const { data: kvStatus } = useRPC(() => skykv.status());

  const entries = allData?.entries ?? {};
  const keys = Object.keys(entries).sort();

  // Load value when key is selected
  const selectKey = useCallback(
    (key: string) => {
      setSelectedKey(key);
      setEditValue(entries[key] ?? "");
    },
    [entries]
  );

  const saveValue = async () => {
    if (!selectedKey) return;
    await skykv.set({ key: selectedKey, value: editValue });
    refetch();
  };

  const deleteKey = async () => {
    if (!selectedKey) return;
    await skykv.delete({ key: selectedKey });
    setSelectedKey(null);
    refetch();
  };

  const createKey = async () => {
    if (!newKey) return;
    await skykv.set({ key: newKey, value: newValue });
    setShowNew(false);
    setNewKey("");
    setNewValue("");
    refetch();
  };

  // Try to detect if value is JSON for display
  const isJSON = (v: string) => {
    try {
      JSON.parse(v);
      return true;
    } catch {
      return false;
    }
  };


  return (
    <div className="flex flex-col flex-1 overflow-hidden">
      {/* Namespace bar */}
      <div className="px-8 pt-6 pb-2 flex items-center gap-2 border-b border-transparent">
        <button className="px-4 py-2 rounded-lg bg-primary-fixed/30 text-primary font-medium text-xs border border-primary/10">
          {kvStatus?.namespace ?? "default"}
        </button>
        <div className="ml-auto flex items-center gap-3">
          <span className="text-[10px] font-mono text-secondary">
            {kvStatus?.keys ?? 0} keys
          </span>
          <div className="flex items-center gap-2 px-3 py-1 bg-green-50 rounded-full">
            <span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
            <span className="text-[10px] font-bold text-green-700 tracking-wider uppercase">
              Live
            </span>
          </div>
        </div>
      </div>

      {error && (
        <div className="mx-8 mt-4 p-4 bg-error-container/20 text-error rounded-xl text-sm">
          {error}
        </div>
      )}

      {/* Split view */}
      <div className="flex flex-1 overflow-hidden">
        {/* Key list */}
        <div className="w-80 flex flex-col bg-surface-container-low/50 border-r border-transparent">
          <div className="p-4 border-b border-transparent flex gap-2">
            <button className="flex-1 py-1.5 px-3 bg-surface-container-lowest rounded-md text-[11px] font-semibold flex items-center justify-center gap-2 shadow-sm border border-black/5">
              <Icon name="filter_list" className="text-xs" />
              Sort: A-Z
            </button>
            <button
              onClick={() => setShowNew(true)}
              className="p-1.5 bg-primary text-white rounded-md shadow-sm hover:opacity-90 transition-colors"
            >
              <Icon name="add" className="text-sm" />
            </button>
          </div>
          <div className="flex-1 overflow-y-auto px-2 py-4 space-y-1">
            {loading && keys.length === 0 && (
              <div className="space-y-2 px-3">
                {[1, 2, 3].map((i) => (
                  <div
                    key={i}
                    className="h-14 bg-surface-container-highest/50 rounded-xl animate-pulse"
                  />
                ))}
              </div>
            )}
            {keys.map((key) => (
              <button
                key={key}
                onClick={() => selectKey(key)}
                className={`w-full text-left px-3 py-3 rounded-xl cursor-pointer transition-all ${
                  selectedKey === key
                    ? "bg-surface-container-lowest shadow-sm border border-transparent hover:border-primary/20"
                    : "hover:bg-surface-container-highest/50"
                }`}
              >
                <div className="flex justify-between items-start mb-1">
                  <span
                    className={`font-mono text-xs font-semibold truncate ${selectedKey === key ? "text-primary" : "text-on-surface"}`}
                  >
                    {key}
                  </span>
                  <span className="text-[9px] text-outline px-1.5 py-0.5 bg-surface-container-high rounded uppercase font-bold ml-2 shrink-0">
                    {isJSON(entries[key] ?? "") ? "JSON" : "STR"}
                  </span>
                </div>
                <p className="text-[11px] text-secondary truncate font-mono">
                  {entries[key]}
                </p>
              </button>
            ))}
          </div>
        </div>

        {/* Value detail */}
        <div className="flex-1 flex flex-col bg-surface overflow-hidden">
          {showNew ? (
            <div className="p-8 space-y-6">
              <h2 className="text-xl font-semibold tracking-tight">
                New Key
              </h2>
              <div className="space-y-2">
                <label className="text-[10px] font-bold text-outline uppercase tracking-widest">
                  Key
                </label>
                <input
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  className="w-full bg-surface-container-lowest p-3 rounded-xl border border-outline-variant/20 font-mono text-sm focus:ring-1 focus:ring-primary focus:border-primary"
                  placeholder="my-key"
                />
              </div>
              <div className="space-y-2">
                <label className="text-[10px] font-bold text-outline uppercase tracking-widest">
                  Value
                </label>
                <textarea
                  value={newValue}
                  onChange={(e) => setNewValue(e.target.value)}
                  className="w-full h-40 bg-surface-container-lowest p-3 rounded-xl border border-outline-variant/20 font-mono text-sm focus:ring-1 focus:ring-primary focus:border-primary resize-none"
                  placeholder="value..."
                />
              </div>
              <div className="flex gap-2">
                <button
                  onClick={createKey}
                  className="px-6 py-2 bg-gradient-to-br from-primary to-primary-container text-white rounded-full font-medium text-sm shadow-lg shadow-primary/10"
                >
                  Create
                </button>
                <button
                  onClick={() => setShowNew(false)}
                  className="px-6 py-2 text-sm text-secondary hover:text-on-surface"
                >
                  Cancel
                </button>
              </div>
            </div>
          ) : selectedKey ? (
            <>
              <div className="p-6 flex items-center justify-between">
                <div>
                  <h2 className="text-xl font-semibold tracking-tight mb-1">
                    {selectedKey}
                  </h2>
                  <div className="flex items-center gap-4">
                    <span className="text-xs text-secondary font-mono">
                      {(entries[selectedKey] ?? "").length} bytes
                    </span>
                  </div>
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={deleteKey}
                    className="px-4 py-2 text-sm font-medium text-error hover:bg-error-container/20 rounded-lg transition-colors flex items-center gap-2"
                  >
                    <Icon name="delete" className="text-sm" />
                    Delete
                  </button>
                  <button
                    onClick={saveValue}
                    className="px-6 py-2 bg-gradient-to-br from-primary to-primary-container text-white rounded-full font-medium text-sm shadow-lg shadow-primary/10 hover:opacity-90 transition-all flex items-center gap-2"
                  >
                    <Icon name="save" className="text-sm" />
                    Save Changes
                  </button>
                </div>
              </div>
              <div className="flex-1 px-8 pb-8 flex flex-col gap-6">
                <div className="flex-1 flex flex-col space-y-2">
                  <label className="text-[10px] font-bold text-outline uppercase tracking-widest px-1">
                    Value
                  </label>
                  <div className="flex-1 bg-surface-container-lowest rounded-xl shadow-sm border border-transparent overflow-hidden flex flex-col">
                    <div className="bg-surface-container-high px-4 py-2 flex items-center justify-between">
                      <div className="flex gap-1.5">
                        <div className="w-2.5 h-2.5 rounded-full bg-error/20" />
                        <div className="w-2.5 h-2.5 rounded-full bg-tertiary/20" />
                        <div className="w-2.5 h-2.5 rounded-full bg-primary/20" />
                      </div>
                      <span className="text-[10px] font-mono text-secondary uppercase tracking-tight">
                        {isJSON(entries[selectedKey] ?? "")
                          ? "application/json"
                          : "text/plain"}
                      </span>
                    </div>
                    <textarea
                      value={editValue}
                      onChange={(e) => setEditValue(e.target.value)}
                      className="flex-1 p-6 font-mono text-sm leading-relaxed bg-transparent border-none focus:ring-0 resize-none"
                      placeholder="Enter value..."
                    />
                  </div>
                </div>
              </div>
            </>
          ) : (
            <div className="flex-1 flex items-center justify-center text-center">
              <div>
                <Icon
                  name="database"
                  className="text-5xl text-outline mb-4"
                />
                <p className="text-secondary">
                  Select a key to view its value
                </p>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
