import { useCallback, useState } from "react";
import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import {
  wallet,
  x402,
  type X402Receipt,
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
        <div className="flex items-start justify-between gap-4">
          <div className="flex-1 space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-base font-semibold text-on-surface">
                {service.display_name || service.id}
              </h3>
              <span
                className={`rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider ${tierBadgeClasses(service.tier)}`}
              >
                {service.tier}
              </span>
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
                <dd>{service.category || "—"}</dd>
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
            {service.enabled ? (
              <p className="text-xs text-secondary">
                Approved {formatTimestamp(service.approved_at)} · max $
                {service.approved_max_price_usdc ?? "—"}/call
              </p>
            ) : null}
            {error ? (
              <p className="text-xs text-red-600 dark:text-red-300">{error}</p>
            ) : null}
          </div>
          <div className="flex items-start gap-2">
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

        {error ? (
          <div className="rounded-2xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-200">
            Failed to load services: {error}
          </div>
        ) : null}

        {!error && services.length === 0 && !loading ? (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 text-sm text-secondary">
            No services available yet. The daemon seeds the curated primitive
            set on startup; if this list stays empty, check the daemon log for
            x402 seed errors.
          </div>
        ) : null}

        <div className="grid gap-3">
          {services.map((service) => (
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
