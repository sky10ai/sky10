import { useEffect, useState, type FormEvent } from "react";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import {
  messaging,
  type MessagingAction,
  type MessagingAdapterInfo,
  type MessagingConnection,
  type MessagingRunAdapterActionResult,
  type MessagingSetting,
  type MessagingValidationIssue,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

type AdapterFormState = {
  formKey: string;
  isExisting: boolean;
  connectionID: string;
  label: string;
  secretScope: "current" | "trusted";
  values: Record<string, string>;
};

type ActionState = {
  formKey: string;
  actionID: string;
  busy: boolean;
};

type AdapterActionFeedback = {
  error: string | null;
  result: MessagingRunAdapterActionResult | null;
};

let draftCounter = 0;
function nextDraftKey() {
  draftCounter += 1;
  return `__new__:${draftCounter}`;
}

function defaultConnectionID(adapterID: string) {
  return `${adapterID}/default`;
}

function suggestConnectionID(adapterID: string, used: Set<string>) {
  const candidates = [defaultConnectionID(adapterID)];
  for (let i = 2; i < 1000; i += 1) candidates.push(`${adapterID}/${i}`);
  for (const candidate of candidates) {
    if (!used.has(candidate)) return candidate;
  }
  return `${adapterID}/${Date.now()}`;
}

function defaultAdapterLabel(adapter: MessagingAdapterInfo) {
  return adapter.adapter?.display_name || adapter.name;
}

function connectionStatus(connection?: MessagingConnection) {
  return connection?.status || "not connected";
}

function statusTone(
  status: string,
): "success" | "processing" | "danger" | "neutral" {
  switch (status) {
    case "connected":
      return "success";
    case "connecting":
    case "auth_required":
    case "degraded":
      return "processing";
    case "error":
      return "danger";
    default:
      return "neutral";
  }
}

function initialDraftState(
  adapter: MessagingAdapterInfo,
  suggestedID: string,
): AdapterFormState {
  const values: Record<string, string> = {};
  for (const setting of adapter.settings ?? []) {
    if (setting.target === "credential") continue;
    values[setting.key] = setting.default ?? "";
  }
  return {
    formKey: nextDraftKey(),
    isExisting: false,
    connectionID: suggestedID,
    label: defaultAdapterLabel(adapter),
    secretScope: "current",
    values,
  };
}

function formFromConnection(
  adapter: MessagingAdapterInfo,
  connection: MessagingConnection,
): AdapterFormState {
  const values: Record<string, string> = {};
  for (const setting of adapter.settings ?? []) {
    if (setting.target === "credential") {
      values[setting.key] = "";
      continue;
    }
    values[setting.key] =
      connection.metadata?.[setting.key] ?? setting.default ?? "";
  }
  return {
    formKey: connection.id,
    isExisting: true,
    connectionID: connection.id,
    label: connection.label || defaultAdapterLabel(adapter),
    secretScope: "current",
    values,
  };
}

function hydrateForms(
  adapters: MessagingAdapterInfo[],
  connections: MessagingConnection[],
  current: Record<string, AdapterFormState[]>,
): Record<string, AdapterFormState[]> {
  const result: Record<string, AdapterFormState[]> = {};
  for (const adapter of adapters) {
    const adapterID = adapter.adapter?.id || adapter.name;
    const adapterConnections = connections.filter(
      (connection) => connection.adapter_id === adapterID,
    );
    const previous = current[adapterID] ?? [];
    const byKey = new Map(previous.map((form) => [form.formKey, form]));

    const next: AdapterFormState[] = [];
    for (const connection of adapterConnections) {
      const existing = byKey.get(connection.id);
      next.push(existing ?? formFromConnection(adapter, connection));
      byKey.delete(connection.id);
    }
    for (const draft of byKey.values()) {
      if (!draft.isExisting) next.push(draft);
    }
    if (next.length === 0) {
      next.push(initialDraftState(adapter, defaultConnectionID(adapterID)));
    }
    result[adapterID] = next;
  }
  return result;
}

function buildSettingsPayload(
  settings: MessagingSetting[],
  values: Record<string, string>,
) {
  const payload: Record<string, string | number | boolean | null> = {};
  for (const setting of settings) {
    const raw = values[setting.key] ?? "";
    if (setting.target === "credential" && raw.trim() === "") continue;
    if (setting.kind === "boolean") {
      payload[setting.key] = raw === "true";
      continue;
    }
    if (setting.kind === "number" && raw.trim() !== "") {
      payload[setting.key] = Number(raw);
      continue;
    }
    if (raw.trim() !== "") {
      payload[setting.key] = raw;
    }
  }
  return payload;
}

function capabilityLabels(adapter: MessagingAdapterInfo) {
  const capabilities = adapter.adapter?.capabilities ?? {};
  const labels: string[] = [];
  if (capabilities.receive_messages) labels.push("Receive");
  if (capabilities.send_messages) labels.push("Send");
  if (capabilities.create_drafts) labels.push("Drafts");
  if (capabilities.search_messages) labels.push("Message search");
  if (capabilities.search_conversations) labels.push("Conversation search");
  if (capabilities.search_identities) labels.push("Identity search");
  if (capabilities.threading) labels.push("Threads");
  if (capabilities.webhooks) labels.push("Webhooks");
  if (capabilities.polling) labels.push("Polling");
  return labels.slice(0, 8);
}

function fieldType(setting: MessagingSetting) {
  switch (setting.kind) {
    case "password":
    case "secret":
      return "password";
    case "number":
      return "number";
    case "url":
      return "url";
    default:
      return "text";
  }
}

function issuesFor(issues: MessagingValidationIssue[], field: string) {
  return issues.filter((issue) => issue.field === field);
}

function issueClasses(severity: string) {
  switch (severity) {
    case "error":
      return {
        bg: "bg-error/10",
        border: "border-error/20",
        text: "text-error",
      };
    case "warning":
      return {
        bg: "bg-amber-500/10",
        border: "border-amber-500/20",
        text: "text-amber-700 dark:text-amber-200",
      };
    default:
      return {
        bg: "bg-primary/10",
        border: "border-primary/20",
        text: "text-primary",
      };
  }
}

export default function SettingsMessaging() {
  const {
    data: adapterData,
    error: adapterError,
    loading: adaptersLoading,
    refetch: refetchAdapters,
  } = useRPC(() => messaging.adapters(), [], {
    refreshIntervalMs: 30_000,
  });
  const {
    data: connectionData,
    error: connectionError,
    refetch: refetchConnections,
  } = useRPC(() => messaging.connections(), [], {
    live: ["messaging:event"],
    refreshIntervalMs: 10_000,
  });

  const adapters = (adapterData?.adapters ?? []).filter(
    (adapter) => adapter.settings && adapter.settings.length > 0,
  );
  const connections = connectionData?.connections ?? [];
  const [forms, setForms] = useState<Record<string, AdapterFormState[]>>({});
  const [actionState, setActionState] = useState<ActionState | null>(null);
  const [feedback, setFeedback] = useState<
    Record<string, AdapterActionFeedback>
  >({});

  useEffect(() => {
    setForms((current) => hydrateForms(adapters, connections, current));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [adapterData, connectionData]);

  function patchForm(
    adapterID: string,
    formKey: string,
    patch:
      | Partial<AdapterFormState>
      | ((previous: AdapterFormState) => Partial<AdapterFormState>),
  ) {
    setForms((current) => {
      const list = current[adapterID];
      if (!list) return current;
      const next = list.map((form) => {
        if (form.formKey !== formKey) return form;
        const update =
          typeof patch === "function" ? patch(form) : patch;
        return { ...form, ...update };
      });
      return { ...current, [adapterID]: next };
    });
  }

  function updateField(
    adapterID: string,
    formKey: string,
    key: string,
    value: string,
  ) {
    patchForm(adapterID, formKey, (previous) => ({
      values: { ...previous.values, [key]: value },
    }));
  }

  function updateForm(
    adapterID: string,
    formKey: string,
    patch: Partial<AdapterFormState>,
  ) {
    patchForm(adapterID, formKey, patch);
  }

  function addDraft(adapter: MessagingAdapterInfo) {
    const adapterID = adapter.adapter?.id || adapter.name;
    setForms((current) => {
      const existing = current[adapterID] ?? [];
      const used = new Set(existing.map((form) => form.connectionID));
      for (const connection of connections) {
        if (connection.adapter_id === adapterID) used.add(connection.id);
      }
      const draft = initialDraftState(
        adapter,
        suggestConnectionID(adapterID, used),
      );
      return { ...current, [adapterID]: [...existing, draft] };
    });
  }

  function removeDraft(adapter: MessagingAdapterInfo, formKey: string) {
    const adapterID = adapter.adapter?.id || adapter.name;
    setForms((current) => {
      const existing = current[adapterID] ?? [];
      const filtered = existing.filter((form) => form.formKey !== formKey);
      if (filtered.length === 0) {
        filtered.push(
          initialDraftState(adapter, defaultConnectionID(adapterID)),
        );
      }
      return { ...current, [adapterID]: filtered };
    });
    setFeedback((current) => {
      if (!(formKey in current)) return current;
      const next = { ...current };
      delete next[formKey];
      return next;
    });
  }

  async function disconnect(form: AdapterFormState) {
    if (!form.isExisting) return;
    const ok = window.confirm(
      `Disconnect ${form.label || form.connectionID}? The credential secret stays in place; remove it separately if desired.`,
    );
    if (!ok) return;
    setActionState({
      formKey: form.formKey,
      actionID: "__disconnect__",
      busy: true,
    });
    try {
      await messaging.deleteConnection({ connection_id: form.connectionID });
      setFeedback((current) => {
        if (!(form.formKey in current)) return current;
        const next = { ...current };
        delete next[form.formKey];
        return next;
      });
      refetchConnections({ background: true });
    } catch (error) {
      setFeedback((current) => ({
        ...current,
        [form.formKey]: {
          error:
            error instanceof Error ? error.message : "Disconnect failed",
          result: null,
        },
      }));
    } finally {
      setActionState(null);
    }
  }

  async function runAction(
    adapter: MessagingAdapterInfo,
    form: AdapterFormState,
    action: MessagingAction,
  ) {
    const adapterID = adapter.adapter?.id || adapter.name;

    if (action.kind === "open_url") {
      if (action.url) {
        window.open(action.url, "_blank", "noopener,noreferrer");
      }
      setFeedback((current) => ({
        ...current,
        [form.formKey]: {
          error: null,
          result: {
            action_id: action.id,
            action_kind: action.kind,
            url: action.url,
          },
        },
      }));
      return;
    }

    setActionState({ formKey: form.formKey, actionID: action.id, busy: true });
    setFeedback((current) => ({
      ...current,
      [form.formKey]: { error: null, result: null },
    }));
    try {
      const response = await messaging.runAdapterAction({
        adapter_id: adapterID,
        action_id: action.id,
        connection_id: form.connectionID,
        label: form.label,
        settings: buildSettingsPayload(adapter.settings ?? [], form.values),
        secret_scope: form.secretScope,
      });
      const isConnect = action.kind === "connect";
      const persistedID = response?.connection?.id || form.connectionID;
      if (isConnect && !form.isExisting) {
        // Promote the draft to a saved form so hydration won't duplicate it.
        setForms((current) => {
          const list = current[adapterID];
          if (!list) return current;
          const next = list.map((existing) =>
            existing.formKey === form.formKey
              ? {
                  ...existing,
                  formKey: persistedID,
                  connectionID: persistedID,
                  isExisting: true,
                }
              : existing,
          );
          return { ...current, [adapterID]: next };
        });
        setFeedback((current) => {
          const previous = current[form.formKey];
          if (!previous) return current;
          const next = { ...current };
          delete next[form.formKey];
          next[persistedID] = { error: null, result: response };
          return next;
        });
      } else {
        setFeedback((current) => ({
          ...current,
          [form.formKey]: { error: null, result: response },
        }));
      }
      refetchConnections({ background: true });
      refetchAdapters({ background: true });
    } catch (error) {
      setFeedback((current) => ({
        ...current,
        [form.formKey]: {
          error: error instanceof Error ? error.message : "Action failed",
          result: null,
        },
      }));
    } finally {
      setActionState(null);
    }
  }

  function handleSubmit(
    event: FormEvent<HTMLFormElement>,
    adapter: MessagingAdapterInfo,
    form: AdapterFormState,
  ) {
    event.preventDefault();
    const connectAction = (adapter.actions ?? []).find(
      (action) => action.kind === "connect",
    );
    if (connectAction) {
      void runAction(adapter, form, connectAction);
    }
  }

  return (
    <SettingsPage
      backHref="/settings"
      description="Connect messaging platforms through adapter manifests. Sky10 stores platform credentials in secrets and keeps policy decisions in the broker."
      pinnablePageID="messaging"
      title="Messaging"
      width="wide"
    >
      <section className="overflow-hidden rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
        <div className="grid gap-0 lg:grid-cols-[minmax(0,1.05fr)_minmax(320px,0.95fr)]">
          <div className="relative overflow-hidden bg-[#153d2c] p-8 text-white">
            <div className="absolute -right-16 -top-16 h-56 w-56 rounded-full bg-emerald-300/20 blur-3xl" />
            <div className="absolute -bottom-24 left-10 h-64 w-64 rounded-full bg-sky-300/10 blur-3xl" />
            <div className="relative z-10 space-y-6">
              <div className="inline-flex items-center gap-2 rounded-full border border-white/15 bg-white/10 px-3 py-1 text-[10px] font-bold uppercase tracking-[0.22em] text-emerald-50">
                <Icon className="text-sm" name="forum" />
                Brokered messaging
              </div>
              <div className="max-w-xl space-y-3">
                <h2 className="text-4xl font-semibold tracking-tight">
                  Link apps without giving agents the keys.
                </h2>
                <p className="text-sm leading-6 text-emerald-50/80">
                  Adapters declare their own setup fields. Sky10 maps those
                  fields into connection metadata, encrypted credentials, and a
                  supervised adapter process.
                </p>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <div className="rounded-2xl border border-white/10 bg-white/10 p-4">
                  <p className="text-2xl font-semibold">{adapterData?.count ?? 0}</p>
                  <p className="text-xs text-emerald-50/70">Adapters</p>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/10 p-4">
                  <p className="text-2xl font-semibold">
                    {connectionData?.count ?? 0}
                  </p>
                  <p className="text-xs text-emerald-50/70">Connections</p>
                </div>
                <div className="rounded-2xl border border-white/10 bg-white/10 p-4">
                  <p className="text-2xl font-semibold">Policy</p>
                  <p className="text-xs text-emerald-50/70">Broker owned</p>
                </div>
              </div>
            </div>
          </div>
          <div className="space-y-4 p-8">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              Current boundary
            </p>
            <div className="space-y-3 text-sm text-secondary">
              <p>
                Credentials go to <code>pkg/secrets</code>; connections keep
                only a credential reference.
              </p>
              <p>
                The adapter receives staged credential material only while
                Sky10 is supervising the process.
              </p>
              <p>
                The next step after setup is policy review for read, draft, and
                send permissions.
              </p>
            </div>
            {(adapterError || connectionError) && (
              <div className="rounded-2xl border border-error/20 bg-error/10 p-4 text-sm text-error">
                {adapterError || connectionError}
              </div>
            )}
          </div>
        </div>
      </section>

      {adaptersLoading && adapters.length === 0 ? (
        <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 text-sm text-secondary">
          Loading messaging adapters...
        </div>
      ) : adapters.length === 0 ? (
        <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8">
          <h3 className="text-xl font-semibold text-on-surface">
            No setup-capable adapters found
          </h3>
          <p className="mt-2 max-w-2xl text-sm text-secondary">
            Adapter manifests must declare settings and actions before this
            screen can render setup controls.
          </p>
        </div>
      ) : (
        <div className="grid gap-6 xl:grid-cols-2">
          {adapters.map((adapter) => {
            const adapterID = adapter.adapter?.id || adapter.name;
            const adapterForms = forms[adapterID] ?? [];
            const labels = capabilityLabels(adapter);

            return (
              <section
                key={adapterID}
                className="overflow-hidden rounded-[1.75rem] border border-outline-variant/10 bg-surface-container-lowest shadow-sm"
              >
                <header className="flex items-start justify-between gap-4 border-b border-outline-variant/10 p-6">
                  <div className="space-y-2">
                    <h2 className="text-2xl font-semibold text-on-surface">
                      {adapter.adapter?.display_name || adapter.name}
                    </h2>
                    <p className="max-w-xl text-sm text-secondary">
                      {adapter.summary ||
                        adapter.adapter?.description ||
                        "Messaging adapter"}
                    </p>
                    {labels.length > 0 && (
                      <div className="flex flex-wrap gap-2 pt-1">
                        {labels.map((label) => (
                          <span
                            className="rounded-full border border-outline-variant/10 bg-surface-container px-3 py-1 text-[11px] font-semibold text-secondary"
                            key={label}
                          >
                            {label}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                  <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                    <Icon className="text-2xl" name="forum" />
                  </div>
                </header>

                <div className="space-y-5 p-6">
                  {adapterForms.map((form) => {
                    const connection = connections.find(
                      (item) => item.id === form.connectionID,
                    );
                    const status = connectionStatus(connection);
                    const busy =
                      actionState?.formKey === form.formKey &&
                      actionState.busy;
                    const formFeedback = feedback[form.formKey] ?? null;
                    const allowRemove = adapterForms.length > 1 || form.isExisting;
                    return (
                      <ConnectionFormCard
                        key={form.formKey}
                        actionState={actionState}
                        adapter={adapter}
                        allowRemoveDraft={allowRemove}
                        busy={busy}
                        feedback={formFeedback}
                        form={form}
                        status={status}
                        onDisconnect={() => void disconnect(form)}
                        onRemoveDraft={() => removeDraft(adapter, form.formKey)}
                        onRunAction={(action) =>
                          void runAction(adapter, form, action)
                        }
                        onSubmit={(event) => handleSubmit(event, adapter, form)}
                        onUpdateField={(key, value) =>
                          updateField(adapterID, form.formKey, key, value)
                        }
                        onUpdateForm={(patch) =>
                          updateForm(adapterID, form.formKey, patch)
                        }
                      />
                    );
                  })}

                  <button
                    className="inline-flex items-center gap-2 self-start rounded-full border border-dashed border-outline-variant/30 bg-transparent px-4 py-2 text-sm font-semibold text-secondary transition hover:border-primary/40 hover:text-primary"
                    onClick={() => addDraft(adapter)}
                    type="button"
                  >
                    <Icon className="text-base" name="add" />
                    Add connection
                  </button>
                </div>
              </section>
            );
          })}
        </div>
      )}
    </SettingsPage>
  );
}

function ConnectionFormCard({
  actionState,
  adapter,
  allowRemoveDraft,
  busy,
  feedback,
  form,
  status,
  onDisconnect,
  onRemoveDraft,
  onRunAction,
  onSubmit,
  onUpdateField,
  onUpdateForm,
}: {
  actionState: ActionState | null;
  adapter: MessagingAdapterInfo;
  allowRemoveDraft: boolean;
  busy: boolean;
  feedback: AdapterActionFeedback | null;
  form: AdapterFormState;
  status: string;
  onDisconnect: () => void;
  onRemoveDraft: () => void;
  onRunAction: (action: MessagingAction) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onUpdateField: (key: string, value: string) => void;
  onUpdateForm: (patch: Partial<AdapterFormState>) => void;
}) {
  const adapterID = adapter.adapter?.id || adapter.name;
  const validationIssues = feedback?.result?.validation?.issues ?? [];
  const showFeedback = Boolean(feedback?.error || feedback?.result);
  const headingLabel = form.isExisting
    ? form.label || form.connectionID
    : "New connection";
  const disconnecting =
    busy && actionState?.actionID === "__disconnect__";

  return (
    <form
      className="rounded-[1.5rem] border border-outline-variant/10 bg-surface-container-low p-5 shadow-sm"
      onSubmit={onSubmit}
    >
      <div className="flex flex-col gap-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="space-y-1">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              {form.isExisting ? "Connection" : "Draft"}
            </p>
            <h3 className="text-lg font-semibold text-on-surface">
              {headingLabel}
            </h3>
            {form.isExisting && (
              <p className="font-mono text-xs text-secondary">
                {form.connectionID}
              </p>
            )}
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge tone={statusTone(status)}>{status}</StatusBadge>
            {!form.isExisting && allowRemoveDraft && (
              <button
                className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 px-3 py-1 text-xs font-semibold text-secondary hover:text-on-surface"
                onClick={onRemoveDraft}
                type="button"
              >
                <Icon className="text-xs" name="close" />
                Discard
              </button>
            )}
          </div>
        </div>

        <div className="grid gap-4 md:grid-cols-2">
          <label className="space-y-2">
            <span className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
              Connection ID
            </span>
            <input
              className="w-full rounded-2xl border border-outline-variant/15 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition focus:border-primary/40 focus:bg-surface-container-lowest disabled:cursor-not-allowed disabled:opacity-70"
              disabled={form.isExisting}
              onChange={(event) =>
                onUpdateForm({ connectionID: event.target.value })
              }
              placeholder={defaultConnectionID(adapterID)}
              value={form.connectionID}
            />
            <FieldIssues
              issues={issuesFor(validationIssues, "connection_id")}
            />
            {form.isExisting && (
              <p className="text-xs text-secondary">
                Connection ID is fixed once saved. Add a new connection to
                use a different ID.
              </p>
            )}
          </label>
          <label className="space-y-2">
            <span className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
              Label
            </span>
            <input
              className="w-full rounded-2xl border border-outline-variant/15 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition focus:border-primary/40 focus:bg-surface-container-lowest"
              onChange={(event) => onUpdateForm({ label: event.target.value })}
              placeholder={defaultAdapterLabel(adapter)}
              value={form.label}
            />
            <FieldIssues issues={issuesFor(validationIssues, "label")} />
          </label>
        </div>

        <div className="grid gap-4 md:grid-cols-2">
          {(adapter.settings ?? []).map((setting) => (
            <AdapterSettingField
              issues={issuesFor(validationIssues, setting.key)}
              key={setting.key}
              onChange={(value) => onUpdateField(setting.key, value)}
              setting={setting}
              value={form.values[setting.key] ?? ""}
            />
          ))}
          <label className="space-y-2">
            <span className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
              Secret scope
            </span>
            <select
              className="w-full rounded-2xl border border-outline-variant/15 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition focus:border-primary/40 focus:bg-surface-container-lowest"
              onChange={(event) =>
                onUpdateForm({
                  secretScope: event.target.value as "current" | "trusted",
                })
              }
              value={form.secretScope}
            >
              <option value="current">Current device only</option>
              <option value="trusted">Trusted devices</option>
            </select>
            <p className="text-xs text-secondary">
              Applies when a credential field is provided.
            </p>
            <FieldIssues
              issues={issuesFor(validationIssues, "secret_scope")}
            />
          </label>
        </div>

        <div className="flex flex-wrap items-center gap-3 border-t border-outline-variant/10 pt-5">
          {(adapter.actions ?? []).map((action) => (
            <button
              className={`inline-flex items-center gap-2 rounded-full px-4 py-2 text-sm font-semibold transition active:scale-[0.98] ${
                action.primary
                  ? "bg-primary text-on-primary shadow-sm hover:shadow-md"
                  : "border border-outline-variant/20 bg-surface-container-low text-secondary hover:bg-surface-container hover:text-on-surface"
              }`}
              disabled={busy}
              key={action.id}
              onClick={
                action.kind === "connect"
                  ? undefined
                  : () => onRunAction(action)
              }
              type={action.kind === "connect" ? "submit" : "button"}
            >
              <Icon
                className="text-base"
                name={
                  action.kind === "open_url"
                    ? "open_in_new"
                    : action.kind === "validate_config"
                      ? "check_circle"
                      : action.kind === "extract_credentials"
                        ? "download"
                        : "link"
                }
              />
              {busy && actionState?.actionID === action.id
                ? "Working..."
                : action.label}
            </button>
          ))}
          {form.isExisting && (
            <button
              className="ml-auto inline-flex items-center gap-2 rounded-full border border-error/30 px-4 py-2 text-sm font-semibold text-error transition hover:bg-error/10 disabled:opacity-60"
              disabled={busy}
              onClick={onDisconnect}
              type="button"
            >
              <Icon
                className={
                  disconnecting ? "animate-spin text-base" : "text-base"
                }
                name={disconnecting ? "sync" : "link_off"}
              />
              {disconnecting ? "Disconnecting..." : "Disconnect"}
            </button>
          )}
        </div>

        {showFeedback && feedback ? (
          <ActionResultPanel error={feedback.error} result={feedback.result} />
        ) : null}
      </div>
    </form>
  );
}

function AdapterSettingField({
  issues,
  onChange,
  setting,
  value,
}: {
  issues: MessagingValidationIssue[];
  onChange: (value: string) => void;
  setting: MessagingSetting;
  value: string;
}) {
  if (setting.kind === "select") {
    return (
      <label className="space-y-2">
        <span className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
          {setting.label}
          {setting.required ? " *" : ""}
        </span>
        <select
          className="w-full rounded-2xl border border-outline-variant/15 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition focus:border-primary/40 focus:bg-surface-container-lowest"
          onChange={(event) => onChange(event.target.value)}
          value={value}
        >
          <option value="">Select...</option>
          {(setting.options ?? []).map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        {setting.description && (
          <p className="text-xs text-secondary">{setting.description}</p>
        )}
        <FieldIssues issues={issues} />
      </label>
    );
  }

  if (setting.kind === "boolean") {
    return (
      <div className="space-y-2">
        <label className="flex items-start gap-3 rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4">
          <input
            checked={value === "true"}
            className="mt-1 h-4 w-4"
            onChange={(event) =>
              onChange(event.target.checked ? "true" : "false")
            }
            type="checkbox"
          />
          <span className="space-y-1">
            <span className="block text-sm font-semibold text-on-surface">
              {setting.label}
            </span>
            {setting.description && (
              <span className="block text-xs text-secondary">
                {setting.description}
              </span>
            )}
          </span>
        </label>
        <FieldIssues issues={issues} />
      </div>
    );
  }

  return (
    <label className="space-y-2">
      <span className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
        {setting.label}
        {setting.required ? " *" : ""}
      </span>
      <input
        autoComplete={setting.target === "credential" ? "off" : undefined}
        className="w-full rounded-2xl border border-outline-variant/15 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition focus:border-primary/40 focus:bg-surface-container-lowest"
        onChange={(event) => onChange(event.target.value)}
        placeholder={setting.placeholder || setting.default || ""}
        type={fieldType(setting)}
        value={value}
      />
      {setting.description && (
        <p className="text-xs text-secondary">{setting.description}</p>
      )}
      <FieldIssues issues={issues} />
    </label>
  );
}

function FieldIssues({ issues }: { issues: MessagingValidationIssue[] }) {
  if (issues.length === 0) return null;
  return (
    <div className="space-y-1">
      {issues.map((issue, index) => {
        const classes = issueClasses(issue.severity);
        return (
          <p
            className={`rounded-xl border px-3 py-2 text-xs ${classes.border} ${classes.bg} ${classes.text}`}
            key={`${issue.code ?? issue.severity}:${index}`}
          >
            {issue.message}
          </p>
        );
      })}
    </div>
  );
}

function ActionResultPanel({
  error,
  result,
}: {
  error: string | null;
  result: MessagingRunAdapterActionResult | null;
}) {
  const allIssues = result?.validation?.issues ?? [];
  const globalIssues = allIssues.filter((issue) => !issue.field);
  const connected = result?.connect?.connection;

  return (
    <section className="rounded-[1.5rem] border border-outline-variant/10 bg-surface-container p-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Last action
          </p>
          <h3 className="mt-1 text-xl font-semibold text-on-surface">
            {error
              ? "Action failed"
              : connected
                ? `${connected.label} connected`
                : allIssues.length > 0
                  ? "Validation needs changes"
                : result?.action_kind === "open_url"
                  ? "Opened setup link"
                  : "Validation complete"}
          </h3>
        </div>
        <Icon
          className={`text-2xl ${error ? "text-error" : "text-primary"}`}
          name={error ? "error" : "task_alt"}
        />
      </div>

      {error && (
        <div className="mt-4 rounded-2xl border border-error/20 bg-error/10 p-4 text-sm text-error">
          {error}
        </div>
      )}

      {!error && globalIssues.length > 0 && (
        <div className="mt-4 space-y-2">
          {globalIssues.map((issue, index) => {
            const classes = issueClasses(issue.severity);
            return (
              <div
                className={`rounded-2xl border p-4 text-sm ${classes.border} ${classes.bg}`}
                key={`${issue.code ?? issue.severity}:${index}`}
              >
                <span
                  className={`font-semibold uppercase tracking-[0.12em] ${classes.text}`}
                >
                  {issue.severity}
                </span>
                <span className="ml-2 text-on-surface">{issue.message}</span>
              </div>
            );
          })}
        </div>
      )}

      {!error && allIssues.length > 0 && globalIssues.length === 0 && (
        <p className="mt-4 text-sm text-secondary">
          Fix the highlighted fields above.
        </p>
      )}

      {!error && result && allIssues.length === 0 && (
        <p className="mt-4 text-sm text-secondary">
          {connected
            ? `Connection ${connected.id} is ${connected.status ?? "updated"}.`
            : "No validation issues returned."}
        </p>
      )}
    </section>
  );
}
