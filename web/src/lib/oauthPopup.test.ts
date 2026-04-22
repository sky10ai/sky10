import { describe, expect, test } from "bun:test";
import {
  closeOAuthPopup,
  navigateOAuthPopup,
  openOAuthPopup,
} from "./oauthPopup";

describe("oauth popup helpers", () => {
  test("falls back to current-tab navigation when the popup is blocked", () => {
    let assignedURL = "";
    let openCalls = 0;
    const browser = {
      open: () => {
        openCalls += 1;
        return null;
      },
      location: {
        assign: (url: string) => {
          assignedURL = url;
        },
      },
    } as unknown as Window;

    const popup = openOAuthPopup(browser);
    navigateOAuthPopup(browser, popup, "https://auth.openai.com/oauth/authorize?code=123");

    expect(openCalls).toBe(1);
    expect(assignedURL).toBe("https://auth.openai.com/oauth/authorize?code=123");
  });

  test("reuses the opened popup when a handle is available", () => {
    let replacedURL = "";
    let closeCalls = 0;
    const popup = {
      closed: false,
      opener: null,
      close: () => {
        closeCalls += 1;
      },
      document: {
        open: () => {},
        write: () => {},
        close: () => {},
      },
      location: {
        replace: (url: string) => {
          replacedURL = url;
        },
      },
    };
    const browser = {
      open: () => popup,
      location: {
        assign: (_url: string) => {
          throw new Error("should not fall back to same-tab navigation");
        },
      },
    } as unknown as Window;

    const handle = openOAuthPopup(browser);
    navigateOAuthPopup(browser, handle, "https://auth.openai.com/oauth/authorize?code=456");
    closeOAuthPopup(handle);

    expect(handle).toBe(popup);
    expect(replacedURL).toBe("https://auth.openai.com/oauth/authorize?code=456");
    expect(closeCalls).toBe(1);
  });
});
