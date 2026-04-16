import { useState } from "react";
import { skyfs } from "../../lib/rpc";
import { Icon } from "../Icon";

export function NewDriveForm({
  onCreated,
  onCancel,
}: {
  onCreated: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!name.trim() || !path.trim()) return;
    setError(null);
    setCreating(true);
    try {
      await skyfs.driveCreate({ name: name.trim(), path: path.trim() });
      onCreated();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create drive");
    } finally {
      setCreating(false);
    }
  };

  return (
    <form
      className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm"
      onSubmit={submit}
    >
      <h3 className="flex items-center gap-2 text-lg font-semibold text-on-surface">
        <Icon className="text-primary" name="add_circle" />
        New Drive
      </h3>

      <div className="mt-4 space-y-4">
        <div>
          <label className="text-xs font-bold uppercase tracking-wider text-secondary">
            Name
          </label>
          <input
            autoFocus
            className="mt-1 w-full rounded-lg border border-outline-variant/20 bg-surface-container px-3 py-2 text-sm text-on-surface outline-none focus:border-primary"
            onChange={(e) => setName(e.target.value)}
            placeholder="my-docs"
            value={name}
          />
        </div>
        <div>
          <label className="text-xs font-bold uppercase tracking-wider text-secondary">
            Local Path
          </label>
          <input
            className="mt-1 w-full rounded-lg border border-outline-variant/20 bg-surface-container px-3 py-2 font-mono text-sm text-on-surface outline-none focus:border-primary"
            onChange={(e) => setPath(e.target.value)}
            placeholder="/Users/you/Documents/my-docs"
            value={path}
          />
        </div>
      </div>

      {error && (
        <div className="mt-3 rounded-lg bg-error-container/20 p-3 text-sm text-error">
          {error}
        </div>
      )}

      <div className="mt-4 flex justify-end gap-3">
        <button
          className="rounded-full px-4 py-2 text-sm font-medium text-secondary transition-colors hover:bg-surface-container-high"
          onClick={onCancel}
          type="button"
        >
          Cancel
        </button>
        <button
          className="rounded-full bg-primary px-5 py-2 text-sm font-semibold text-on-primary shadow-lg shadow-primary/20 transition-colors hover:bg-primary/90 disabled:opacity-50"
          disabled={creating || !name.trim() || !path.trim()}
          type="submit"
        >
          {creating ? "Creating..." : "Create Drive"}
        </button>
      </div>
    </form>
  );
}
