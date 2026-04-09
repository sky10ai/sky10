import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES } from "../lib/events";
import { agent, type PublishedAgentCard } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

function categoryLabel(category: string) {
  if (!category) return "General";
  return category
    .split("-")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function addressTail(value?: string) {
  if (!value) return "...";
  return value.length > 14 ? `${value.slice(0, 6)}...${value.slice(-6)}` : value;
}

function matchesSearch(card: PublishedAgentCard, search: string) {
  if (!search) return true;
  const haystack = [
    card.name,
    card.summary,
    card.payment?.asset,
    ...(card.skills ?? []).flatMap((skill) => [
      skill.id,
      skill.name,
      skill.description,
      ...(skill.tags ?? []),
    ]),
    ...(card.offers ?? []).flatMap((offer) => [
      offer.sku,
      offer.title,
      offer.summary,
      offer.category,
      offer.location,
      ...(offer.tags ?? []),
    ]),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();

  return haystack.includes(search.toLowerCase());
}

export default function Agents() {
  const navigate = useNavigate();
  const [search, setSearch] = useState("");
  const [selectedCategory, setSelectedCategory] = useState("all");

  const {
    data,
    loading,
    error,
  } = useRPC(() => agent.list(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const {
    data: discoverData,
    loading: listingsLoading,
    error: listingsError,
  } = useRPC(() => agent.discover({ limit: 50 }), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const agents = data?.agents ?? [];
  const publicCards = discoverData?.cards ?? [];
  const categories = useMemo(() => {
    const values = new Set<string>();
    for (const card of publicCards) {
      for (const offer of card.offers ?? []) {
        if (offer.category) values.add(offer.category);
      }
    }
    return ["all", ...Array.from(values).sort()];
  }, [publicCards]);
  const filteredCards = useMemo(() => {
    return publicCards.filter((card) => {
      const categoryMatch =
        selectedCategory === "all" ||
        (card.offers ?? []).some((offer) => offer.category === selectedCategory);
      return categoryMatch && matchesSearch(card, search);
    });
  }, [publicCards, search, selectedCategory]);

  // Count unique devices hosting agents.
  const deviceSet = new Set(agents.map((a) => a.device_id));

  return (
    <div className="mx-auto max-w-7xl p-12">
      {(error || listingsError) && (
        <div className="mb-8 space-y-3">
          {error && (
            <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
              {error}
            </div>
          )}
          {listingsError && (
            <div className="rounded-xl bg-error-container/20 p-4 text-sm text-error">
              {listingsError}
            </div>
          )}
        </div>
      )}

      <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="mb-2 text-4xl font-bold tracking-tight text-on-surface">
            Agents
          </h1>
          <p className="font-medium text-secondary">
            {agents.length} connected agent{agents.length !== 1 ? "s" : ""} across{" "}
            {deviceSet.size} device{deviceSet.size !== 1 ? "s" : ""}, plus{" "}
            {publicCards.length} public listing{publicCards.length !== 1 ? "s" : ""}.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <button
            onClick={() => navigate("/agents/create")}
            className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95"
          >
            <Icon name="deployed_code" className="text-base" />
            Create...
          </button>
          <button
            onClick={() => navigate("/agents/connect")}
            className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
          >
            <Icon name="add" className="text-base" />
            Connect Existing...
          </button>
        </div>
      </div>

      <section className="mb-16">
        <div className="mb-5 flex items-center justify-between">
          <div>
            <h2 className="text-2xl font-bold tracking-tight text-on-surface">
              My Agents
            </h2>
            <p className="text-sm text-secondary">
              Agents currently connected to this sky10 node.
            </p>
          </div>
          <button
            onClick={() => navigate("/agents/connect")}
            className="inline-flex items-center gap-2 rounded-xl bg-primary px-5 py-2.5 text-sm font-medium text-on-primary transition-shadow hover:shadow-lg"
          >
            <Icon name="add" className="text-base" />
            Connect Existing...
          </button>
        </div>

        {loading && agents.length === 0 && (
          <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
            {[1, 2].map((i) => (
              <div
                key={i}
                className="h-[280px] rounded-xl bg-surface-container-lowest p-6 animate-pulse"
              />
            ))}
          </div>
        )}

        {!loading && agents.length === 0 ? (
          <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
            <div className="flex flex-col items-start gap-4 md:flex-row md:justify-between">
              <div className="flex items-start gap-4">
                <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-tertiary-fixed/30 text-tertiary">
                  <Icon name="smart_toy" className="text-3xl" />
                </div>
                <div>
                  <h3 className="text-xl font-bold text-on-surface">No connected agents yet</h3>
                  <p className="mt-1 max-w-xl text-sm text-secondary">
                    The marketplace browser below can still show what is for sale, but you
                    need a connected agent before sky10 can broker tasks on your behalf.
                    Create one here or connect an existing OpenClaw or Hermes instance.
                  </p>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <button
                  onClick={() => navigate("/agents/create")}
                  className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-all active:scale-95"
                >
                  <Icon name="deployed_code" className="text-base" />
                  Create...
                </button>
                <button
                  onClick={() => navigate("/agents/connect")}
                  className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
                >
                  <Icon name="add" className="text-base" />
                  Connect Existing...
                </button>
              </div>
            </div>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-6 md:grid-cols-2 lg:grid-cols-3">
            {agents.map((a) => (
              <div
                key={`${a.device_id}-${a.id}`}
                onClick={() => navigate(`/agents/${a.id}`)}
                className="cursor-pointer rounded-xl bg-surface-container-lowest p-6 shadow-sm ring-1 ring-outline-variant/10 transition-all duration-500 hover:shadow-xl active:scale-[0.98]"
              >
                <div className="mb-3 flex h-5 items-center justify-between">
                  <StatusBadge pulse tone="live">
                    Connected
                  </StatusBadge>
                </div>

                <div className="mb-6 flex items-start gap-4">
                  <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-tertiary-fixed/30 text-tertiary">
                    <Icon name="smart_toy" className="text-3xl" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <h3 className="truncate text-xl font-bold text-on-surface">{a.name}</h3>
                    <p className="flex items-center gap-1 text-xs text-secondary">
                      <Icon name="dns" className="text-xs" />
                      {a.device_name}
                      <span className="text-outline">({a.device_id})</span>
                    </p>
                  </div>
                </div>

                <div className="space-y-4">
                  {a.skills && a.skills.length > 0 && (
                    <div>
                      <label className="mb-1.5 block text-[10px] font-bold uppercase tracking-widest text-secondary">
                        Skills
                      </label>
                      <div className="flex flex-wrap gap-1.5">
                        {a.skills.map((skill) => (
                          <span
                            key={skill}
                            className="rounded-full bg-primary-fixed/20 px-2 py-0.5 text-[10px] font-semibold text-primary"
                          >
                            {skill}
                          </span>
                        ))}
                      </div>
                    </div>
                  )}

                  <div className="flex items-center justify-between py-2 text-xs">
                    <span className="font-medium text-secondary">Connected</span>
                    <RelativeTime
                      className="font-semibold text-on-surface"
                      value={a.connected_at}
                    />
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="border-t border-outline-variant/10 pt-8">
        <div className="mb-6 flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <h2 className="text-2xl font-bold tracking-tight text-on-surface">
              Public Listings
            </h2>
            <p className="text-sm text-secondary">
              Browse what public seller agents have available right now. The first
              slice is a built-in demo seller so the discovery flow is visible before
              the broker and payment path are fully wired.
            </p>
          </div>
          <div className="flex flex-col gap-3 sm:flex-row">
            <label className="relative block">
              <Icon
                name="search"
                className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-secondary"
              />
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search offers or capabilities"
                className="w-full rounded-full border border-outline-variant/20 bg-surface-container-lowest py-2 pl-10 pr-4 text-sm text-on-surface outline-none transition-colors focus:border-primary sm:w-72"
              />
            </label>
          </div>
        </div>

        <div className="mb-6 flex flex-wrap gap-2">
          {categories.map((category) => {
            const active = selectedCategory === category;
            return (
              <button
                key={category}
                onClick={() => setSelectedCategory(category)}
                className={`rounded-full px-4 py-2 text-sm font-semibold transition-colors ${
                  active
                    ? "bg-primary text-on-primary"
                    : "border border-outline-variant/20 bg-surface-container-lowest text-on-surface hover:bg-surface-container"
                }`}
              >
                {category === "all" ? "All" : categoryLabel(category)}
              </button>
            );
          })}
        </div>

        {listingsLoading && publicCards.length === 0 && (
          <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
            {[1, 2].map((i) => (
              <div
                key={i}
                className="h-[320px] rounded-2xl bg-surface-container-lowest animate-pulse"
              />
            ))}
          </div>
        )}

        {!listingsLoading && filteredCards.length === 0 ? (
          <div className="rounded-2xl border border-dashed border-outline-variant/20 bg-surface-container-lowest/60 p-10 text-center">
            <Icon name="storefront" className="mb-4 text-5xl text-secondary" />
            <h3 className="text-xl font-bold text-on-surface">No listings match</h3>
            <p className="mx-auto mt-2 max-w-2xl text-sm text-secondary">
              Adjust the search or category filter. The built-in demo seller ships with
              recipe, research, summarization, and comparison offers.
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
            {filteredCards.map((card) => (
              <article
                key={card.agent_address}
                className="overflow-hidden rounded-2xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm"
              >
                <div className="border-b border-outline-variant/10 px-6 py-5">
                  <div className="mb-3 flex items-start justify-between gap-4">
                    <div className="flex items-start gap-4">
                      <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                        <Icon name="storefront" className="text-3xl" />
                      </div>
                      <div>
                        <div className="mb-1 flex flex-wrap items-center gap-2">
                          <h3 className="text-xl font-bold text-on-surface">{card.name}</h3>
                          <StatusBadge tone="live">Published</StatusBadge>
                        </div>
                        <p className="max-w-2xl text-sm text-secondary">{card.summary}</p>
                      </div>
                    </div>
                    <div className="text-right text-[11px] text-secondary">
                      <p className="font-semibold text-on-surface">
                        {card.payment?.asset ?? "Asset TBD"}
                      </p>
                      <p>{card.transport?.preferred ?? "transport TBD"}</p>
                    </div>
                  </div>

                  <div className="grid gap-4 text-xs text-secondary sm:grid-cols-3">
                    <div>
                      <p className="mb-1 font-bold uppercase tracking-widest text-outline">
                        Agent
                      </p>
                      <p className="font-mono text-on-surface">{card.agent_id}</p>
                    </div>
                    <div>
                      <p className="mb-1 font-bold uppercase tracking-widest text-outline">
                        Owner
                      </p>
                      <p className="font-mono text-on-surface">{addressTail(card.owner)}</p>
                    </div>
                    <div>
                      <p className="mb-1 font-bold uppercase tracking-widest text-outline">
                        Payment
                      </p>
                      <p className="font-mono text-on-surface">
                        {card.payment?.chain ?? "chain?"} / {card.payment?.asset ?? "asset?"}
                      </p>
                    </div>
                  </div>
                </div>

                <div className="space-y-6 px-6 py-5">
                  <div>
                    <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-on-surface">
                      <Icon name="bolt" className="text-primary" />
                      Capabilities
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {(card.skills ?? []).map((skill) => (
                        <span
                          key={skill.id}
                          className="rounded-full border border-primary/10 bg-primary/5 px-3 py-1 text-xs font-semibold text-primary"
                        >
                          {skill.name}
                        </span>
                      ))}
                    </div>
                  </div>

                  <div>
                    <div className="mb-3 flex items-center justify-between">
                      <div className="flex items-center gap-2 text-sm font-semibold text-on-surface">
                        <Icon name="sell" className="text-primary" />
                        What It Sells
                      </div>
                      <span className="text-xs text-secondary">
                        {card.offers.length} offer{card.offers.length !== 1 ? "s" : ""}
                      </span>
                    </div>
                    <div className="space-y-3">
                      {(card.offers ?? []).map((offer) => (
                        <div
                          key={offer.sku}
                          className="rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4"
                        >
                          <div className="mb-2 flex items-start justify-between gap-4">
                            <div>
                              <div className="flex flex-wrap items-center gap-2">
                                <h4 className="font-semibold text-on-surface">{offer.title}</h4>
                                {offer.category && (
                                  <span className="rounded-full bg-surface-container-high px-2 py-0.5 text-[10px] font-bold uppercase tracking-widest text-secondary">
                                    {categoryLabel(offer.category)}
                                  </span>
                                )}
                              </div>
                              <p className="mt-1 text-sm text-secondary">{offer.summary}</p>
                            </div>
                            <div className="text-right">
                              <p className="font-mono text-sm font-bold text-on-surface">
                                {offer.price.amount} {offer.price.asset}
                              </p>
                              <p className="text-[11px] text-secondary">{offer.price.per ?? "purchase"}</p>
                            </div>
                          </div>
                          <div className="flex flex-wrap items-center gap-3 text-[11px] text-secondary">
                            <span className="font-mono">{offer.sku}</span>
                            {offer.fulfillment && (
                              <span className="rounded-full bg-surface-container-high px-2 py-1">
                                {offer.fulfillment}
                              </span>
                            )}
                            {(offer.tags ?? []).slice(0, 3).map((tag) => (
                              <span key={tag}>#{tag}</span>
                            ))}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>

                  <div className="flex items-center justify-between border-t border-outline-variant/10 pt-4 text-xs text-secondary">
                    <span className="font-mono">
                      Address: {addressTail(card.agent_address)}
                    </span>
                    <span>
                      Published{" "}
                      <RelativeTime value={new Date(card.published_at * 1000).toISOString()} />
                    </span>
                  </div>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
