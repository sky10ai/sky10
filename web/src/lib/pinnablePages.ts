export const PINNED_SIDEBAR_PAGES_STORAGE_KEY =
  "sky10:sidebar:pinned-pages";
export const PINNED_SIDEBAR_PAGES_CHANGED_EVENT =
  "sky10:sidebar:pinned-pages.changed";

export const PINNABLE_PAGES = [
  {
    id: "wallet",
    icon: "account_balance_wallet",
    label: "Wallet",
    matchPrefixes: ["/settings/wallet", "/wallet"],
    to: "/settings/wallet",
  },
  {
    id: "kv",
    icon: "database",
    label: "Key-Value",
    matchPrefixes: ["/settings/kv", "/kv"],
    to: "/settings/kv",
  },
  {
    id: "mailbox",
    icon: "inbox",
    label: "Mailbox",
    matchPrefixes: ["/settings/mailbox", "/mailbox"],
    to: "/settings/mailbox",
  },
  {
    id: "messaging",
    icon: "forum",
    label: "Messaging",
    matchPrefixes: ["/settings/messaging"],
    to: "/settings/messaging",
  },
  {
    id: "ai-connections",
    icon: "smart_toy",
    label: "AI Connections",
    matchPrefixes: ["/settings/ai-connections"],
    to: "/settings/ai-connections",
  },
  {
    id: "network",
    icon: "hub",
    label: "Network",
    matchPrefixes: ["/settings/network", "/network"],
    to: "/settings/network",
  },
  {
    id: "devices",
    icon: "devices",
    label: "Devices",
    matchPrefixes: ["/settings/devices", "/devices"],
    to: "/settings/devices",
  },
  {
    id: "secrets",
    icon: "key_vertical",
    label: "Secrets",
    matchPrefixes: ["/settings/secrets"],
    to: "/settings/secrets",
  },
  {
    id: "sandboxes",
    icon: "deployed_code",
    label: "Sandboxes",
    matchPrefixes: ["/settings/sandboxes", "/sandboxes"],
    to: "/settings/sandboxes",
  },
  {
    id: "apps",
    icon: "download",
    label: "Apps",
    matchPrefixes: ["/settings/apps"],
    to: "/settings/apps",
  },
  {
    id: "services",
    icon: "storefront",
    label: "Services",
    matchPrefixes: ["/settings/services"],
    to: "/settings/services",
  },
  {
    id: "activity",
    icon: "monitor_heart",
    label: "Activity",
    matchPrefixes: ["/settings/activity", "/activity"],
    to: "/settings/activity",
  },
] as const;

export type PinnablePage = (typeof PINNABLE_PAGES)[number];
export type PinnablePageID = PinnablePage["id"];

export const DEFAULT_PINNED_PAGE_IDS: readonly PinnablePageID[] = [];

const pinnablePageIDs = new Set<string>(PINNABLE_PAGES.map((page) => page.id));

export function isPinnablePageID(value: string): value is PinnablePageID {
  return pinnablePageIDs.has(value);
}

export function normalizePinnedPageIDs(value: unknown): PinnablePageID[] {
  if (!Array.isArray(value)) return [];

  const seen = new Set<PinnablePageID>();
  for (const item of value) {
    if (typeof item !== "string" || !isPinnablePageID(item)) continue;
    if (seen.has(item)) continue;
    seen.add(item);
  }

  return [...seen];
}

export function parsePinnedPageIDs(raw: string | null): PinnablePageID[] {
  if (raw === null) return [...DEFAULT_PINNED_PAGE_IDS];

  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [...DEFAULT_PINNED_PAGE_IDS];
    return normalizePinnedPageIDs(parsed);
  } catch {
    return [...DEFAULT_PINNED_PAGE_IDS];
  }
}

export function getPinnablePage(id: PinnablePageID) {
  return PINNABLE_PAGES.find((page) => page.id === id);
}

export function resolvePinnedPages(ids: readonly PinnablePageID[]) {
  return ids.flatMap((id) => {
    const page = getPinnablePage(id);
    return page ? [page] : [];
  });
}

export function isPinnablePagePath(
  pathname: string,
  page: Pick<PinnablePage, "matchPrefixes">,
) {
  return page.matchPrefixes.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`),
  );
}

export function readPinnedPageIDs() {
  if (typeof window === "undefined") return [...DEFAULT_PINNED_PAGE_IDS];

  try {
    return parsePinnedPageIDs(
      window.localStorage.getItem(PINNED_SIDEBAR_PAGES_STORAGE_KEY),
    );
  } catch {
    return [...DEFAULT_PINNED_PAGE_IDS];
  }
}

export function writePinnedPageIDs(ids: readonly PinnablePageID[]) {
  const normalized = normalizePinnedPageIDs(ids);

  if (typeof window !== "undefined") {
    try {
      window.localStorage.setItem(
        PINNED_SIDEBAR_PAGES_STORAGE_KEY,
        JSON.stringify(normalized),
      );
    } catch {
      // Ignore localStorage write failures; the in-memory UI state still updates.
    }

    window.dispatchEvent(
      new CustomEvent(PINNED_SIDEBAR_PAGES_CHANGED_EVENT, {
        detail: normalized,
      }),
    );
  }

  return normalized;
}
