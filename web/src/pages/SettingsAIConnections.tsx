import { useCallback, useMemo, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { AI_CONNECTION_EVENT_TYPES } from "../lib/events";
import {
  aiConnections,
  secrets,
  wallet,
  x402,
  type AIConnection,
  type AIProviderInfo,
  type SecretSummary,
  type X402ServiceListing,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

const PROVIDER_OPENAI = "openai";
const PROVIDER_ANTHROPIC = "anthropic";
const PROVIDER_VENICE = "venice";
const VENICE_SERVICE_ID = "venice-ai";

const FALLBACK_PROVIDERS: [AIProviderInfo, ...AIProviderInfo[]] = [
  {
    id: PROVIDER_OPENAI,
    display_name: "OpenAI",
    endpoint_style: "openai_chat_completions",
    auth_methods: ["api_key"],
    default_base_url: "https://api.openai.com/v1",
    default_model: "gpt-5.5",
    default_models: ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"],
    default_auth: { method: "api_key", api_key_env: "OPENAI_API_KEY" },
  },
  {
    id: PROVIDER_ANTHROPIC,
    display_name: "Anthropic",
    endpoint_style: "anthropic_messages",
    auth_methods: ["api_key"],
    default_base_url: "https://api.anthropic.com/v1",
    default_model: "claude-opus-4-7",
    default_models: ["claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5"],
    default_auth: {
      method: "api_key",
      api_key_env: "ANTHROPIC_API_KEY",
      api_version: "2023-06-01",
    },
  },
  {
    id: PROVIDER_VENICE,
    display_name: "Venice",
    endpoint_style: "openai_chat_completions",
    auth_methods: ["x402"],
    default_base_url: "https://api.venice.ai/api/v1",
    default_model: "venice-uncensored",
    default_models: ["venice-uncensored"],
    default_auth: {
      method: "x402",
      wallet: "default",
      network: "base",
      service_id: VENICE_SERVICE_ID,
      max_price_usdc: "10.00",
      daily_cap_usdc: "10.00",
    },
  },
];

interface ConnectionFormState {
  providerID: string;
  id: string;
  label: string;
  baseURL: string;
  defaultModel: string;
  models: string;
  apiKeyEnv: string;
  secretRef: string;
  apiVersion: string;
  walletName: string;
  network: string;
  serviceID: string;
  maxPriceUSDC: string;
  dailyCapUSDC: string;
}

function providersOrFallback(
  providers: AIProviderInfo[] | undefined,
): AIProviderInfo[] {
  return providers && providers.length > 0 ? providers : FALLBACK_PROVIDERS;
}

function providerByID(providers: AIProviderInfo[], providerID: string) {
  return providers.find((provider) => provider.id === providerID);
}

function defaultProvider(providers: AIProviderInfo[]): AIProviderInfo {
  return (
    providerByID(providers, PROVIDER_OPENAI) ??
    providerByID(providers, PROVIDER_ANTHROPIC) ??
    providers[0] ??
    FALLBACK_PROVIDERS[0]
  );
}

function defaultConnectionForm(
  id: string,
  provider: AIProviderInfo,
): ConnectionFormState {
  return {
    providerID: provider.id,
    id,
    label: provider.display_name,
    baseURL: provider.default_base_url,
    defaultModel: "",
    models: "",
    apiKeyEnv: provider.default_auth.api_key_env ?? "",
    secretRef: provider.default_auth.secret_ref ?? "",
    apiVersion: provider.default_auth.api_version ?? "",
    walletName: provider.default_auth.wallet ?? "default",
    network: provider.default_auth.network ?? "base",
    serviceID: provider.default_auth.service_id ?? VENICE_SERVICE_ID,
    maxPriceUSDC: provider.default_auth.max_price_usdc ?? "10.00",
    dailyCapUSDC: provider.default_auth.daily_cap_usdc ?? "10.00",
  };
}

function formFromConnection(connection: AIConnection): ConnectionFormState {
  return {
    providerID: connection.provider,
    id: connection.id,
    label: connection.label,
    baseURL: connection.base_url,
    defaultModel: connection.default_model ?? "",
    models: (connection.models ?? []).join(", "),
    apiKeyEnv: connection.auth.api_key_env ?? "",
    secretRef: connection.auth.secret_ref ?? "",
    apiVersion: connection.auth.api_version ?? "",
    walletName: connection.auth.wallet ?? "default",
    network: connection.auth.network ?? "base",
    serviceID: connection.auth.service_id ?? VENICE_SERVICE_ID,
    maxPriceUSDC: connection.auth.max_price_usdc ?? "10.00",
    dailyCapUSDC: connection.auth.daily_cap_usdc ?? "10.00",
  };
}

function connectionFromForm(form: ConnectionFormState): AIConnection {
  const isVenice = form.providerID === PROVIDER_VENICE;
  const secretRef = form.secretRef.trim();
  return {
    id: form.id.trim(),
    label: form.label.trim(),
    provider: form.providerID,
    base_url: form.baseURL.trim(),
    default_model: form.defaultModel.trim(),
    models: [],
    auth: isVenice
      ? {
          method: "x402",
          wallet: form.walletName.trim(),
          network: form.network.trim(),
          service_id: form.serviceID.trim(),
          max_price_usdc: form.maxPriceUSDC.trim(),
          daily_cap_usdc: form.dailyCapUSDC.trim(),
        }
      : {
          method: "api_key",
          api_key_env: secretRef ? "" : form.apiKeyEnv.trim(),
          secret_ref: secretRef,
          api_version: form.apiVersion.trim(),
        },
  };
}

function nextConnectionID(connections: AIConnection[], providerID: string) {
  const used = new Set(connections.map((connection) => connection.id));
  if (!used.has(providerID)) return providerID;
  for (let i = 2; i < 1000; i += 1) {
    const candidate = `${providerID}-${i}`;
    if (!used.has(candidate)) return candidate;
  }
  return `${providerID}-${Date.now()}`;
}

function providerLabel(providers: AIProviderInfo[], providerID: string) {
  return providerByID(providers, providerID)?.display_name ?? providerID;
}

function networkLabel(value: string | undefined) {
  if (value === "base" || value === "eip155:8453") return "Base";
  if (value === "solana") return "Solana";
  return value || "-";
}

function formatTimestamp(value: string | undefined) {
  if (!value) return "-";
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return value;
  return dt.toLocaleString();
}

function authSummary(connection: AIConnection) {
  if (connection.auth.method === "x402") {
    return connection.auth.service_id || "x402";
  }
  if (connection.auth.secret_ref) return `Secret: ${connection.auth.secret_ref}`;
  return connection.auth.api_key_env || "Host environment";
}

function WalletBanner() {
  const { data: status } = useRPC(() => wallet.status(), [], {
    refreshIntervalMs: 30_000,
  });
  if (!status) return null;
  if (!status.installed) {
    return (
      <div className="flex items-start gap-3 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-800 dark:text-amber-200">
        <Icon className="text-base" name="warning" />
        <div className="min-w-0 flex-1">
          <p className="font-medium">OWS is not installed.</p>
          <p>
            Venice x402 calls need the wallet signer. Install it from{" "}
            <Link className="underline" to="/settings/wallet">
              Settings / Wallet
            </Link>
            .
          </p>
        </div>
      </div>
    );
  }
  if (status.wallets === 0) {
    return (
      <div className="flex items-start gap-3 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-800 dark:text-amber-200">
        <Icon className="text-base" name="account_balance_wallet" />
        <div className="min-w-0 flex-1">
          <p className="font-medium">No local wallet exists yet.</p>
          <p>
            Create and fund a wallet from{" "}
            <Link className="underline" to="/settings/wallet">
              Settings / Wallet
            </Link>{" "}
            before using paid Venice x402 calls.
          </p>
        </div>
      </div>
    );
  }
  return null;
}

function FieldLabel({
  children,
  htmlFor,
}: {
  children: React.ReactNode;
  htmlFor: string;
}) {
  return (
    <label
      className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline"
      htmlFor={htmlFor}
    >
      {children}
    </label>
  );
}

function ConnectionCard({
  connection,
  onDelete,
  onEdit,
  providers,
  service,
}: {
  connection: AIConnection;
  onDelete: (connection: AIConnection) => void;
  onEdit: (connection: AIConnection) => void;
  providers: AIProviderInfo[];
  service?: X402ServiceListing;
}) {
  const isVenice = connection.provider === PROVIDER_VENICE;
  const approved =
    isVenice &&
    service?.enabled &&
    service.id === (connection.auth.service_id ?? "");
  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1 space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-lg font-semibold text-on-surface">
              {connection.label || connection.id}
            </h3>
            <span className="rounded-full bg-sky-500/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-sky-700 dark:text-sky-200">
              {providerLabel(providers, connection.provider)}
            </span>
            {approved ? (
              <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-primary">
                Service approved
              </span>
            ) : null}
          </div>
          <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div className="min-w-0">
              <dt className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Auth
              </dt>
              <dd className="truncate text-secondary">
                {authSummary(connection)}
              </dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Limit
              </dt>
              <dd className="truncate text-secondary">
                {isVenice
                  ? `$${connection.auth.max_price_usdc || "-"}/call`
                  : connection.auth.api_version || "-"}
              </dd>
            </div>
            <div className="min-w-0 sm:col-span-2">
              <dt className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Base URL
              </dt>
              <dd className="truncate font-mono text-xs text-secondary">
                {connection.base_url}
              </dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Network
              </dt>
              <dd className="truncate text-secondary">
                {isVenice ? networkLabel(connection.auth.network) : "-"}
              </dd>
            </div>
            <div className="min-w-0">
              <dt className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                Updated
              </dt>
              <dd className="truncate text-secondary">
                {formatTimestamp(connection.updated_at)}
              </dd>
            </div>
          </dl>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-3 py-1.5 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
            onClick={() => onEdit(connection)}
            type="button"
          >
            <Icon className="text-base" name="edit" />
            Edit
          </button>
          <button
            className="inline-flex items-center gap-2 rounded-full bg-red-500/10 px-3 py-1.5 text-sm font-semibold text-red-700 transition-colors hover:bg-red-500/20 dark:text-red-200"
            onClick={() => onDelete(connection)}
            type="button"
          >
            <Icon className="text-base" name="delete" />
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}

interface ConnectionDialogProps {
  approveOnSave: boolean;
  editingConnection?: AIConnection;
  error: string | null;
  form: ConnectionFormState;
  onApproveOnSaveChange: (value: boolean) => void;
  onClose: () => void;
  onProviderChange: (providerID: string) => void;
  onSubmit: (event: React.FormEvent) => void;
  onUpdateForm: <K extends keyof ConnectionFormState>(
    key: K,
    value: ConnectionFormState[K],
  ) => void;
  open: boolean;
  providers: AIProviderInfo[];
  saving: boolean;
  secretOptions: SecretSummary[];
  service?: X402ServiceListing;
  serviceError: string | null;
}

function ConnectionDialog({
  approveOnSave,
  editingConnection,
  error,
  form,
  onApproveOnSaveChange,
  onClose,
  onProviderChange,
  onSubmit,
  onUpdateForm,
  open,
  providers,
  saving,
  secretOptions,
  service,
  serviceError,
}: ConnectionDialogProps) {
  if (!open) return null;

  const selectedProvider = providerByID(providers, form.providerID);
  const isVenice = form.providerID === PROVIDER_VENICE;
  const isAnthropic = form.providerID === PROVIDER_ANTHROPIC;
  const serviceApproved = isVenice && service?.enabled && service.id === form.serviceID;
  const selectedSecretKnown =
    form.secretRef === "" ||
    secretOptions.some((secret) => secret.name === form.secretRef);

  return (
    <div
      aria-labelledby="ai-connection-dialog-title"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4 py-6"
      role="dialog"
    >
      <form
        className="max-h-[calc(100vh-3rem)] w-full max-w-4xl overflow-y-auto rounded-2xl border border-outline-variant/20 bg-surface p-6 shadow-2xl"
        onSubmit={onSubmit}
      >
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="space-y-2">
            <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
              {selectedProvider?.display_name ?? "AI provider"}
            </p>
            <h2
              className="text-xl font-semibold text-on-surface"
              id="ai-connection-dialog-title"
            >
              {editingConnection ? "Edit connection" : "Add connection"}
            </h2>
            <p className="max-w-2xl text-sm text-secondary">
              Configure endpoint and credentials for this connection.
            </p>
          </div>
          <button
            aria-label="Close"
            className="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-full border border-outline-variant/20 text-secondary transition-colors hover:text-on-surface"
            onClick={onClose}
            type="button"
          >
            <Icon className="text-base" name="close" />
          </button>
        </div>

        <div className="mt-6 grid gap-4 md:grid-cols-2">
          <div className="space-y-2">
            <FieldLabel htmlFor="ai-connection-provider">Provider</FieldLabel>
            <select
              className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm disabled:cursor-not-allowed disabled:opacity-60"
              disabled={Boolean(editingConnection)}
              id="ai-connection-provider"
              onChange={(event) => onProviderChange(event.target.value)}
              value={form.providerID}
            >
              {providers.map((provider) => (
                <option key={provider.id} value={provider.id}>
                  {provider.display_name}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <FieldLabel htmlFor="ai-connection-label">Name</FieldLabel>
            <input
              className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm"
              id="ai-connection-label"
              onChange={(event) => onUpdateForm("label", event.target.value)}
              value={form.label}
            />
          </div>
          <div className="space-y-2 md:col-span-2">
            <FieldLabel htmlFor="ai-connection-base-url">Base URL</FieldLabel>
            <input
              className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 font-mono text-sm"
              id="ai-connection-base-url"
              onChange={(event) => onUpdateForm("baseURL", event.target.value)}
              value={form.baseURL}
            />
          </div>

          {!isVenice ? (
            <>
              <div className="space-y-2 md:col-span-2">
                <FieldLabel htmlFor="ai-connection-secret-ref">
                  Credential
                </FieldLabel>
                <select
                  className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 font-mono text-sm"
                  id="ai-connection-secret-ref"
                  onChange={(event) =>
                    onUpdateForm("secretRef", event.target.value)
                  }
                  value={form.secretRef}
                >
                  <option value="">Host environment variable</option>
                  {selectedSecretKnown ? null : (
                    <option value={form.secretRef}>{form.secretRef}</option>
                  )}
                  {secretOptions.map((secret) => (
                    <option key={secret.id} value={secret.name}>
                      {secret.name}
                    </option>
                  ))}
                </select>
              </div>
              {form.secretRef ? null : (
                <div className="space-y-2 md:col-span-2">
                  <FieldLabel htmlFor="ai-connection-api-env">
                    Environment variable
                  </FieldLabel>
                  <input
                    className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 font-mono text-sm"
                    id="ai-connection-api-env"
                    onChange={(event) =>
                      onUpdateForm("apiKeyEnv", event.target.value)
                    }
                    value={form.apiKeyEnv}
                  />
                </div>
              )}
              {isAnthropic ? (
                <div className="space-y-2 md:col-span-2">
                  <FieldLabel htmlFor="ai-connection-api-version">
                    API version
                  </FieldLabel>
                  <input
                    className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 font-mono text-sm"
                    id="ai-connection-api-version"
                    onChange={(event) =>
                      onUpdateForm("apiVersion", event.target.value)
                    }
                    value={form.apiVersion}
                  />
                </div>
              ) : null}
            </>
          ) : (
            <>
              <div className="space-y-2">
                <FieldLabel htmlFor="ai-connection-wallet">Wallet</FieldLabel>
                <input
                  className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm"
                  id="ai-connection-wallet"
                  onChange={(event) =>
                    onUpdateForm("walletName", event.target.value)
                  }
                  value={form.walletName}
                />
              </div>
              <div className="space-y-2">
                <FieldLabel htmlFor="ai-connection-network">Network</FieldLabel>
                <select
                  className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm"
                  id="ai-connection-network"
                  onChange={(event) =>
                    onUpdateForm("network", event.target.value)
                  }
                  value={form.network}
                >
                  <option value="base">Base</option>
                </select>
              </div>
              <div className="space-y-2">
                <FieldLabel htmlFor="ai-connection-service">
                  x402 service
                </FieldLabel>
                <input
                  className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 font-mono text-sm"
                  id="ai-connection-service"
                  onChange={(event) =>
                    onUpdateForm("serviceID", event.target.value)
                  }
                  value={form.serviceID}
                />
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <FieldLabel htmlFor="ai-connection-max-price">
                    Max / call
                  </FieldLabel>
                  <input
                    className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm"
                    id="ai-connection-max-price"
                    inputMode="decimal"
                    onChange={(event) =>
                      onUpdateForm("maxPriceUSDC", event.target.value)
                    }
                    value={form.maxPriceUSDC}
                  />
                </div>
                <div className="space-y-2">
                  <FieldLabel htmlFor="ai-connection-daily-cap">
                    Daily cap
                  </FieldLabel>
                  <input
                    className="w-full rounded-lg border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm"
                    id="ai-connection-daily-cap"
                    inputMode="decimal"
                    onChange={(event) =>
                      onUpdateForm("dailyCapUSDC", event.target.value)
                    }
                    value={form.dailyCapUSDC}
                  />
                </div>
              </div>
            </>
          )}
        </div>

        {isVenice ? (
          <div className="mt-5 rounded-xl border border-outline-variant/10 bg-surface-container-lowest p-4">
            <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0 space-y-1">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  x402 service
                </p>
                <p className="text-sm font-medium text-on-surface">
                  {service?.display_name ?? "Venice AI"}
                </p>
                <p className="text-sm text-secondary">
                  {serviceApproved
                    ? `Approved up to $${service?.approved_max_price_usdc ?? form.maxPriceUSDC}/call.`
                    : "Not approved for agent calls yet."}
                </p>
                {serviceError ? (
                  <p className="text-xs text-red-600 dark:text-red-300">
                    {serviceError}
                  </p>
                ) : null}
              </div>
              <Link
                className="inline-flex items-center gap-2 text-sm font-semibold text-primary transition-colors hover:text-on-surface"
                to="/settings/services"
              >
                Open Services
                <Icon className="text-base" name="arrow_forward" />
              </Link>
            </div>
          </div>
        ) : null}

        <div className="mt-5 flex flex-col gap-3 border-t border-outline-variant/10 pt-5 sm:flex-row sm:items-center sm:justify-between">
          {isVenice ? (
            <label className="inline-flex items-center gap-3 text-sm text-secondary">
              <input
                checked={approveOnSave}
                className="h-4 w-4 rounded border-outline-variant/40 text-primary"
                onChange={(event) => onApproveOnSaveChange(event.target.checked)}
                type="checkbox"
              />
              Approve the Venice x402 service when saving
            </label>
          ) : (
            <span className="text-sm text-secondary">
              API keys are resolved by the host daemon at call time.
            </span>
          )}
          <div className="flex flex-wrap items-center justify-end gap-3">
            <button
              className="inline-flex items-center justify-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
              onClick={onClose}
              type="button"
            >
              Cancel
            </button>
            <button
              className="inline-flex items-center justify-center gap-2 rounded-full bg-primary px-5 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-60"
              disabled={saving}
              type="submit"
            >
              <Icon
                className={`text-base ${saving ? "animate-spin" : ""}`}
                name={saving ? "progress_activity" : "save"}
              />
              Save
            </button>
          </div>
        </div>
        {error ? (
          <p className="mt-4 rounded-xl border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-200">
            {error}
          </p>
        ) : null}
      </form>
    </div>
  );
}

export default function SettingsAIConnections() {
  const { data: providerData } = useRPC(() => aiConnections.providers(), []);
  const providers = providersOrFallback(providerData?.providers);
  const {
    data: connectionData,
    error: connectionError,
    loading,
    refetch,
  } = useRPC(() => aiConnections.connections(), [], {
    live: AI_CONNECTION_EVENT_TYPES,
    refreshIntervalMs: 15_000,
  });
  const {
    data: serviceData,
    error: serviceError,
    refetch: refetchServices,
  } = useRPC(() => x402.listServices(), [], {
    refreshIntervalMs: 30_000,
  });
  const { data: secretData } = useRPC(() => secrets.list(), [], {
    refreshIntervalMs: 30_000,
  });

  const connections = useMemo(
    () => connectionData?.connections ?? [],
    [connectionData?.connections],
  );
  const secretOptions = useMemo(() => secretData?.items ?? [], [secretData?.items]);
  const veniceService = serviceData?.services.find(
    (service) => service.id === VENICE_SERVICE_ID,
  );

  const [form, setForm] = useState<ConnectionFormState>(() => {
    const provider = defaultProvider(FALLBACK_PROVIDERS);
    return defaultConnectionForm(provider.id, provider);
  });
  const [editingID, setEditingID] = useState<string | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [approveOnSave, setApproveOnSave] = useState(true);
  const [saving, setSaving] = useState(false);
  const [feedback, setFeedback] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const updateForm = useCallback(
    <K extends keyof ConnectionFormState>(
      key: K,
      value: ConnectionFormState[K],
    ) => {
      setForm((current) => ({ ...current, [key]: value }));
      setFeedback(null);
      setError(null);
    },
    [],
  );

  const openNewDialog = useCallback(
    (providerID?: string) => {
      const provider =
        (providerID ? providerByID(providers, providerID) : undefined) ??
        defaultProvider(providers);
      setForm(
        defaultConnectionForm(
          nextConnectionID(connections, provider.id),
          provider,
        ),
      );
      setEditingID(null);
      setApproveOnSave(provider.id === PROVIDER_VENICE);
      setFeedback(null);
      setError(null);
      setModalOpen(true);
    },
    [connections, providers],
  );

  const handleEdit = useCallback((connection: AIConnection) => {
    setForm(formFromConnection(connection));
    setEditingID(connection.id);
    setApproveOnSave(connection.auth.method === "x402");
    setFeedback(null);
    setError(null);
    setModalOpen(true);
  }, []);

  const saveServiceApproval = useCallback(
    async (connection: AIConnection) => {
      if (connection.provider !== PROVIDER_VENICE) return;
      const serviceID = connection.auth.service_id?.trim();
      if (!serviceID) return;
      await x402.setEnabled({
        service_id: serviceID,
        enabled: true,
        max_price_usdc: connection.auth.max_price_usdc || undefined,
      });
      await refetchServices();
    },
    [refetchServices],
  );

  const handleSubmit = useCallback(
    async (event: React.FormEvent) => {
      event.preventDefault();
      setSaving(true);
      setFeedback(null);
      setError(null);
      try {
        const connection = connectionFromForm(form);
        const saved = await aiConnections.save({ connection });
        if (approveOnSave && saved.connection.provider === PROVIDER_VENICE) {
          await saveServiceApproval(saved.connection);
        }
        setForm(formFromConnection(saved.connection));
        setEditingID(saved.connection.id);
        await refetch();
        setFeedback("Connection saved.");
        setModalOpen(false);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Save failed");
      } finally {
        setSaving(false);
      }
    },
    [approveOnSave, form, refetch, saveServiceApproval],
  );

  const handleDelete = useCallback(
    async (connection: AIConnection) => {
      if (!window.confirm(`Delete ${connection.label || connection.id}?`)) {
        return;
      }
      setError(null);
      setFeedback(null);
      try {
        await aiConnections.delete({ id: connection.id });
        await refetch();
        if (editingID === connection.id) {
          const provider = defaultProvider(providers);
          setForm(defaultConnectionForm(provider.id, provider));
          setEditingID(null);
          setModalOpen(false);
        }
        setFeedback("Connection deleted.");
      } catch (err) {
        setError(err instanceof Error ? err.message : "Delete failed");
      }
    },
    [editingID, providers, refetch],
  );

  const handleProviderChange = useCallback(
    (providerID: string) => {
      const provider = providerByID(providers, providerID);
      if (!provider) return;
      setForm(
        defaultConnectionForm(
          nextConnectionID(connections, provider.id),
          provider,
        ),
      );
      setApproveOnSave(provider.id === PROVIDER_VENICE);
      setFeedback(null);
      setError(null);
    },
    [connections, providers],
  );

  const existingConnection = editingID
    ? connections.find((connection) => connection.id === editingID)
    : undefined;
  const needsWalletBanner =
    form.providerID === PROVIDER_VENICE ||
    connections.some((connection) => connection.provider === PROVIDER_VENICE);

  return (
    <SettingsPage
      actions={
        <button
          className="inline-flex items-center justify-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary transition-colors hover:bg-primary/90"
          onClick={() => openNewDialog()}
          type="button"
        >
          <Icon className="text-base" name="add" />
          Add
        </button>
      }
      description="Create named AI endpoint connections for root agent and sandbox routing."
      pinnablePageID="ai-connections"
      title="AI Connections"
      width="wide"
    >
      {needsWalletBanner ? <WalletBanner /> : null}

      {feedback ? (
        <div className="rounded-2xl border border-emerald-500/30 bg-emerald-500/10 p-4 text-sm text-emerald-700 dark:text-emerald-200">
          {feedback}
        </div>
      ) : null}

      {connectionError ? (
        <div className="rounded-2xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-200">
          {connectionError}
        </div>
      ) : null}

      <section className="space-y-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-lg font-semibold text-on-surface">
              Configured connections
            </h2>
            <p className="text-sm text-secondary">
              {connectionData?.count ?? 0} total AI connection
              {(connectionData?.count ?? 0) === 1 ? "" : "s"}.
            </p>
          </div>
          <button
            className="inline-flex items-center justify-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
            onClick={() => openNewDialog()}
            type="button"
          >
            <Icon className="text-base" name="add" />
            Add
          </button>
        </div>

        {loading && connections.length === 0 ? (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 text-sm text-secondary">
            Loading connections...
          </div>
        ) : null}

        {!loading && connections.length === 0 ? (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 text-sm text-secondary">
            No AI connections are configured yet.
            <button
              className="ml-2 font-semibold text-primary hover:text-on-surface"
              onClick={() => openNewDialog()}
              type="button"
            >
              Add one.
            </button>
          </div>
        ) : null}

        <div className="grid gap-3">
          {connections.map((connection) => (
            <ConnectionCard
              connection={connection}
              key={connection.id}
              onDelete={handleDelete}
              onEdit={handleEdit}
              providers={providers}
              service={veniceService}
            />
          ))}
        </div>
      </section>

      <ConnectionDialog
        approveOnSave={approveOnSave}
        editingConnection={existingConnection}
        error={error}
        form={form}
        onApproveOnSaveChange={setApproveOnSave}
        onClose={() => {
          if (saving) return;
          setModalOpen(false);
          setError(null);
        }}
        onProviderChange={handleProviderChange}
        onSubmit={handleSubmit}
        onUpdateForm={updateForm}
        open={modalOpen}
        providers={providers}
        saving={saving}
        secretOptions={secretOptions}
        service={veniceService}
        serviceError={serviceError}
      />
    </SettingsPage>
  );
}
