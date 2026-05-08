import { useEffect, useState, type FormEvent } from "react";
import {
  DiscordIcon,
  IMessageIcon,
  SignalIcon,
  SlackIcon,
  TelegramIcon,
  WhatsAppIcon,
} from "../components/BrandIcon";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { StatusBadge } from "../components/StatusBadge";
import {
  messaging,
  secrets,
  type MessagingAction,
  type MessagingAdapterInfo,
  type MessagingConnection,
  type MessagingRunAdapterActionResult,
  type MessagingSetting,
  type MessagingValidationIssue,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

type PolicyRules = {
  read_inbound: boolean;
  mark_read: boolean;
  create_drafts: boolean;
  send_messages: boolean;
  require_approval: boolean;
  reply_only: boolean;
  allow_new_conversations: boolean;
  allow_attachments: boolean;
  manage_messages: boolean;
  search_identities: boolean;
  search_conversations: boolean;
  search_messages: boolean;
};

function defaultPolicyRules(): PolicyRules {
  return {
    read_inbound: true,
    mark_read: false,
    create_drafts: true,
    send_messages: false,
    require_approval: true,
    reply_only: true,
    allow_new_conversations: false,
    allow_attachments: false,
    manage_messages: false,
    search_identities: true,
    search_conversations: true,
    search_messages: true,
  };
}

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

function adapterIconName(adapterID: string) {
  const id = adapterID.toLowerCase();
  if (id.includes("telegram")) return "send";
  if (id.includes("teams")) return "groups";
  if (id.includes("discord")) return "chat";
  if (
    id.includes("imap") ||
    id.includes("smtp") ||
    id.includes("mail") ||
    id.includes("email")
  )
    return "mail";
  return "forum";
}

function brandIconFor(adapterID: string) {
  const id = adapterID.toLowerCase();
  if (id.includes("slack")) return SlackIcon;
  if (id.includes("telegram")) return TelegramIcon;
  if (id.includes("whatsapp")) return WhatsAppIcon;
  if (id.includes("signal")) return SignalIcon;
  if (id.includes("imessage")) return IMessageIcon;
  if (id.includes("discord")) return DiscordIcon;
  return null;
}

function AdapterIcon({
  adapterID,
  size = "lg",
}: {
  adapterID: string;
  size?: "sm" | "lg";
}) {
  const Brand = brandIconFor(adapterID);
  const tile =
    size === "sm"
      ? "flex h-9 w-9 shrink-0 items-center justify-center rounded-xl"
      : "flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl";
  const tone = Brand
    ? "bg-white border border-outline-variant/15 dark:bg-surface-container"
    : "bg-primary/10 text-primary";

  return (
    <div className={`${tile} ${tone}`}>
      {Brand ? (
        <Brand className={size === "sm" ? "h-5 w-5" : "h-7 w-7"} />
      ) : (
        <Icon
          className={size === "sm" ? "text-lg" : "text-2xl"}
          name={adapterIconName(adapterID)}
        />
      )}
    </div>
  );
}

type ComingSoonAdapter = {
  id: string;
  displayName: string;
  description: string;
};

const COMING_SOON_ADAPTERS: ComingSoonAdapter[] = [
  {
    id: "whatsapp",
    displayName: "WhatsApp",
    description:
      "Business API and personal-account access for one-to-one and group chats.",
  },
  {
    id: "signal",
    displayName: "Signal",
    description:
      "End-to-end encrypted messaging via signal-cli style bridges.",
  },
  {
    id: "imessage",
    displayName: "iMessage",
    description: "Native macOS bridge for iMessage and SMS conversations.",
  },
  {
    id: "discord",
    displayName: "Discord",
    description: "Bot and user-account integrations for servers, DMs, and threads.",
  },
];

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
  const availableAdapterIDs = new Set(
    adapters.map((adapter) => adapter.adapter?.id || adapter.name),
  );
  const comingSoonAdapters = COMING_SOON_ADAPTERS.filter(
    (adapter) => !availableAdapterIDs.has(adapter.id),
  );
  const connections = connectionData?.connections ?? [];
  const [forms, setForms] = useState<Record<string, AdapterFormState[]>>({});
  const [actionState, setActionState] = useState<ActionState | null>(null);
  const [feedback, setFeedback] = useState<
    Record<string, AdapterActionFeedback>
  >({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [picker, setPicker] = useState<{
    step: "platform" | "configure" | "policies";
    adapterID: string | null;
    formKey: string | null;
  } | null>(null);
  const [policiesByConnection, setPoliciesByConnection] = useState<
    Record<string, PolicyRules>
  >({});
  const [policiesModalConnectionID, setPoliciesModalConnectionID] = useState<
    string | null
  >(null);

  function policyFor(connectionID: string): PolicyRules {
    return policiesByConnection[connectionID] ?? defaultPolicyRules();
  }

  function setPolicyFor(connectionID: string, next: PolicyRules) {
    setPoliciesByConnection((current) => ({ ...current, [connectionID]: next }));
  }

  function openPicker(adapter?: MessagingAdapterInfo) {
    if (!adapter) {
      setPicker({ step: "platform", adapterID: null, formKey: null });
      return;
    }
    const adapterID = adapter.adapter?.id || adapter.name;
    const formKey = addDraft(adapter, { autoExpand: false });
    if (formKey) {
      setPicker({ step: "configure", adapterID, formKey });
    }
  }

  function closePicker() {
    setPicker((current) => {
      if (
        current?.step === "configure" &&
        current.adapterID &&
        current.formKey
      ) {
        const adapter = adapters.find(
          (a) => (a.adapter?.id || a.name) === current.adapterID,
        );
        const draft = forms[current.adapterID]?.find(
          (f) => f.formKey === current.formKey,
        );
        if (adapter && draft && !draft.isExisting) {
          removeDraft(adapter, current.formKey);
        }
      }
      return null;
    });
  }

  function toggleExpanded(formKey: string) {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(formKey)) next.delete(formKey);
      else next.add(formKey);
      return next;
    });
  }

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

  function addDraft(
    adapter: MessagingAdapterInfo,
    options: { autoExpand?: boolean } = {},
  ): string {
    const { autoExpand = true } = options;
    const adapterID = adapter.adapter?.id || adapter.name;
    const existing = forms[adapterID] ?? [];
    const used = new Set(existing.map((form) => form.connectionID));
    for (const connection of connections) {
      if (connection.adapter_id === adapterID) used.add(connection.id);
    }
    const draft = initialDraftState(
      adapter,
      suggestConnectionID(adapterID, used),
    );
    setForms((current) => {
      const list = current[adapterID] ?? [];
      return { ...current, [adapterID]: [...list, draft] };
    });
    if (autoExpand) {
      setExpanded((current) => {
        const next = new Set(current);
        next.add(draft.formKey);
        return next;
      });
    }
    return draft.formKey;
  }

  function removeDraft(adapter: MessagingAdapterInfo, formKey: string) {
    const adapterID = adapter.adapter?.id || adapter.name;
    setForms((current) => {
      const existing = current[adapterID] ?? [];
      const filtered = existing.filter((form) => form.formKey !== formKey);
      return { ...current, [adapterID]: filtered };
    });
    setFeedback((current) => {
      if (!(formKey in current)) return current;
      const next = { ...current };
      delete next[formKey];
      return next;
    });
    setExpanded((current) => {
      if (!current.has(formKey)) return current;
      const next = new Set(current);
      next.delete(formKey);
      return next;
    });
  }

  async function disconnect(form: AdapterFormState) {
    if (!form.isExisting) return;
    const ok = window.confirm(
      `Disconnect ${form.label || form.connectionID}?`,
    );
    if (!ok) return;

    const connection = connections.find(
      (item) => item.id === form.connectionID,
    );
    const credentialRef = connection?.auth?.credential_ref ?? "";
    const alsoDeleteSecret =
      credentialRef !== "" &&
      window.confirm(
        `Also delete the credential secret "${credentialRef}"? This can't be undone. Cancel to keep the secret in place.`,
      );

    setActionState({
      formKey: form.formKey,
      actionID: "__disconnect__",
      busy: true,
    });
    let secretError: string | null = null;
    try {
      await messaging.deleteConnection({ connection_id: form.connectionID });
      if (alsoDeleteSecret && credentialRef) {
        try {
          await secrets.delete({ id_or_name: credentialRef });
        } catch (error) {
          secretError =
            error instanceof Error
              ? `Connection removed but credential secret delete failed: ${error.message}`
              : "Connection removed but credential secret delete failed";
        }
      }
      setFeedback((current) => {
        const next = { ...current };
        delete next[form.formKey];
        if (secretError) {
          next[form.formKey] = { error: secretError, result: null };
        }
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
        setExpanded((current) => {
          if (!current.has(form.formKey)) return current;
          const next = new Set(current);
          next.delete(form.formKey);
          next.add(persistedID);
          return next;
        });
        setPicker((current) => {
          if (!current || current.formKey !== form.formKey) return current;
          return { ...current, formKey: persistedID };
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
      actions={
        adapters.length > 0 ? (
          <button
            className="inline-flex items-center gap-2 rounded-full bg-emerald-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-emerald-700 hover:shadow-md active:scale-[0.98]"
            onClick={() => openPicker()}
            type="button"
          >
            <Icon className="text-base" name="add" />
            Add connection
          </button>
        ) : undefined
      }
      backHref="/settings"
      description="Connect messaging platforms through adapter manifests. Sky10 stores platform credentials in secrets and keeps policy decisions in the broker."
      pinnablePageID="messaging"
      title="Messaging"
      width="wide"
    >
      <section className="grid gap-4 sm:grid-cols-3">
        <StatCard
          icon="apps"
          label="Adapters available"
          value={String(adapterData?.count ?? 0)}
        />
        <StatCard
          icon="hub"
          label="Total connections"
          value={String(connectionData?.count ?? 0)}
        />
        <StatCard
          accent="success"
          icon="lock"
          label="Connection security"
          subtitle="Encrypted at rest"
          value="Stored securely"
        />
      </section>

      {(adapterError || connectionError) && (
        <div className="rounded-2xl border border-error/20 bg-error/10 p-4 text-sm text-error">
          {adapterError || connectionError}
        </div>
      )}

      {adaptersLoading && adapters.length === 0 ? (
        <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 text-sm text-secondary">
          Loading messaging adapters...
        </div>
      ) : adapters.length === 0 ? (
        <div className="space-y-6">
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8">
            <h3 className="text-xl font-semibold text-on-surface">
              No setup-capable adapters found
            </h3>
            <p className="mt-2 max-w-2xl text-sm text-secondary">
              Adapter manifests must declare settings and actions before this
              screen can render setup controls.
            </p>
          </div>
          {comingSoonAdapters.map((adapter) => (
            <ComingSoonAdapterSection adapter={adapter} key={adapter.id} />
          ))}
        </div>
      ) : (
        <div className="space-y-6">
          {adapters.map((adapter) => {
            const adapterID = adapter.adapter?.id || adapter.name;
            const adapterForms = forms[adapterID] ?? [];
            const adapterConnections = connections.filter(
              (item) => item.adapter_id === adapterID,
            );
            const displayName = adapter.adapter?.display_name || adapter.name;
            const connectionCount = adapterConnections.length;

            return (
              <section
                key={adapterID}
                className="overflow-hidden rounded-[1.5rem] border border-outline-variant/10 bg-surface-container-lowest shadow-sm"
              >
                <header className="flex items-start justify-between gap-4 p-6">
                  <div className="flex items-start gap-4">
                    <AdapterIcon adapterID={adapterID} />
                    <div className="space-y-1">
                      <h2 className="text-xl font-semibold text-on-surface">
                        {displayName}
                      </h2>
                      <p className="max-w-xl text-sm text-secondary">
                        {adapter.summary ||
                          adapter.adapter?.description ||
                          "Messaging adapter"}
                      </p>
                    </div>
                  </div>
                  <div className="text-right">
                    <p className="text-2xl font-semibold text-on-surface">
                      {connectionCount}
                    </p>
                    <p className="text-xs text-secondary">
                      {connectionCount === 1 ? "connection" : "connections"}
                    </p>
                  </div>
                </header>

                {adapterForms.length > 0 && (
                  <ul className="divide-y divide-outline-variant/10 border-t border-outline-variant/10">
                    {adapterForms.map((form) => {
                      const connection = connections.find(
                        (item) => item.id === form.connectionID,
                      );
                      const status = connectionStatus(connection);
                      const busy =
                        actionState?.formKey === form.formKey &&
                        actionState.busy;
                      const formFeedback = feedback[form.formKey] ?? null;
                      const isExpanded = expanded.has(form.formKey);
                      return (
                        <ConnectionRow
                          key={form.formKey}
                          actionState={actionState}
                          adapter={adapter}
                          busy={busy}
                          expanded={isExpanded}
                          feedback={formFeedback}
                          form={form}
                          status={status}
                          onDisconnect={() => void disconnect(form)}
                          onOpenPolicies={() =>
                            setPoliciesModalConnectionID(form.connectionID)
                          }
                          onRemoveDraft={() =>
                            removeDraft(adapter, form.formKey)
                          }
                          onRunAction={(action) =>
                            void runAction(adapter, form, action)
                          }
                          onSubmit={(event) =>
                            handleSubmit(event, adapter, form)
                          }
                          onToggleExpanded={() =>
                            toggleExpanded(form.formKey)
                          }
                          onUpdateField={(key, value) =>
                            updateField(adapterID, form.formKey, key, value)
                          }
                          onUpdateForm={(patch) =>
                            updateForm(adapterID, form.formKey, patch)
                          }
                        />
                      );
                    })}
                  </ul>
                )}
                <div className="border-t border-outline-variant/10 px-6 py-4">
                  <button
                    className="inline-flex items-center gap-2 rounded-full border border-dashed border-outline-variant/30 bg-transparent px-4 py-2 text-sm font-semibold text-secondary transition hover:border-primary/40 hover:text-primary"
                    onClick={() => openPicker(adapter)}
                    type="button"
                  >
                    <Icon className="text-base" name="add" />
                    Add {displayName} connection
                  </button>
                </div>
              </section>
            );
          })}

          {comingSoonAdapters.map((adapter) => (
            <ComingSoonAdapterSection adapter={adapter} key={adapter.id} />
          ))}
        </div>
      )}

      {policiesModalConnectionID && (
        <PoliciesModal
          adapter={
            adapters.find(
              (a) =>
                connections.find(
                  (c) => c.id === policiesModalConnectionID,
                )?.adapter_id === (a.adapter?.id || a.name),
            ) ?? null
          }
          connectionID={policiesModalConnectionID}
          connectionLabel={
            connections.find((c) => c.id === policiesModalConnectionID)
              ?.label || policiesModalConnectionID
          }
          initialValue={policyFor(policiesModalConnectionID)}
          onClose={() => setPoliciesModalConnectionID(null)}
          onSave={(next) => {
            setPolicyFor(policiesModalConnectionID, next);
            setPoliciesModalConnectionID(null);
          }}
        />
      )}

      {picker && (
        <AddConnectionWizard
          actionState={actionState}
          adapters={adapters}
          feedback={
            picker.formKey ? (feedback[picker.formKey] ?? null) : null
          }
          form={
            picker.adapterID && picker.formKey
              ? (forms[picker.adapterID]?.find(
                  (f) => f.formKey === picker.formKey,
                ) ?? null)
              : null
          }
          pickerAdapter={
            picker.adapterID
              ? (adapters.find(
                  (a) => (a.adapter?.id || a.name) === picker.adapterID,
                ) ?? null)
              : null
          }
          step={picker.step}
          onBack={() => {
            if (picker.step === "configure") {
              if (picker.adapterID && picker.formKey) {
                const adapter = adapters.find(
                  (a) => (a.adapter?.id || a.name) === picker.adapterID,
                );
                if (adapter) removeDraft(adapter, picker.formKey);
              }
              setPicker({
                step: "platform",
                adapterID: null,
                formKey: null,
              });
            }
          }}
          onAdvance={() => {
            setPicker((current) =>
              current && current.step === "configure"
                ? { ...current, step: "policies" }
                : current,
            );
          }}
          onClose={closePicker}
          onPickPlatform={(adapter) => {
            const adapterID = adapter.adapter?.id || adapter.name;
            const formKey = addDraft(adapter, { autoExpand: false });
            if (formKey) {
              setPicker({ step: "configure", adapterID, formKey });
            }
          }}
          onPolicyChange={(connectionID, next) => {
            setPolicyFor(connectionID, next);
          }}
          onRunAction={(adapter, form, action) =>
            void runAction(adapter, form, action)
          }
          onUpdateField={(adapterID, formKey, key, value) =>
            updateField(adapterID, formKey, key, value)
          }
          onUpdateForm={(adapterID, formKey, patch) =>
            updateForm(adapterID, formKey, patch)
          }
          policyFor={policyFor}
        />
      )}
    </SettingsPage>
  );
}

function ConnectionFormCard({
  actionState,
  adapter,
  busy,
  feedback,
  form,
  onDisconnect,
  onRunAction,
  onSubmit,
  onUpdateField,
  onUpdateForm,
}: {
  actionState: ActionState | null;
  adapter: MessagingAdapterInfo;
  busy: boolean;
  feedback: AdapterActionFeedback | null;
  form: AdapterFormState;
  onDisconnect: () => void;
  onRunAction: (action: MessagingAction) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onUpdateField: (key: string, value: string) => void;
  onUpdateForm: (patch: Partial<AdapterFormState>) => void;
}) {
  const adapterID = adapter.adapter?.id || adapter.name;
  const validationIssues = feedback?.result?.validation?.issues ?? [];
  const showFeedback = Boolean(feedback?.error || feedback?.result);
  const disconnecting =
    busy && actionState?.actionID === "__disconnect__";

  return (
    <form className="flex flex-col gap-5" onSubmit={onSubmit}>
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
              Connection ID is fixed once saved. Add a new connection to use a
              different ID.
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

function ComingSoonAdapterSection({
  adapter,
}: {
  adapter: ComingSoonAdapter;
}) {
  return (
    <section className="overflow-hidden rounded-[1.5rem] border border-outline-variant/10 bg-surface-container-lowest opacity-80 shadow-sm">
      <header className="flex items-start justify-between gap-4 p-6">
        <div className="flex items-start gap-4">
          <AdapterIcon adapterID={adapter.id} />
          <div className="space-y-1">
            <h2 className="text-xl font-semibold text-on-surface">
              {adapter.displayName}
            </h2>
            <p className="max-w-xl text-sm text-secondary">
              {adapter.description}
            </p>
          </div>
        </div>
        <span className="inline-flex shrink-0 items-center gap-1 rounded-full border border-outline-variant/20 bg-surface-container-low px-3 py-1 text-xs font-semibold uppercase tracking-wide text-secondary">
          <Icon className="text-sm" name="schedule" />
          Coming soon
        </span>
      </header>
    </section>
  );
}

function StatCard({
  accent = "primary",
  icon,
  label,
  subtitle,
  value,
}: {
  accent?: "primary" | "success";
  icon: string;
  label: string;
  subtitle?: string;
  value: string;
}) {
  const tone =
    accent === "success"
      ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
      : "bg-primary/10 text-primary";
  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
      <div className="flex items-center gap-3">
        <div
          className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ${tone}`}
        >
          <Icon className="text-xl" name={icon} />
        </div>
        <div className="min-w-0">
          <p className="truncate text-base font-semibold text-on-surface">
            {value}
          </p>
          <p className="truncate text-xs text-secondary">
            {subtitle ? `${label} • ${subtitle}` : label}
          </p>
        </div>
      </div>
    </div>
  );
}

function ConnectionRow({
  actionState,
  adapter,
  busy,
  expanded,
  feedback,
  form,
  status,
  onDisconnect,
  onOpenPolicies,
  onRemoveDraft,
  onRunAction,
  onSubmit,
  onToggleExpanded,
  onUpdateField,
  onUpdateForm,
}: {
  actionState: ActionState | null;
  adapter: MessagingAdapterInfo;
  busy: boolean;
  expanded: boolean;
  feedback: AdapterActionFeedback | null;
  form: AdapterFormState;
  status: string;
  onDisconnect: () => void;
  onOpenPolicies: () => void;
  onRemoveDraft: () => void;
  onRunAction: (action: MessagingAction) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onToggleExpanded: () => void;
  onUpdateField: (key: string, value: string) => void;
  onUpdateForm: (patch: Partial<AdapterFormState>) => void;
}) {
  const adapterID = adapter.adapter?.id || adapter.name;
  const heading = form.isExisting
    ? form.label || form.connectionID
    : "New connection";

  return (
    <li>
      <div className="flex items-center justify-between gap-4 px-6 py-4">
        <button
          aria-expanded={expanded}
          className="flex min-w-0 flex-1 items-center gap-3 text-left"
          onClick={onToggleExpanded}
          type="button"
        >
          {form.isExisting ? (
            <AdapterIcon adapterID={adapterID} size="sm" />
          ) : (
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <Icon className="text-lg" name="edit" />
            </div>
          )}
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-on-surface">
              {heading}
            </p>
            <p className="truncate font-mono text-xs text-secondary">
              {form.connectionID}
            </p>
          </div>
        </button>
        <div className="flex shrink-0 items-center gap-2">
          <StatusBadge tone={statusTone(status)}>{status}</StatusBadge>
          {!form.isExisting && (
            <button
              className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 px-3 py-1 text-xs font-semibold text-secondary hover:text-on-surface"
              onClick={onRemoveDraft}
              type="button"
            >
              <Icon className="text-xs" name="close" />
              Discard
            </button>
          )}
          {form.isExisting && (
            <button
              className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 bg-surface-container-low px-3 py-1 text-xs font-semibold text-secondary transition hover:bg-surface-container hover:text-on-surface"
              onClick={onOpenPolicies}
              type="button"
            >
              <Icon className="text-sm" name="policy" />
              Policies
            </button>
          )}
          {form.isExisting && (
            <button
              className="inline-flex items-center gap-1 rounded-full border border-error/30 px-3 py-1 text-xs font-semibold text-error transition hover:bg-error/10"
              onClick={onDisconnect}
              type="button"
            >
              <Icon className="text-sm" name="delete" />
              Delete
            </button>
          )}
          <button
            aria-label={expanded ? "Collapse" : "Manage"}
            className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 bg-surface-container-low px-3 py-1 text-xs font-semibold text-secondary transition hover:bg-surface-container hover:text-on-surface"
            onClick={onToggleExpanded}
            type="button"
          >
            <span className="hidden sm:inline">
              {expanded ? "Close" : "Manage"}
            </span>
            <Icon
              className="text-base"
              name={expanded ? "expand_less" : "expand_more"}
            />
          </button>
        </div>
      </div>
      {expanded && (
        <div className="border-t border-outline-variant/10 bg-surface-container-low px-6 py-5">
          <ConnectionFormCard
            actionState={actionState}
            adapter={adapter}
            busy={busy}
            feedback={feedback}
            form={form}
            onDisconnect={onDisconnect}
            onRunAction={onRunAction}
            onSubmit={onSubmit}
            onUpdateField={onUpdateField}
            onUpdateForm={onUpdateForm}
          />
        </div>
      )}
    </li>
  );
}

type WizardStep = "platform" | "configure" | "policies";

const WIZARD_STEPS: { id: WizardStep; label: string }[] = [
  { id: "platform", label: "Choose platform" },
  { id: "configure", label: "Configure connection" },
  { id: "policies", label: "Set policies" },
];

function AddConnectionWizard({
  actionState,
  adapters,
  feedback,
  form,
  pickerAdapter,
  policyFor,
  step,
  onAdvance,
  onBack,
  onClose,
  onPickPlatform,
  onPolicyChange,
  onRunAction,
  onUpdateField,
  onUpdateForm,
}: {
  actionState: ActionState | null;
  adapters: MessagingAdapterInfo[];
  feedback: AdapterActionFeedback | null;
  form: AdapterFormState | null;
  pickerAdapter: MessagingAdapterInfo | null;
  policyFor: (connectionID: string) => PolicyRules;
  step: WizardStep;
  onAdvance: () => void;
  onBack: () => void;
  onClose: () => void;
  onPickPlatform: (adapter: MessagingAdapterInfo) => void;
  onPolicyChange: (connectionID: string, next: PolicyRules) => void;
  onRunAction: (
    adapter: MessagingAdapterInfo,
    form: AdapterFormState,
    action: MessagingAction,
  ) => void;
  onUpdateField: (
    adapterID: string,
    formKey: string,
    key: string,
    value: string,
  ) => void;
  onUpdateForm: (
    adapterID: string,
    formKey: string,
    patch: Partial<AdapterFormState>,
  ) => void;
}) {
  const stepIndex = WIZARD_STEPS.findIndex((entry) => entry.id === step);
  const adapterID = pickerAdapter?.adapter?.id || pickerAdapter?.name || "";
  const busy =
    !!form && actionState?.formKey === form.formKey && actionState.busy;

  return (
    <div
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="dialog"
    >
      <div
        className="flex max-h-[calc(100vh-2rem)] w-full max-w-4xl flex-col overflow-hidden rounded-[2rem] border border-outline-variant/10 bg-surface-container-lowest shadow-2xl"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="flex items-start justify-between gap-4 border-b border-outline-variant/10 p-8">
          <div className="min-w-0">
            <p className="text-xs font-bold uppercase tracking-[0.18em] text-outline">
              Step {stepIndex + 1} of {WIZARD_STEPS.length}
            </p>
            <h2 className="mt-2 truncate text-3xl font-semibold text-on-surface">
              {WIZARD_STEPS[stepIndex]?.label}
            </h2>
            {pickerAdapter && step !== "platform" && (
              <p className="mt-1 truncate text-base text-secondary">
                {pickerAdapter.adapter?.display_name || pickerAdapter.name}
              </p>
            )}
          </div>
          <button
            aria-label="Close"
            className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-secondary transition hover:bg-surface-container-low hover:text-on-surface"
            onClick={onClose}
            type="button"
          >
            <Icon className="text-2xl" name="close" />
          </button>
        </header>

        <ol className="flex items-center gap-2 border-b border-outline-variant/10 bg-surface-container-low px-8 py-4 text-sm">
          {WIZARD_STEPS.map((entry, index) => {
            const state =
              index < stepIndex
                ? "done"
                : index === stepIndex
                  ? "current"
                  : "todo";
            return (
              <li className="flex items-center gap-2" key={entry.id}>
                <span
                  className={`flex h-8 w-8 items-center justify-center rounded-full text-sm font-semibold ${
                    state === "done"
                      ? "bg-primary text-on-primary"
                      : state === "current"
                        ? "border border-primary text-primary"
                        : "border border-outline-variant/30 text-secondary"
                  }`}
                >
                  {state === "done" ? (
                    <Icon className="text-base" name="check" />
                  ) : (
                    index + 1
                  )}
                </span>
                <span
                  className={`hidden sm:inline ${
                    state === "current"
                      ? "font-semibold text-on-surface"
                      : "text-secondary"
                  }`}
                >
                  {entry.label}
                </span>
                {index < WIZARD_STEPS.length - 1 && (
                  <span className="mx-2 hidden h-px w-10 bg-outline-variant/30 sm:inline-block" />
                )}
              </li>
            );
          })}
        </ol>

        <div className="flex-1 overflow-y-auto">
          {step === "platform" && (
            <ul className="divide-y divide-outline-variant/10">
              {adapters.map((adapter) => {
                const id = adapter.adapter?.id || adapter.name;
                const displayName =
                  adapter.adapter?.display_name || adapter.name;
                const description =
                  adapter.summary ||
                  adapter.adapter?.description ||
                  "Messaging adapter";
                return (
                  <li key={id}>
                    <button
                      className="flex w-full items-center gap-4 px-8 py-5 text-left transition hover:bg-surface-container-low"
                      onClick={() => onPickPlatform(adapter)}
                      type="button"
                    >
                      <AdapterIcon adapterID={id} />
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-base font-semibold text-on-surface">
                          {displayName}
                        </p>
                        <p className="truncate text-sm text-secondary">
                          {description}
                        </p>
                      </div>
                      <Icon
                        className="text-2xl text-secondary"
                        name="arrow_forward"
                      />
                    </button>
                  </li>
                );
              })}
            </ul>
          )}

          {step === "configure" && pickerAdapter && form && (
            <ConfigureStep
              actionState={actionState}
              adapter={pickerAdapter}
              busy={busy}
              feedback={feedback}
              form={form}
              onRunAction={(action) =>
                onRunAction(pickerAdapter, form, action)
              }
              onUpdateField={(key, value) =>
                onUpdateField(adapterID, form.formKey, key, value)
              }
              onUpdateForm={(patch) =>
                onUpdateForm(adapterID, form.formKey, patch)
              }
            />
          )}

          {step === "policies" && pickerAdapter && form && (
            <PoliciesEditor
              value={policyFor(form.connectionID)}
              onChange={(next) => onPolicyChange(form.connectionID, next)}
            />
          )}
        </div>

        <footer className="flex items-center justify-between gap-3 border-t border-outline-variant/10 bg-surface-container-low px-8 py-5">
          <div className="text-sm text-secondary">
            {step === "platform" &&
              "You can add multiple connections of the same type."}
            {step === "configure" &&
              (form?.isExisting
                ? "Connected. Continue to set policies."
                : "Fill the required fields, then sign in or save credentials.")}
            {step === "policies" &&
              "Policies decide what agents can read, draft, or send."}
          </div>
          <div className="flex items-center gap-2">
            {step === "configure" && (
              <button
                className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 px-5 py-2.5 text-base font-semibold text-secondary hover:text-on-surface"
                onClick={onBack}
                type="button"
              >
                Back
              </button>
            )}
            {step === "configure" && (
              <button
                className="inline-flex items-center gap-1 rounded-full bg-emerald-600 px-5 py-2.5 text-base font-semibold text-white shadow-sm transition hover:bg-emerald-700 hover:shadow-md"
                onClick={onAdvance}
                type="button"
              >
                Set policies
                <Icon className="text-lg" name="arrow_forward" />
              </button>
            )}
            {step === "policies" && (
              <button
                className="inline-flex items-center gap-1 rounded-full bg-emerald-600 px-5 py-2.5 text-base font-semibold text-white shadow-sm transition hover:bg-emerald-700 hover:shadow-md"
                onClick={onClose}
                type="button"
              >
                Done
              </button>
            )}
          </div>
        </footer>
      </div>
    </div>
  );
}

function primaryConnectAction(
  adapter: MessagingAdapterInfo,
): MessagingAction | null {
  const actions = adapter.actions ?? [];
  return (
    actions.find((action) => action.kind === "connect") ??
    actions.find((action) => action.kind === "open_url" && action.primary) ??
    actions.find((action) => action.primary) ??
    actions[0] ??
    null
  );
}

function importActions(adapter: MessagingAdapterInfo): MessagingAction[] {
  return (adapter.actions ?? []).filter(
    (action) => action.kind === "extract_credentials",
  );
}

function actionIconName(kind: string) {
  switch (kind) {
    case "open_url":
      return "open_in_new";
    case "validate_config":
      return "check_circle";
    case "extract_credentials":
      return "download";
    default:
      return "link";
  }
}

function importIcon(actionID: string) {
  const id = actionID.toLowerCase();
  if (id.includes("desktop")) return "desktop_windows";
  if (id.includes("chrome")) return "public";
  if (id.includes("firefox")) return "local_fire_department";
  if (id.includes("brave")) return "shield";
  if (id.includes("safari")) return "explore";
  return "download";
}

function importShortLabel(action: MessagingAction) {
  // Strip the "Import from " prefix when present so the chip stays compact.
  return action.label.replace(/^Import from\s+/i, "");
}

function ConfigureStep({
  actionState,
  adapter,
  busy,
  feedback,
  form,
  onRunAction,
  onUpdateField,
  onUpdateForm,
}: {
  actionState: ActionState | null;
  adapter: MessagingAdapterInfo;
  busy: boolean;
  feedback: AdapterActionFeedback | null;
  form: AdapterFormState;
  onRunAction: (action: MessagingAction) => void;
  onUpdateField: (key: string, value: string) => void;
  onUpdateForm: (patch: Partial<AdapterFormState>) => void;
}) {
  const adapterID = adapter.adapter?.id || adapter.name;
  const displayName = adapter.adapter?.display_name || adapter.name;
  const validationIssues = feedback?.result?.validation?.issues ?? [];
  const globalIssues = validationIssues.filter((issue) => !issue.field);
  // Step 2 only shows credential fields; metadata fields stay on the Manage
  // form for users who need to override defaults.
  const credentialSettings = (adapter.settings ?? []).filter(
    (setting) => setting.target === "credential",
  );
  const imports = importActions(adapter);
  const primary = primaryConnectAction(adapter);
  const hasUserCredentialInput = credentialSettings.some(
    (setting) => (form.values[setting.key] ?? "").trim() !== "",
  );
  const [showManual, setShowManual] = useState(
    imports.length === 0 || hasUserCredentialInput,
  );
  const actionBusyLabel =
    busy && actionState?.actionID === primary?.id ? "Working..." : null;

  if (form.isExisting) {
    const connectedID = feedback?.result?.connect?.connection?.id || form.connectionID;
    return (
      <div className="space-y-6 px-8 py-8">
        <div className="flex items-center gap-4">
          <AdapterIcon adapterID={adapterID} />
          <div>
            <h3 className="text-2xl font-semibold text-on-surface">
              {displayName}
            </h3>
            <p className="text-base text-secondary">{form.label}</p>
          </div>
        </div>
        <div className="flex items-start gap-3 rounded-2xl border border-emerald-500/20 bg-emerald-500/10 p-5">
          <Icon
            className="text-3xl text-emerald-600 dark:text-emerald-400"
            name="check_circle"
          />
          <div className="min-w-0">
            <p className="text-base font-semibold text-on-surface">
              Connected successfully
            </p>
            <p className="mt-1 font-mono text-sm text-secondary">
              {connectedID}
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6 px-8 py-8">
      <div className="flex items-center gap-4">
        <AdapterIcon adapterID={adapterID} />
        <div className="min-w-0">
          <h3 className="text-2xl font-semibold text-on-surface">
            {displayName}
          </h3>
          <p className="truncate text-base text-secondary">
            {adapter.summary ||
              adapter.adapter?.description ||
              "Add connection details and access."}
          </p>
        </div>
      </div>

      <label className="space-y-2">
        <span className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
          Label
        </span>
        <input
          className="w-full rounded-2xl border border-outline-variant/15 bg-surface-container-low px-4 py-3.5 text-base text-on-surface outline-none transition focus:border-primary/40 focus:bg-surface-container-lowest"
          onChange={(event) => onUpdateForm({ label: event.target.value })}
          placeholder={defaultAdapterLabel(adapter)}
          value={form.label}
        />
      </label>

      {imports.length > 0 && (
        <div className="space-y-2">
          <p className="text-xs font-bold uppercase tracking-[0.14em] text-outline">
            Import existing session
          </p>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {imports.map((action) => {
              const importBusy =
                busy && actionState?.actionID === action.id;
              return (
                <button
                  className="flex flex-col items-center gap-2 rounded-2xl border border-outline-variant/15 bg-surface-container-low p-3 text-center text-xs font-semibold text-on-surface transition hover:border-primary/30 hover:bg-surface-container disabled:opacity-60"
                  disabled={busy}
                  key={action.id}
                  onClick={() => onRunAction(action)}
                  title={action.description}
                  type="button"
                >
                  <Icon
                    className={`text-2xl ${importBusy ? "animate-pulse text-primary" : "text-secondary"}`}
                    name={importIcon(action.id)}
                  />
                  <span className="leading-tight">
                    {importBusy ? "Importing…" : importShortLabel(action)}
                  </span>
                </button>
              );
            })}
          </div>
          <p className="text-xs text-secondary">
            Reads your existing logged-in Slack session — no token needed.
          </p>
        </div>
      )}

      {credentialSettings.length > 0 && (
        <div className="rounded-2xl border border-outline-variant/15 bg-surface-container-low">
          <button
            aria-expanded={showManual}
            className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left text-xs font-bold uppercase tracking-[0.14em] text-outline"
            onClick={() => setShowManual((value) => !value)}
            type="button"
          >
            {imports.length > 0
              ? "Or paste credentials manually"
              : "Credentials"}
            <Icon
              className="text-base text-secondary"
              name={showManual ? "expand_less" : "expand_more"}
            />
          </button>
          {showManual && (
            <div className="space-y-4 border-t border-outline-variant/10 px-4 py-4">
              {credentialSettings.map((setting) => (
                <AdapterSettingField
                  issues={issuesFor(validationIssues, setting.key)}
                  key={setting.key}
                  onChange={(value) => onUpdateField(setting.key, value)}
                  setting={setting}
                  value={form.values[setting.key] ?? ""}
                />
              ))}
            </div>
          )}
        </div>
      )}

      {primary && (
        <button
          className="inline-flex w-full items-center justify-center gap-2 rounded-2xl bg-emerald-600 px-5 py-4 text-base font-semibold text-white shadow-sm transition hover:bg-emerald-700 hover:shadow-md disabled:opacity-60"
          disabled={busy}
          onClick={() => onRunAction(primary)}
          type="button"
        >
          <Icon className="text-lg" name={actionIconName(primary.kind)} />
          {actionBusyLabel ??
            primary.label ??
            (primary.kind === "open_url"
              ? `Sign in with ${displayName}`
              : `Connect ${displayName}`)}
        </button>
      )}

      {feedback?.error && (
        <div className="rounded-2xl border border-error/20 bg-error/10 p-3 text-sm text-error">
          {feedback.error}
        </div>
      )}

      {globalIssues.length > 0 && (
        <div className="space-y-2">
          {globalIssues.map((issue, index) => {
            const classes = issueClasses(issue.severity);
            return (
              <div
                className={`rounded-2xl border p-3 text-sm ${classes.border} ${classes.bg}`}
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
    </div>
  );
}

type PolicyRuleEntry = {
  key: keyof PolicyRules;
  label: string;
  description: string;
};

type PolicySection = {
  title: string;
  icon: string;
  rules: PolicyRuleEntry[];
};

const POLICY_SECTIONS: PolicySection[] = [
  {
    title: "Read & receive",
    icon: "inbox",
    rules: [
      {
        key: "read_inbound",
        label: "Read messages",
        description: "Allow agents to read incoming messages.",
      },
      {
        key: "mark_read",
        label: "Mark messages as read",
        description: "Allow agents to update read state.",
      },
    ],
  },
  {
    title: "Respond (in existing threads)",
    icon: "reply",
    rules: [
      {
        key: "create_drafts",
        label: "Draft replies",
        description: "Compose responses for human review before sending.",
      },
      {
        key: "send_messages",
        label: "Send replies",
        description:
          "Actually send messages (combine with require approval to gate).",
      },
      {
        key: "require_approval",
        label: "Require approval before sending",
        description: "Hold every send until a human approves.",
      },
      {
        key: "reply_only",
        label: "Threads only — never start new conversations",
        description:
          "When on, agents can only reply inside threads they were already in.",
      },
      {
        key: "allow_attachments",
        label: "Allow attachments",
        description: "Let agents attach files to outgoing messages.",
      },
    ],
  },
  {
    title: "Initiate (start new conversations)",
    icon: "add_comment",
    rules: [
      {
        key: "allow_new_conversations",
        label: "Start new conversations",
        description:
          "Let agents post a first message in a channel or DM the people in scope.",
      },
    ],
  },
  {
    title: "Search",
    icon: "search",
    rules: [
      {
        key: "search_identities",
        label: "Search people",
        description: "Look up users by name or handle.",
      },
      {
        key: "search_conversations",
        label: "Search conversations",
        description: "Find channels, DMs, and group DMs.",
      },
      {
        key: "search_messages",
        label: "Search messages",
        description: "Full-text search across history within scope.",
      },
    ],
  },
  {
    title: "Manage",
    icon: "settings",
    rules: [
      {
        key: "manage_messages",
        label: "Move, archive, or label messages",
        description:
          "Lets agents reorganize the history. Most teams keep this off.",
      },
    ],
  },
];

function PoliciesEditor({
  value,
  onChange,
}: {
  value: PolicyRules;
  onChange: (next: PolicyRules) => void;
}) {
  return (
    <div className="space-y-7 px-8 py-7">
      {POLICY_SECTIONS.map((section) => (
        <section key={section.title}>
          <h3 className="flex items-center gap-2 text-xs font-bold uppercase tracking-[0.18em] text-outline">
            <Icon className="text-base" name={section.icon} />
            {section.title}
          </h3>
          <ul className="mt-3 space-y-2">
            {section.rules.map((rule) => {
              const checked = value[rule.key];
              return (
                <li key={rule.key}>
                  <label className="flex cursor-pointer items-start gap-3 rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4 transition hover:border-primary/30">
                    <input
                      checked={checked}
                      className="mt-1 h-4 w-4"
                      onChange={(event) =>
                        onChange({
                          ...value,
                          [rule.key]: event.target.checked,
                        })
                      }
                      type="checkbox"
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block text-sm font-semibold text-on-surface">
                        {rule.label}
                      </span>
                      <span className="mt-0.5 block text-xs text-secondary">
                        {rule.description}
                      </span>
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}

function PoliciesModal({
  adapter,
  connectionID,
  connectionLabel,
  initialValue,
  onClose,
  onSave,
}: {
  adapter: MessagingAdapterInfo | null;
  connectionID: string;
  connectionLabel: string;
  initialValue: PolicyRules;
  onClose: () => void;
  onSave: (next: PolicyRules) => void;
}) {
  const [draft, setDraft] = useState(initialValue);
  const adapterID = adapter?.adapter?.id || adapter?.name || "";
  return (
    <div
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="dialog"
    >
      <div
        className="flex max-h-[calc(100vh-2rem)] w-full max-w-3xl flex-col overflow-hidden rounded-[1.5rem] border border-outline-variant/10 bg-surface-container-lowest shadow-xl"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="flex items-start justify-between gap-4 border-b border-outline-variant/10 p-8">
          <div className="flex min-w-0 items-start gap-4">
            {adapter && <AdapterIcon adapterID={adapterID} />}
            <div className="min-w-0">
              <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Policies
              </p>
              <h2 className="mt-1 truncate text-2xl font-semibold text-on-surface">
                {connectionLabel}
              </h2>
              <p className="mt-1 truncate font-mono text-xs text-secondary">
                {connectionID}
              </p>
            </div>
          </div>
          <button
            aria-label="Close"
            className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-secondary transition hover:bg-surface-container-low hover:text-on-surface"
            onClick={onClose}
            type="button"
          >
            <Icon className="text-xl" name="close" />
          </button>
        </header>
        <div className="flex-1 overflow-y-auto">
          <PoliciesEditor onChange={setDraft} value={draft} />
        </div>
        <footer className="flex items-center justify-between gap-3 border-t border-outline-variant/10 bg-surface-container-low px-8 py-4">
          <p className="text-xs text-secondary">
            Stored locally for now — backend wiring is pending.
          </p>
          <div className="flex items-center gap-2">
            <button
              className="inline-flex items-center gap-1 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary hover:text-on-surface"
              onClick={onClose}
              type="button"
            >
              Cancel
            </button>
            <button
              className="inline-flex items-center gap-1 rounded-full bg-emerald-600 px-4 py-2 text-sm font-semibold text-white shadow-sm transition hover:bg-emerald-700 hover:shadow-md"
              onClick={() => onSave(draft)}
              type="button"
            >
              Save policies
            </button>
          </div>
        </footer>
      </div>
    </div>
  );
}
