import { useCallback, useState } from "react";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { x402, type X402ServiceListing } from "../lib/rpc";
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

interface ServiceCardProps {
  service: X402ServiceListing;
  busy: boolean;
  error: string | null;
  onToggle: (service: X402ServiceListing, next: boolean) => void;
}

function ServiceCard({ service, busy, error, onToggle }: ServiceCardProps) {
  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm">
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
                Price
              </dt>
              <dd>
                {service.max_price_usdc ? `$${service.max_price_usdc} USDC` : "—"}
              </dd>
            </div>
          </dl>
          {error ? (
            <p className="text-xs text-red-600 dark:text-red-300">{error}</p>
          ) : null}
        </div>
        <label className="flex items-center gap-2 self-start">
          <input
            type="checkbox"
            disabled={busy}
            checked={service.enabled}
            onChange={(event) => onToggle(service, event.target.checked)}
            className="h-5 w-5 cursor-pointer accent-emerald-600 disabled:cursor-not-allowed disabled:opacity-50"
            aria-label={`Toggle ${service.display_name || service.id}`}
          />
          <span className="text-sm font-medium text-on-surface">
            {service.enabled ? "On" : "Off"}
          </span>
        </label>
      </div>
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

  const onToggle = useCallback(
    async (service: X402ServiceListing, next: boolean) => {
      setBusy((prev) => ({ ...prev, [service.id]: true }));
      setErrors((prev) => ({ ...prev, [service.id]: null }));
      try {
        await x402.setEnabled({ service_id: service.id, enabled: next });
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
      <section className="space-y-4">
        <header className="flex items-center justify-between gap-4">
          <div>
            <h2 className="text-lg font-semibold text-on-surface">
              Agentic.Market
            </h2>
            <p className="text-sm text-secondary">
              Curated catalog of x402-enabled services. Toggle a service on to
              make it available to any agent on this device.
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
              onToggle={onToggle}
            />
          ))}
        </div>
      </section>
    </SettingsPage>
  );
}
