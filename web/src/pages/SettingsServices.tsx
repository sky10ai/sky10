import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import {
  wallet,
  x402,
  type X402Receipt,
  type X402ServiceEndpoint,
  type X402ServiceListing,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

function networkLabel(networks: X402ServiceListing["networks"]): string {
  if (!networks || networks.length === 0) return "—";
  return networks.map((n) => n.charAt(0).toUpperCase() + n.slice(1)).join(", ");
}

function tierBadgeClasses(tier: X402ServiceListing["tier"]): string {
  if (tier === "primitive") {
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-200";
  }
  return "bg-secondary-container/30 text-secondary";
}

function tierLabel(tier: X402ServiceListing["tier"]): string {
  if (tier === "primitive") return "Hard to do locally";
  return "Optional paid API";
}

function tierTooltip(tier: X402ServiceListing["tier"]): string {
  if (tier === "primitive") {
    return "Use when the agent needs something local tools usually cannot provide.";
  }
  return "Use when a paid API is worth the cost for this workflow.";
}

function categoryLabel(category: string | undefined): string {
  if (!category) return "—";
  return category
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function serviceIconName(service: X402ServiceListing): string {
  const id = service.id.toLowerCase();
  const name = service.display_name.toLowerCase();
  const category = service.category?.toLowerCase() ?? "";
  const text =
    `${id} ${name} ${category} ${service.description ?? ""}`.toLowerCase();

  if (
    text.includes("deepgram") ||
    text.includes("speech") ||
    text.includes("audio")
  ) {
    return "graphic_eq";
  }
  if (
    text.includes("fal") ||
    text.includes("image") ||
    text.includes("video")
  ) {
    return "auto_awesome";
  }
  if (
    text.includes("e2b") ||
    text.includes("code") ||
    text.includes("sandbox")
  ) {
    return "terminal";
  }
  if (text.includes("browserbase") || text.includes("browser")) {
    return "public";
  }
  if (
    text.includes("openai") ||
    text.includes("anthropic") ||
    text.includes("venice") ||
    text.includes("llm")
  ) {
    return "smart_toy";
  }
  if (
    text.includes("perplexity") ||
    text.includes("exa") ||
    text.includes("search")
  ) {
    return "travel_explore";
  }
  if (
    text.includes("messari") ||
    text.includes("coingecko") ||
    text.includes("market")
  ) {
    return "query_stats";
  }
  if (
    text.includes("alchemy") ||
    text.includes("quicknode") ||
    text.includes("rpc")
  ) {
    return "hub";
  }
  if (text.includes("tripadvisor") || text.includes("travel")) {
    return "map";
  }
  if (text.includes("apollo") || text.includes("contact")) {
    return "contacts";
  }
  if (category.includes("infrastructure")) return "dns";
  if (category.includes("media")) return "perm_media";
  if (category.includes("data")) return "database";
  return "apps";
}

function serviceIconClasses(service: X402ServiceListing): string {
  const category = service.category?.toLowerCase() ?? "";
  if (category.includes("media")) {
    return "bg-sky-500/10 text-sky-700 dark:text-sky-200";
  }
  if (category.includes("infrastructure")) {
    return "bg-violet-500/10 text-violet-700 dark:text-violet-200";
  }
  if (category.includes("search") || category.includes("data")) {
    return "bg-amber-500/10 text-amber-700 dark:text-amber-200";
  }
  if (service.tier === "primitive") {
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-200";
  }
  return "bg-secondary-container/30 text-secondary";
}

function formatTimestamp(value: string | undefined): string {
  if (!value) return "—";
  const dt = new Date(value);
  if (Number.isNaN(dt.getTime())) return value;
  return dt.toLocaleString();
}

function truncate(value: string | undefined, n = 10): string {
  if (!value) return "";
  if (value.length <= n * 2 + 3) return value;
  return `${value.slice(0, n)}…${value.slice(-n)}`;
}

function serviceEndpoints(service: X402ServiceListing): X402ServiceEndpoint[] {
  const seen = new Set<string>();
  const endpoints = (service.endpoints ?? []).filter((ep) => {
    if (!ep.url) return false;
    const key = endpointKey(ep);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  if (endpoints.length > 0) return endpoints;
  if (!service.endpoint) return [];
  return [
    {
      url: service.endpoint,
      price_usdc: service.max_price_usdc,
      network: service.networks?.[0],
    },
  ];
}

function endpointKey(endpoint: X402ServiceEndpoint): string {
  return JSON.stringify([
    endpoint.method ?? "",
    endpoint.url,
    endpoint.description ?? "",
    endpoint.price_usdc ?? "",
    endpoint.network ?? "",
  ]);
}

function serviceLink(service: X402ServiceListing): string {
  return service.service_url || service.endpoint || service.endpoints?.[0]?.url || "";
}

function endpointPathLabel(endpointURL: string): string {
  try {
    const parsed = new URL(endpointURL);
    const path = `${parsed.pathname}${parsed.search}`;
    if (path && path !== "/") return path;
    return parsed.host;
  } catch {
    return endpointURL;
  }
}

function endpointHostLabel(endpointURL: string): string {
  try {
    return new URL(endpointURL).host;
  } catch {
    return endpointURL;
  }
}

function endpointDisplayLabel(endpoint: X402ServiceEndpoint): string {
  return endpoint.description || endpointPathLabel(endpoint.url);
}

function endpointMetaParts(endpoint: X402ServiceEndpoint): string[] {
  return [
    endpoint.description ? endpointPathLabel(endpoint.url) : undefined,
    endpoint.network ? networkLabel([endpoint.network]) : undefined,
    endpoint.price_usdc ? `$${endpoint.price_usdc}` : undefined,
  ]
    .filter(Boolean)
    .map(String);
}

interface EndpointDropdownProps {
  endpoints: X402ServiceEndpoint[];
  selectedEndpoint: X402ServiceEndpoint | undefined;
  onSelect: (key: string) => void;
}

function EndpointDropdown({
  endpoints,
  selectedEndpoint,
  onSelect,
}: EndpointDropdownProps) {
  const [open, setOpen] = useState(false);
  if (!selectedEndpoint) return null;
  const metaParts = endpointMetaParts(selectedEndpoint);
  const selectedEndpointKey = endpointKey(selectedEndpoint);

  return (
    <div
      className="relative w-full max-w-2xl space-y-2"
      onBlur={(event) => {
        const next = event.relatedTarget;
        if (!(next instanceof Node) || !event.currentTarget.contains(next)) {
          setOpen(false);
        }
      }}
    >
      <div className="mb-1 flex items-center gap-2">
        <span className="text-xs font-medium uppercase tracking-wider text-outline">
          Endpoints
        </span>
        <span className="rounded-full bg-surface-container px-2 py-0.5 text-[10px] font-medium text-secondary">
          {endpoints.length}
        </span>
      </div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="flex w-full min-w-0 items-center gap-3 rounded-lg border border-outline-variant/30 bg-surface px-3 py-2 text-left transition-colors hover:bg-surface-container-low focus:outline-none focus:ring-2 focus:ring-emerald-500/30"
      >
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-surface-container text-secondary">
          <Icon className="text-base" name="route" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-2">
            {selectedEndpoint.method ? (
              <span className="shrink-0 rounded bg-emerald-500/10 px-1.5 py-0.5 font-mono text-[10px] font-bold text-emerald-700 dark:text-emerald-200">
                {selectedEndpoint.method}
              </span>
            ) : null}
            <span className="truncate text-sm font-medium text-on-surface">
              {endpointDisplayLabel(selectedEndpoint)}
            </span>
          </span>
          <span className="mt-0.5 block truncate text-xs text-secondary">
            {[endpointHostLabel(selectedEndpoint.url), ...metaParts].join(
              " · ",
            )}
          </span>
        </span>
        <Icon
          className={`shrink-0 text-base text-outline transition-transform ${
            open ? "rotate-180" : ""
          }`}
          name="expand_more"
        />
      </button>

      {open ? (
        <div className="absolute left-0 top-full z-30 mt-2 max-h-72 w-full overflow-y-auto overflow-x-hidden overscroll-contain rounded-lg border border-outline-variant/20 bg-surface-container-lowest p-1 shadow-xl">
          {endpoints.map((endpoint) => {
            const key = endpointKey(endpoint);
            const selected = key === selectedEndpointKey;
            const parts = endpointMetaParts(endpoint);
            return (
              <button
                key={key}
                type="button"
                onClick={() => {
                  onSelect(key);
                  setOpen(false);
                }}
                className={`flex w-full min-w-0 items-start gap-3 overflow-hidden rounded-md px-3 py-2 text-left transition-colors ${
                  selected
                    ? "bg-emerald-500/10 text-on-surface"
                    : "text-secondary hover:bg-surface-container hover:text-on-surface"
                }`}
              >
                <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded bg-surface">
                  <Icon
                    className="text-sm"
                    name={selected ? "check" : "link"}
                  />
                </span>
                <span className="min-w-0 flex-1 space-y-1">
                  <span className="flex min-w-0 items-center gap-2">
                    {endpoint.method ? (
                      <span className="shrink-0 rounded bg-surface px-1.5 py-0.5 font-mono text-[10px] font-bold">
                        {endpoint.method}
                      </span>
                    ) : null}
                    <span className="truncate text-sm font-medium">
                      {endpointDisplayLabel(endpoint)}
                    </span>
                  </span>
                  <span className="block truncate font-mono text-[11px]">
                    {endpoint.url}
                  </span>
                  {endpoint.description || parts.length > 0 ? (
                    <span className="block truncate text-xs">
                      {[endpoint.description, ...parts]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  ) : null}
                </span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

interface ServiceCardProps {
  service: X402ServiceListing;
  busy: boolean;
  error: string | null;
  onApprove: (service: X402ServiceListing, maxPriceUSDC: string) => void;
  onRevoke: (service: X402ServiceListing) => void;
}

function ServiceCard({
  service,
  busy,
  error,
  onApprove,
  onRevoke,
}: ServiceCardProps) {
  const [editing, setEditing] = useState(false);
  const [maxPrice, setMaxPrice] = useState(service.max_price_usdc ?? "");
  const endpoints = useMemo(() => serviceEndpoints(service), [service]);
  const [selectedEndpointKey, setSelectedEndpointKey] = useState(
    endpoints[0] ? endpointKey(endpoints[0]) : "",
  );
  const selectedEndpoint =
    endpoints.find((ep) => endpointKey(ep) === selectedEndpointKey) ??
    endpoints[0];
  const currentServiceLink = serviceLink(service);

  useEffect(() => {
    const firstEndpointKey = endpoints[0] ? endpointKey(endpoints[0]) : "";
    setSelectedEndpointKey((prev) =>
      endpoints.some((ep) => endpointKey(ep) === prev)
        ? prev
        : firstEndpointKey,
    );
  }, [endpoints]);

  const startApprove = useCallback(() => {
    setEditing(true);
    setMaxPrice(service.max_price_usdc ?? "");
  }, [service.max_price_usdc]);
  const cancelApprove = useCallback(() => setEditing(false), []);
  const submitApprove = useCallback(
    (event: React.FormEvent) => {
      event.preventDefault();
      onApprove(service, maxPrice.trim());
      setEditing(false);
    },
    [maxPrice, onApprove, service],
  );

  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex min-w-0 flex-1 items-start gap-3">
            <div
              className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-xl ${serviceIconClasses(service)}`}
              aria-hidden="true"
            >
              <Icon className="text-2xl" name={serviceIconName(service)} />
            </div>
            <div className="min-w-0 flex-1 space-y-2">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="text-base font-semibold text-on-surface">
                  {currentServiceLink ? (
                    <a
                      href={currentServiceLink}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex min-w-0 items-center gap-1 transition-colors hover:text-primary"
                    >
                      <span className="truncate">
                        {service.display_name || service.id}
                      </span>
                      <Icon
                        className="shrink-0 text-sm text-outline"
                        name="open_in_new"
                      />
                    </a>
                  ) : (
                    service.display_name || service.id
                  )}
                </h3>
                {service.tier === "primitive" ? (
                  <span
                    title={tierTooltip(service.tier)}
                    className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${tierBadgeClasses(service.tier)}`}
                  >
                    {tierLabel(service.tier)}
                  </span>
                ) : null}
                {service.enabled ? (
                  <span className="rounded-full bg-emerald-500/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-emerald-700 dark:text-emerald-200">
                    Approved
                  </span>
                ) : null}
              </div>
              {service.description ? (
                <p className="text-sm text-secondary">{service.description}</p>
              ) : null}
              {service.hint ? (
                <p className="rounded-md bg-surface-container/40 px-2 py-1 text-xs italic text-secondary">
                  {service.hint}
                </p>
              ) : null}
              <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-secondary sm:grid-cols-3">
                <div>
                  <dt className="font-medium uppercase tracking-wider text-outline">
                    Category
                  </dt>
                  <dd>{categoryLabel(service.category)}</dd>
                </div>
                <div>
                  <dt className="font-medium uppercase tracking-wider text-outline">
                    Chain
                  </dt>
                  <dd>{networkLabel(service.networks)}</dd>
                </div>
                <div>
                  <dt className="font-medium uppercase tracking-wider text-outline">
                    Listed price
                  </dt>
                  <dd>
                    {service.max_price_usdc
                      ? `$${service.max_price_usdc} USDC`
                      : "—"}
                  </dd>
                </div>
              </dl>
              {endpoints.length > 0 ? (
                <div className="space-y-2 border-t border-outline-variant/10 pt-3">
                  <EndpointDropdown
                    endpoints={endpoints}
                    selectedEndpoint={selectedEndpoint}
                    onSelect={setSelectedEndpointKey}
                  />
                </div>
              ) : null}
              {service.enabled ? (
                <p className="text-xs text-secondary">
                  Approved {formatTimestamp(service.approved_at)} · max $
                  {service.approved_max_price_usdc ?? "—"}/call
                </p>
              ) : null}
              {error ? (
                <p className="text-xs text-red-600 dark:text-red-300">
                  {error}
                </p>
              ) : null}
            </div>
          </div>
          <div className="flex shrink-0 items-start gap-2">
            {!service.enabled && !editing ? (
              <button
                type="button"
                disabled={busy}
                onClick={startApprove}
                className="rounded-full bg-emerald-500/10 px-4 py-1.5 text-sm font-medium text-emerald-700 transition-colors hover:bg-emerald-500/20 disabled:cursor-not-allowed disabled:opacity-50 dark:text-emerald-200"
              >
                Approve
              </button>
            ) : null}
            {service.enabled ? (
              <button
                type="button"
                disabled={busy}
                onClick={() => onRevoke(service)}
                className="rounded-full bg-red-500/10 px-4 py-1.5 text-sm font-medium text-red-700 transition-colors hover:bg-red-500/20 disabled:cursor-not-allowed disabled:opacity-50 dark:text-red-200"
              >
                Revoke
              </button>
            ) : null}
          </div>
        </div>
        {editing ? (
          <form
            className="flex flex-wrap items-end gap-3 rounded-xl border border-emerald-500/20 bg-emerald-500/5 p-3"
            onSubmit={submitApprove}
          >
            <label className="flex flex-col text-xs text-secondary">
              <span className="mb-1 font-medium uppercase tracking-wider text-outline">
                Max price per call (USDC)
              </span>
              <input
                type="text"
                inputMode="decimal"
                value={maxPrice}
                onChange={(event) => setMaxPrice(event.target.value)}
                placeholder={service.max_price_usdc ?? "0.005"}
                className="rounded-md border border-outline-variant/30 bg-surface px-2 py-1 text-sm"
              />
            </label>
            <div className="ml-auto flex gap-2">
              <button
                type="button"
                onClick={cancelApprove}
                className="rounded-full px-4 py-1.5 text-sm text-secondary hover:bg-surface-container"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={busy}
                className="rounded-full bg-emerald-500/20 px-4 py-1.5 text-sm font-medium text-emerald-700 transition-colors hover:bg-emerald-500/30 disabled:cursor-not-allowed disabled:opacity-50 dark:text-emerald-200"
              >
                Confirm approval
              </button>
            </div>
          </form>
        ) : null}
      </div>
    </div>
  );
}

function WalletStatusBanner() {
  const { data: status } = useRPC(() => wallet.status(), [], {
    refreshIntervalMs: 30_000,
  });
  if (!status) return null;
  if (!status.installed) {
    return (
      <div className="flex items-start gap-3 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-800 dark:text-amber-200">
        <Icon className="text-base" name="warning" />
        <div className="flex-1">
          <p className="font-medium">OWS not installed.</p>
          <p>
            Agents cannot make x402 calls until you install the wallet. Install
            it from{" "}
            <Link className="underline" to="/settings/wallet">
              Settings → Wallet
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
        <div className="flex-1">
          <p className="font-medium">No wallet yet.</p>
          <p>
            Create and fund a wallet from{" "}
            <Link className="underline" to="/settings/wallet">
              Settings → Wallet
            </Link>{" "}
            before approving paid services.
          </p>
        </div>
      </div>
    );
  }
  return null;
}

function BudgetCard() {
  const { data, error } = useRPC(() => x402.budgetStatus(), [], {
    refreshIntervalMs: 15_000,
  });
  if (error) {
    return (
      <div className="rounded-2xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-200">
        Budget unavailable: {error}
      </div>
    );
  }
  if (!data) return null;
  if (data.agents === 0) {
    return (
      <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-4 text-sm text-secondary">
        No agents have used x402 yet. Defaults will apply ($0.10 per call,
        $5.00 per day) the first time an agent calls a service.
      </div>
    );
  }
  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-4">
      <div className="grid grid-cols-3 gap-4">
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Per-call max
          </p>
          <p className="mt-1 text-lg font-semibold text-on-surface">
            ${data.per_call_max_usdc}
          </p>
        </div>
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Daily cap
          </p>
          <p className="mt-1 text-lg font-semibold text-on-surface">
            ${data.daily_cap_usdc}
          </p>
        </div>
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            Spent today
          </p>
          <p className="mt-1 text-lg font-semibold text-on-surface">
            ${data.spent_today_usdc}
          </p>
        </div>
      </div>
      <p className="mt-3 text-xs text-secondary">
        Aggregated across {data.agents} agent{data.agents === 1 ? "" : "s"}.
      </p>
    </div>
  );
}

function ReceiptsCard() {
  const { data, error } = useRPC(() => x402.receipts({ limit: 25 }), [], {
    refreshIntervalMs: 15_000,
  });
  if (error) {
    return (
      <div className="rounded-2xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-200">
        Receipts unavailable: {error}
      </div>
    );
  }
  const receipts = data?.receipts ?? [];
  if (receipts.length === 0) {
    return (
      <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-4 text-sm text-secondary">
        No charges yet. Receipts appear here after the first successful agent
        call to an approved service.
      </div>
    );
  }
  return (
    <div className="overflow-hidden rounded-2xl border border-outline-variant/10 bg-surface-container-lowest">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-outline-variant/10 text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            <th className="px-4 py-2">Time</th>
            <th className="px-4 py-2">Service</th>
            <th className="px-4 py-2">Agent</th>
            <th className="px-4 py-2 text-right">Amount</th>
            <th className="px-4 py-2">Tx</th>
          </tr>
        </thead>
        <tbody>
          {receipts.map((r: X402Receipt, idx) => (
            <tr
              key={`${r.ts}-${r.agent_id}-${idx}`}
              className="border-b border-outline-variant/5 last:border-0"
            >
              <td className="px-4 py-2 text-xs text-secondary">
                {formatTimestamp(r.ts)}
              </td>
              <td className="px-4 py-2 text-on-surface">
                {r.service_name || r.service_id}
              </td>
              <td className="px-4 py-2 font-mono text-xs text-secondary">
                {truncate(r.agent_id, 6)}
              </td>
              <td className="px-4 py-2 text-right font-medium text-on-surface">
                ${r.amount_usdc}
              </td>
              <td className="px-4 py-2 font-mono text-xs text-secondary">
                {truncate(r.tx)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function SettingsServices() {
  const {
    data,
    error,
    loading,
    refetch,
  } = useRPC(() => x402.listServices(), [], {
    refreshIntervalMs: 30_000,
  });

  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string | null>>({});

  const onApprove = useCallback(
    async (service: X402ServiceListing, maxPriceUSDC: string) => {
      setBusy((prev) => ({ ...prev, [service.id]: true }));
      setErrors((prev) => ({ ...prev, [service.id]: null }));
      try {
        await x402.setEnabled({
          service_id: service.id,
          enabled: true,
          max_price_usdc: maxPriceUSDC || undefined,
        });
        await refetch();
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setErrors((prev) => ({ ...prev, [service.id]: message }));
      } finally {
        setBusy((prev) => ({ ...prev, [service.id]: false }));
      }
    },
    [refetch],
  );

  const onRevoke = useCallback(
    async (service: X402ServiceListing) => {
      setBusy((prev) => ({ ...prev, [service.id]: true }));
      setErrors((prev) => ({ ...prev, [service.id]: null }));
      try {
        await x402.setEnabled({ service_id: service.id, enabled: false });
        await refetch();
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        setErrors((prev) => ({ ...prev, [service.id]: message }));
      } finally {
        setBusy((prev) => ({ ...prev, [service.id]: false }));
      }
    },
    [refetch],
  );

  const services = data?.services ?? [];

  const [search, setSearch] = useState("");
  const [tierFilter, setTierFilter] = useState<"all" | "primitive" | "convenience">("all");
  const [statusFilter, setStatusFilter] = useState<"all" | "approved" | "not_approved">("all");

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return services.filter((s) => {
      if (tierFilter !== "all" && s.tier !== tierFilter) return false;
      if (statusFilter === "approved" && !s.enabled) return false;
      if (statusFilter === "not_approved" && s.enabled) return false;
      if (!q) return true;
      const haystack = [
        s.id,
        s.display_name,
        s.description,
        s.category,
        tierLabel(s.tier),
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(q);
    });
  }, [services, search, tierFilter, statusFilter]);

  return (
    <SettingsPage
      title="Services"
      description="Approve external services your agents can call. Each service charges per request in USDC; calls only succeed once your wallet is installed and funded."
      pinnablePageID="services"
    >
      <WalletStatusBanner />

      <section className="space-y-3">
        <h2 className="text-lg font-semibold text-on-surface">Budget</h2>
        <BudgetCard />
      </section>

      <section className="space-y-3">
        <h2 className="text-lg font-semibold text-on-surface">Recent charges</h2>
        <ReceiptsCard />
      </section>

      <section className="space-y-4">
        <header className="flex items-center justify-between gap-4">
          <div>
            <h2 className="text-lg font-semibold text-on-surface">
              Agentic.Market
            </h2>
            <p className="text-sm text-secondary">
              Curated catalog of x402-enabled services. Approve a service to
              make it available to any agent on this device, with a per-call
              max price you set.
            </p>
          </div>
          {loading ? (
            <Icon className="text-base text-outline" name="progress_activity" />
          ) : null}
        </header>

        <div className="flex flex-wrap items-center gap-3">
          <div className="relative flex-1 min-w-[200px]">
            <Icon
              className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-base text-outline"
              name="search"
            />
            <input
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search by name, category, or description"
              className="w-full rounded-full border border-outline-variant/30 bg-surface-container-lowest py-2 pl-10 pr-4 text-sm focus:outline-none focus:ring-2 focus:ring-emerald-500/30"
            />
          </div>
          <select
            value={tierFilter}
            onChange={(event) =>
              setTierFilter(event.target.value as typeof tierFilter)
            }
            className="rounded-full border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-emerald-500/30"
          >
            <option value="all">All routing types</option>
            <option value="primitive">Hard to do locally</option>
            <option value="convenience">Optional paid APIs</option>
          </select>
          <select
            value={statusFilter}
            onChange={(event) =>
              setStatusFilter(event.target.value as typeof statusFilter)
            }
            className="rounded-full border border-outline-variant/30 bg-surface-container-lowest px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-emerald-500/30"
          >
            <option value="all">All services</option>
            <option value="approved">Approved only</option>
            <option value="not_approved">Not yet approved</option>
          </select>
          <p className="text-xs text-secondary">
            Showing {filtered.length} of {services.length}
          </p>
        </div>

        {error ? (
          <div className="rounded-2xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-200">
            Failed to load services: {error}
          </div>
        ) : null}

        {!error && services.length === 0 && !loading ? (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 text-sm text-secondary">
            No services available yet. The daemon seeds the starter catalog on
            startup; if this list stays empty, check the daemon log for x402
            seed errors.
          </div>
        ) : null}

        {!error && services.length > 0 && filtered.length === 0 ? (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 text-sm text-secondary">
            No services match your filters. Adjust the search or change the
            routing/status dropdowns.
          </div>
        ) : null}

        <div className="grid gap-3">
          {filtered.map((service) => (
            <ServiceCard
              key={service.id}
              service={service}
              busy={busy[service.id] ?? false}
              error={errors[service.id] ?? null}
              onApprove={onApprove}
              onRevoke={onRevoke}
            />
          ))}
        </div>
      </section>
    </SettingsPage>
  );
}
