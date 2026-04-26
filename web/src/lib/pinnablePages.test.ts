import { describe, expect, test } from "bun:test";
import {
  DEFAULT_PINNED_PAGE_IDS,
  PINNABLE_PAGES,
  getPinnablePage,
  isPinnablePagePath,
  normalizePinnedPageIDs,
  parsePinnedPageIDs,
  resolvePinnedPages,
} from "./pinnablePages";

describe("parsePinnedPageIDs", () => {
  test("starts with no pinned pages when no preference has been saved", () => {
    expect(DEFAULT_PINNED_PAGE_IDS).toEqual([]);
    expect(parsePinnedPageIDs(null)).toEqual([]);
  });

  test("does not pin every pinnable page by default", () => {
    expect(DEFAULT_PINNED_PAGE_IDS.length).toBeLessThan(PINNABLE_PAGES.length);
    expect(DEFAULT_PINNED_PAGE_IDS).not.toContain("secrets");
    expect(DEFAULT_PINNED_PAGE_IDS).not.toContain("sandboxes");
    expect(DEFAULT_PINNED_PAGE_IDS).not.toContain("apps");
    expect(DEFAULT_PINNED_PAGE_IDS).not.toContain("activity");
  });

  test("preserves an intentionally empty pinned-page list", () => {
    expect(parsePinnedPageIDs("[]")).toEqual([]);
  });

  test("falls back to defaults when saved data is invalid", () => {
    expect(parsePinnedPageIDs("not json")).toEqual([
      ...DEFAULT_PINNED_PAGE_IDS,
    ]);
    expect(parsePinnedPageIDs(JSON.stringify({ wallet: true }))).toEqual([
      ...DEFAULT_PINNED_PAGE_IDS,
    ]);
  });
});

describe("normalizePinnedPageIDs", () => {
  test("filters unknown pages and deduplicates known pages", () => {
    expect(
      normalizePinnedPageIDs(["wallet", "unknown", "wallet", "kv"]),
    ).toEqual(["wallet", "kv"]);
  });
});

describe("resolvePinnedPages", () => {
  test("returns page metadata in pinned order", () => {
    expect(resolvePinnedPages(["network", "wallet"]).map((page) => page.label))
      .toEqual(["Network", "Wallet"]);
  });
});

describe("isPinnablePagePath", () => {
  test("matches nested page paths", () => {
    const devices = getPinnablePage("devices");
    expect(devices).toBeDefined();
    expect(isPinnablePagePath("/settings/devices/invite", devices!)).toBe(
      true,
    );
  });
});
