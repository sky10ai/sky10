const CONNECT_POPUP_FEATURES = "popup=yes,width=560,height=760";

const CONNECT_POPUP_HTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Opening ChatGPT…</title>
  <style>
    body { font-family: ui-sans-serif, system-ui, sans-serif; background: #f4f1ea; color: #1d1a17; padding: 40px; }
    main { max-width: 640px; margin: 0 auto; background: #fffdf8; border: 1px solid #ded7cb; border-radius: 20px; padding: 32px; box-shadow: 0 12px 32px rgba(61, 47, 34, 0.08); }
    h1 { margin: 0 0 12px; font-size: 28px; }
    p { margin: 0; line-height: 1.5; color: #5a5148; }
  </style>
</head>
<body>
  <main>
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
