import { startTransition, useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import { SANDBOX_STATE_EVENT_TYPES } from "../lib/events";
import { sandbox } from "../lib/rpc";
import {
  isDockerTemplate,
  isHermesTemplate,
  isOpenClawTemplate,
  sandboxCurrentProgress,
  nextSandboxName,
  sandboxTemplateById,
  SANDBOX_TEMPLATES,
  sandboxLabel,
  sandboxSlug,
  sandboxTone,
} from "../lib/sandboxes";
import { timeAgo, useRPC } from "../lib/useRPC";

export default function Sandboxes() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTemplate = sandboxTemplateById(
    searchParams.get("template") ?? undefined,
  );
  const [draftName, setDraftName] = useState(() =>
    nextSandboxName(requestedTemplate.id),
  );
  const [selectedTemplate, setSelectedTemplate] = useState<string>(
    requestedTemplate.id,
  );
  const [actionError, setActionError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const {
    data: listData,
    error: listError,
    refreshing,
    refetch: refetchList,
  } = useRPC(() => sandbox.list(), [], {
    live: SANDBOX_STATE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const sandboxes = listData?.sandboxes ?? [];
  const templateConfig = sandboxTemplateById(selectedTemplate);
  const draftSlug = sandboxSlug(draftName);
  const creatingLabel = isOpenClawTemplate(templateConfig.id)
    ? "Create OpenClaw"
    : isHermesTemplate(templateConfig.id)
      ? "Create Hermes"
      : "Provision Sandbox";
  const creatingBusyLabel = isOpenClawTemplate(templateConfig.id)
    ? "Creating OpenClaw..."
    : isHermesTemplate(templateConfig.id)
      ? "Creating Hermes..."
      : "Provisioning...";

  useEffect(() => {
    if (requestedTemplate.id === selectedTemplate) return;
    setSelectedTemplate(requestedTemplate.id);
  }, [requestedTemplate.id, selectedTemplate]);

  const handleTemplateSelect = useCallback(
    (templateId: string) => {
      setSelectedTemplate(templateId);
      const nextParams = new URLSearchParams(searchParams);
      nextParams.set("template", templateId);
      setSearchParams(nextParams, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const handleCreate = useCallback(async () => {
    const name = draftName.trim();
    if (!name || creating) return;

    setCreating(true);
    setActionError(null);
    try {
      const created = await sandbox.create({
        name,
        provider: templateConfig.provider,
        template: templateConfig.id,
      });
      setDraftName(nextSandboxName(templateConfig.id));
      refetchList({ background: true });
      startTransition(() => {
        const detailPath = isHermesTemplate(templateConfig.id)
          ? `/settings/sandboxes/${encodeURIComponent(created.slug)}?panel=terminal`
          : `/settings/sandboxes/${encodeURIComponent(created.slug)}`;
        navigate(detailPath);
      });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Create failed");
    } finally {
      setCreating(false);
    }
  }, [
    creating,
    draftName,
    navigate,
    refetchList,
    templateConfig.id,
    templateConfig.provider,
  ]);

  return (
    <SettingsPage
      backHref="/settings"
      description="Provision and manage isolated runtimes on this machine."
      title="Sandboxes"
      width="wide"
    >
      {(actionError || listError) && (
        <div className="rounded-2xl bg-error-container/20 p-4 text-sm text-error">
          {actionError ?? listError}
        </div>
      )}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
        <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
          <div className="space-y-6">
            <div className="space-y-2">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Create Sandbox
              </p>
              <h2 className="text-2xl font-semibold text-on-surface">
                Provision from a template
              </h2>
              <p className="max-w-2xl text-sm text-secondary">
                Pick a template, choose a name, and sky10 will create the
                runtime asynchronously so this screen stays responsive while
                Lima boots the guest. Display names stay intact, while runtime
                IDs and filesystem paths are slugified automatically.
              </p>
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              {SANDBOX_TEMPLATES.map((template) => {
                const active = template.id === selectedTemplate;
                return (
                  <button
                    key={template.id}
                    className={`rounded-2xl border p-5 text-left transition-all ${
                      active
                        ? "border-primary/40 bg-primary/10 shadow-sm"
                        : "border-outline-variant/10 bg-surface-container hover:bg-surface-container-high"
                    }`}
                    onClick={() => handleTemplateSelect(template.id)}
                    type="button"
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="space-y-1">
                        <p className="font-semibold text-on-surface">
                          {template.label}
                        </p>
                        <p className="text-xs text-secondary">
                          {template.summary}
                        </p>
                      </div>
                      {active && (
                        <StatusBadge tone="processing">Selected</StatusBadge>
                      )}
                    </div>
                    <p className="mt-3 text-sm text-secondary">
                      {template.description}
                    </p>
                    <p className="mt-3 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                      Provider: {template.provider}
                    </p>
                  </button>
                );
              })}
            </div>

            <div className="flex flex-col gap-3 md:flex-row">
              <input
                className="min-w-0 flex-1 rounded-full border border-outline-variant/20 bg-surface-container px-5 py-3 text-sm text-on-surface outline-none transition-colors focus:border-primary/40"
                onChange={(e) => setDraftName(e.target.value)}
                placeholder="sandbox name"
                value={draftName}
              />
              <button
                className="inline-flex items-center justify-center gap-2 rounded-full bg-primary px-6 py-3 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95 disabled:opacity-60"
                disabled={creating || !draftSlug}
                onClick={handleCreate}
                type="button"
              >
                <Icon name="add" />
                {creating ? creatingBusyLabel : creatingLabel}
              </button>
            </div>
            <p className="text-xs text-secondary">
              {draftSlug ? (
                <>
                  Runtime ID: <code>{draftSlug}</code> • Agent home{" "}
                  <code>~/Sky10/Drives/Agents/{draftSlug}</code>
                </>
              ) : (
                "Names must include at least one letter or number."
              )}
            </p>
          </div>
        </section>

        <aside className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
          <div className="space-y-4">
            <div>
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Runtime Notes
              </p>
              <h2 className="mt-2 text-xl font-semibold text-on-surface">
                Current template set
              </h2>
            </div>
            <div className="space-y-3 text-sm text-secondary">
              <p>
                Sandboxes mount a durable agent home from{" "}
                <code>~/Sky10/Drives/Agents/&lt;slug&gt;</code> and keep
                disposable sandbox-local state under{" "}
                <code>~/.sky10/sandboxes/&lt;slug&gt;/state</code>.
              </p>
              {isOpenClawTemplate(templateConfig.id) ? (
                <>
                  <p>
                    {isDockerTemplate(templateConfig.id) ? (
                      <>
                        The OpenClaw Docker template installs Docker in the
                        guest, then builds and runs guest-local{" "}
                        <code>sky10</code>, OpenClaw, Chromium, Xvfb, and Caddy
                        as Docker containers while keeping guest UIs on ports{" "}
                        <code>9101</code> and <code>18790</code>.
                      </>
                    ) : (
                      <>
                        The OpenClaw template installs guest-local{" "}
                        <code>sky10</code>, OpenClaw, Chromium, Xvfb, and Caddy
                        inside the guest, with guest UIs on ports{" "}
                        <code>9101</code> and <code>18790</code>.
                      </>
                    )}
                  </p>
                  <p>
                    It also loads the bundled <code>sky10</code> OpenClaw
                    channel and waits for the guest agent to register on the
                    guest-local daemon. sky10 network join comes later.
                  </p>
                </>
              ) : isHermesTemplate(templateConfig.id) ? (
                <>
                  <p>
                    {isDockerTemplate(templateConfig.id) ? (
                      <>
                        The Hermes Docker template installs Docker in the guest,
                        then runs guest-local <code>sky10</code>, Hermes Agent,
                        and the host chat bridge inside Docker while keeping{" "}
                        <code>hermes-shared</code> available from the sandbox
                        terminal in <code>/shared/workspace</code>.
                      </>
                    ) : (
                      <>
                        The Hermes template installs Hermes Agent inside the
                        guest, links <code>/sandbox-state/.env</code> into{" "}
                        <code>~/.hermes/.env</code>, and keeps the embedded
                        sandbox terminal ready for the native Hermes TUI in{" "}
                        <code>/shared/workspace</code>.
                      </>
                    )}
                  </p>
                  <p>
                    It also starts a guest-local Hermes gateway plus a sky10
                    bridge, so the sandbox shows up in the host agent list and
                    can be chatted with from sky10 while still remaining usable
                    from the guest terminal.
                  </p>
                </>
              ) : (
                <>
                  <p>
                    Provisioning logs stream live once the sandbox detail page
                    opens, so boot failures stay visible.
                  </p>
                  <p>
                    Each sandbox gets its own detail page for lifecycle actions,
                    runtime metadata, logs, and terminal access.
                  </p>
                </>
              )}
            </div>
          </div>
        </aside>
      </div>

      <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm">
        <div className="mb-5 flex items-center justify-between gap-4">
          <div>
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Inventory
            </p>
            <h2 className="mt-2 text-2xl font-semibold text-on-surface">
              Existing sandboxes
            </h2>
          </div>
          <div className="flex items-center gap-3">
            {refreshing && (
              <StatusBadge icon="sync" tone="neutral">
                Refreshing
              </StatusBadge>
            )}
            <span className="rounded-full bg-surface-container px-3 py-1 text-xs font-semibold text-secondary">
              {sandboxes.length}
            </span>
          </div>
        </div>

        {sandboxes.length ? (
          <div className="space-y-3">
            {sandboxes.map((item) => {
              const progress = sandboxCurrentProgress(item);
              const progressWidth = Math.max(
                0,
                Math.min(progress?.percent ?? 0, 100),
              );

              return (
                <Link
                  key={item.slug}
                  className="group flex flex-col gap-4 rounded-2xl border border-outline-variant/10 bg-surface-container px-5 py-4 transition-all hover:border-primary/20 hover:bg-surface-container-high md:flex-row md:items-center md:justify-between"
                  to={`/settings/sandboxes/${encodeURIComponent(item.slug)}`}
                >
                  <div className="space-y-3">
                    <div className="flex flex-wrap items-center gap-3">
                      <h3 className="text-lg font-semibold text-on-surface">
                        {item.name}
                      </h3>
                      <StatusBadge tone={sandboxTone(item.status)}>
                        {sandboxLabel(item.status)}
                      </StatusBadge>
                      {item.vm_status && (
                        <StatusBadge tone="neutral">
                          VM {item.vm_status}
                        </StatusBadge>
                      )}
                    </div>
                    <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-secondary">
                      <span>
                        {item.provider} / {item.template}
                      </span>
                      <span>ID {item.slug}</span>
                      <span>Updated {timeAgo(item.updated_at)}</span>
                    </div>
                    {progress && (
                      <div className="space-y-2">
                        <div className="flex items-center justify-between gap-3 text-sm">
                          <span className="font-medium text-on-surface">
                            {progress.summary}
                          </span>
                          <span className="font-semibold text-secondary">
                            {progress.percent}%
                          </span>
                        </div>
                        <div className="h-2 overflow-hidden rounded-full bg-surface-container-high">
                          <div
                            className={`h-full rounded-full transition-[width] duration-300 ${
                              item.status === "error"
                                ? "bg-error"
                                : "bg-primary"
                            }`}
                            style={{ width: `${progressWidth}%` }}
                          />
                        </div>
                      </div>
                    )}
                    {item.last_error && (
                      <p className="text-sm text-error">{item.last_error}</p>
                    )}
                  </div>
                  <div className="inline-flex items-center gap-2 text-sm font-semibold text-primary transition-colors group-hover:text-on-surface">
                    Open
                    <Icon className="text-base" name="arrow_forward" />
                  </div>
                </Link>
              );
            })}
          </div>
        ) : (
          <div className="rounded-2xl bg-surface-container p-6 text-sm text-secondary">
            No sandboxes yet.
          </div>
        )}
      </section>
    </SettingsPage>
  );
}
