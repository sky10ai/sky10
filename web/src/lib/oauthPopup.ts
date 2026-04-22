const CONNECT_POPUP_FEATURES = "popup=yes,width=560,height=760";

const CONNECT_POPUP_HTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Opening ChatGPT…</title>
  <style>
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 32px;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background:
        radial-gradient(circle at top, rgba(16, 163, 127, 0.08), transparent 32%),
        linear-gradient(180deg, #f7f7f8 0%, #efeff1 100%);
      color: #202123;
    }
    main {
      width: min(100%, 560px);
      border-radius: 24px;
      background: rgba(255, 255, 255, 0.94);
      border: 1px solid rgba(32, 33, 35, 0.08);
      box-shadow:
        0 24px 64px rgba(15, 23, 42, 0.10),
        0 2px 12px rgba(15, 23, 42, 0.04);
      padding: 32px;
    }
    .eyebrow {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      margin: 0 0 18px;
      padding: 8px 12px;
      border-radius: 999px;
      background: rgba(16, 163, 127, 0.10);
      color: #0f5132;
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.12em;
      text-transform: uppercase;
    }
    .dot {
      width: 8px;
      height: 8px;
      border-radius: 999px;
      background: #10a37f;
      box-shadow: 0 0 0 6px rgba(16, 163, 127, 0.12);
    }
    h1 {
      margin: 0 0 12px;
      font-size: 30px;
      line-height: 1.1;
      letter-spacing: -0.03em;
    }
    p {
      margin: 0;
      line-height: 1.6;
      color: #5f6368;
      font-size: 15px;
    }
  </style>
</head>
<body>
  <main>
    <div class="eyebrow"><span class="dot"></span>ChatGPT Sign-In</div>
    <h1>Opening ChatGPT…</h1>
    <p>sky10 is preparing the ChatGPT sign-in flow for this device.</p>
  </main>
</body>
</html>`;

type PopupLike = Pick<Window, "close" | "document" | "location"> & {
  closed?: boolean;
  opener?: Window | null;
};

type WindowLike = Pick<Window, "open" | "location">;

export function openOAuthPopup(browser: WindowLike): PopupLike | null {
  const popup = browser.open("about:blank", "_blank", CONNECT_POPUP_FEATURES) as PopupLike | null;
  if (!popup) return null;

  try {
    popup.opener = null;
  } catch {
    // Some browsers expose opener as read-only on detached popups.
  }

  try {
    popup.document.open();
    popup.document.write(CONNECT_POPUP_HTML);
    popup.document.close();
  } catch {
    // If the placeholder cannot be rendered, continue and still try to navigate.
  }

  return popup;
}

export function navigateOAuthPopup(browser: Pick<Window, "location">, popup: PopupLike | null, url: string) {
  if (popup && !popup.closed) {
    popup.location.replace(url);
    return;
  }
  browser.location.assign(url);
}

export function closeOAuthPopup(popup: PopupLike | null) {
  if (!popup || popup.closed) return;
  try {
    popup.close();
  } catch {
    // Ignore close failures.
  }
}
