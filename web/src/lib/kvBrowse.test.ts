import { describe, expect, test } from "bun:test";
import {
  buildKVBrowseQuery,
  isInternalKVKey,
  matchesKVBrowseView,
} from "./kvBrowse";

describe("isInternalKVKey", () => {
  test("recognizes reserved _sys prefixes", () => {
    expect(isInternalKVKey("_sys/secrets/head")).toBe(true);
    expect(isInternalKVKey("_sys:secrets:head")).toBe(true);
    expect(isInternalKVKey("user/config")).toBe(false);
  });
});

describe("buildKVBrowseQuery", () => {
  test("keeps reserved entries hidden by default", () => {
    expect(buildKVBrowseQuery(false, "")).toBeUndefined();
  });

  test("includes internal keys when the toggle is enabled", () => {
    expect(buildKVBrowseQuery(true, "")).toEqual({ include_internal: true });
  });

  test("passes a trimmed prefix filter through to RPC", () => {
    expect(buildKVBrowseQuery(true, "  _sys/secrets/  ")).toEqual({
      include_internal: true,
      prefix: "_sys/secrets/",
    });
  });
});

describe("matchesKVBrowseView", () => {
  test("hides internal keys when the toggle is off", () => {
    expect(matchesKVBrowseView("_sys/secrets/head", false, "")).toBe(false);
    expect(matchesKVBrowseView("settings/theme", false, "")).toBe(true);
  });

  test("shows both user and system keys when unfiltered system view is enabled", () => {
    expect(matchesKVBrowseView("_sys/secrets/head", true, "")).toBe(true);
    expect(matchesKVBrowseView("settings/theme", true, "")).toBe(true);
  });

  test("narrows the list to the configured prefix when a filter is set", () => {
    expect(matchesKVBrowseView("_sys/secrets/head", true, "_sys/secrets/")).toBe(
      true
    );
    expect(matchesKVBrowseView("_sys/mailbox/head", true, "_sys/secrets/")).toBe(
      false
    );
    expect(matchesKVBrowseView("settings/theme", true, "_sys/secrets/")).toBe(
      false
    );
  });
});
