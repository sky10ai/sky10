import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { CODEX_EVENT_TYPES } from "../lib/events";
import { closeOAuthPopup, navigateOAuthPopup, openOAuthPopup } from "../lib/oauthPopup";
import { codex } from "../lib/rpc";
import { timeAgo, useRPC } from "../lib/useRPC";

type AgentAudience = "for_me" | "for_others";

function parseAudience(value: string | null): AgentAudience | null {
  if (value === "for_me" || value === "for_others") {
    return value;
  }
  return null;
}

function authLabelForStatus(authMode?: string, authLabel?: string) {
  if (authLabel) return authLabel;
  if (authMode === "chatgpt") return "ChatGPT";
  if (authMode === "apikey") return "API key";
  return "Unknown";
}

function authSourceLabel(authSource?: string) {
  if (authSource === "host_oauth") return "sky10 OAuth";
  if (authSource === "cli_managed") return "Codex CLI";
  return "Unknown source";
}

export default function SettingsCodex() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const audience = parseAudience(searchParams.get("audience"));
  const {
    data: status,
    error: statusError,
    loading,
    refetch,
  } = useRPC(() => codex.status(), [], {
    live: CODEX_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });

  const [actionError, setActionError] = useState<string | null>(null);
  const [actionMessage, setActionMessage] = useState<string | null>(null);
  const [authorizationInput, setAuthorizationInput] = useState("");
  const [busy, setBusy] = useState<"connect" | "complete" | "cancel" | "logout" | null>(null);
  const [redirectOnLinked, setRedirectOnLinked] = useState(false);

  const pending = status?.pending_login ?? null;
  const authLabel = authLabelForStatus(status?.auth_mode, status?.auth_label);
  const sourceLabel = authSourceLabel(status?.auth_source);
  const backHref = audience ? `/start/setup?audience=${audience}` : "/settings";
  const continueHref = "/codex";
  const linkedWithChatGPT = status?.linked && status?.auth_mode === "chatgpt" && status?.auth_source === "host_oauth";

  useEffect(() => {
    if (!redirectOnLinked) return;
    if (!status?.linked || status.auth_source !== "host_oauth" || pending) return;
    navigate("/codex", { replace: true });
  }, [navigate, pending, redirectOnLinked, status?.auth_source, status?.linked]);

  const headingDescription = useMemo(() => {
    if (audience === "for_others") {
      return "Link ChatGPT in sky10, then let your published or automated agents reuse that Codex-backed access on this device.";
    }
    if (audience === "for_me") {
      return "Link ChatGPT in sky10 so your first personal agent can use Codex-backed access without asking for an API key.";
    }
    return "Link ChatGPT directly in sky10 so the daemon can broker Codex-backed work through a browser OAuth flow.";
  }, [audience]);

  const handleConnect = useCallback(async () => {
    setBusy("connect");
    setActionError(null);
    setActionMessage(null);
    setRedirectOnLinked(true);

    const popup = openOAuthPopup(window);
    try {
      const next = await codex.loginStart();
      const verificationURL = next.pending_login?.verification_url;
      const callbackListening = Boolean(next.pending_login?.callback_listening);
      if (verificationURL) {
        navigateOAuthPopup(window, popup, verificationURL);
      } else {
        closeOAuthPopup(popup);
      }

      setActionMessage(
        callbackListening
          ? "Opened ChatGPT sign-in. sky10 is listening for the localhost callback and should finish linking automatically."
          : "Opened ChatGPT sign-in. Paste the final redirect URL or authorization code below after the browser redirects back."
      );
      refetch({ background: true });
    } catch (error: unknown) {
      closeOAuthPopup(popup);
      setActionError(error instanceof Error ? error.message : "Could not start ChatGPT login");
    } finally {
      setBusy(null);
    }
  }, [refetch]);

  const handleComplete = useCallback(async () => {
    const input = authorizationInput.trim();
    if (!input) {
      setActionError("Paste the final redirect URL or authorization code first.");
      return;
    }

    setBusy("complete");
    setActionError(null);
    setActionMessage(null);
    setRedirectOnLinked(true);
    try {
      const next = await codex.loginComplete({ authorization_input: input });
      setAuthorizationInput("");
      setActionMessage("Linked ChatGPT with sky10.");
      if (next.linked && next.auth_source === "host_oauth") {
        navigate("/codex", { replace: true });
        return;
      }
      refetch({ background: true });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Could not finish ChatGPT login");
    } finally {
      setBusy(null);
    }
  }, [authorizationInput, refetch]);

  const handleCancel = useCallback(async () => {
    setBusy("cancel");
    setActionError(null);
    setActionMessage(null);
    setRedirectOnLinked(false);
    try {
      await codex.loginCancel();
      setAuthorizationInput("");
      setActionMessage("Cancelled the pending ChatGPT login.");
      refetch({ background: true });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Could not cancel ChatGPT login");
    } finally {
      setBusy(null);
    }
  }, [refetch]);

  const handleLogout = useCallback(async () => {
    setBusy("logout");
    setActionError(null);
    setActionMessage(null);
    setRedirectOnLinked(false);
    try {
      await codex.logout();
      setAuthorizationInput("");
      setActionMessage("Removed the saved ChatGPT link from this device.");
      refetch({ background: true });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Could not remove ChatGPT link");
    } finally {
      setBusy(null);
    }
  }, [refetch]);

  return (
    <div className="mx-auto max-w-5xl space-y-10 p-12">
      <PageHeader
        eyebrow="ChatGPT"
        title="Connect Codex"
        description={headingDescription}
        actions={(
          <>
            <Link
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-secondary transition-colors hover:text-on-surface"
              to={backHref}
            >
              <Icon className="text-base" name="arrow_back" />
              {audience ? "Back to setup" : "Back to Settings"}
            </Link>
            {linkedWithChatGPT && (
              <Link
                className="inline-flex items-center gap-2 rounded-full bg-primary px-4 py-2 text-sm font-semibold text-on-primary shadow-lg transition-colors hover:bg-primary/90"
                to={continueHref}
              >
                Open Codex
                <Icon className="text-base" name="arrow_forward" />
              </Link>
            )}
          </>
        )}
      />

      <section className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        <div className="flex flex-col gap-6">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div className="space-y-3">
              <div className="flex items-center gap-3">
                <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-primary/10 text-primary">
                  <Icon className="text-2xl" name="chat" />
                </div>
                <div>
                  <h2 className="text-2xl font-semibold text-on-surface">
                    ChatGPT-backed Codex access
                  </h2>
                  <p className="text-sm text-secondary">
                    sky10 can now own the browser OAuth flow directly, then keep the linked account local to this device.
                  </p>
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge tone="success">
                  sky10 OAuth ready
                </StatusBadge>
                {pending ? (
                  <StatusBadge pulse tone="processing">
                    {pending.callback_listening ? "Waiting for browser callback" : "Manual completion required"}
                  </StatusBadge>
                ) : status?.linked ? (
                  <StatusBadge tone="success">
                    Linked via {authLabel}
                  </StatusBadge>
                ) : (
                  <StatusBadge tone="neutral">
                    Not linked
                  </StatusBadge>
                )}
                {status?.auth_source === "cli_managed" && (
                  <StatusBadge tone="neutral">
                    Legacy CLI session detected
                  </StatusBadge>
                )}
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
              {!pending && (
                <button
                  className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-colors hover:bg-primary/90 disabled:opacity-50"
                  disabled={busy !== null}
                  onClick={handleConnect}
                  type="button"
                >
                  <Icon className="text-base" name="link" />
                  {status?.linked ? "Reconnect ChatGPT" : "Connect ChatGPT"}
                </button>
              )}
              {pending && (
                <button
                  className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container disabled:opacity-50"
                  disabled={busy !== null}
                  onClick={handleCancel}
                  type="button"
                >
                  <Icon className="text-base" name="close" />
                  Cancel
                </button>
              )}
              {status?.linked && (
                <button
                  className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container disabled:opacity-50"
                  disabled={busy !== null}
                  onClick={handleLogout}
                  type="button"
                >
                  <Icon className="text-base" name="logout" />
                  Disconnect
                </button>
              )}
            </div>
          </div>

          {(actionError || statusError || status?.last_error) && (
            <div className="rounded-2xl border border-error/20 bg-error-container/20 px-4 py-3 text-sm text-error">
              {actionError || statusError || status?.last_error}
            </div>
          )}

          {actionMessage && (
            <div className="rounded-2xl border border-primary/20 bg-primary/10 px-4 py-3 text-sm text-on-surface">
              {actionMessage}
            </div>
          )}

          {pending ? (
            <div className="grid gap-4 lg:grid-cols-[minmax(0,1.4fr)_minmax(18rem,0.8fr)]">
              <div className="rounded-2xl border border-outline-variant/10 bg-surface p-6">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Finish login
                </p>
                <h3 className="mt-3 text-lg font-semibold text-on-surface">
                  Browser sign-in is in progress
                </h3>
                <p className="mt-2 text-sm text-secondary">
                  {pending.callback_listening
                    ? "sky10 is listening for the localhost callback. If the browser reaches it, this page should update on its own."
                    : "sky10 could not bind the localhost callback on this device, so you need to finish the exchange manually."}
                </p>

                <div className="mt-4 flex flex-wrap items-center gap-3">
                  <a
                    className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
                    href={pending.verification_url}
                    rel="noreferrer"
                    target="_blank"
                  >
                    <Icon className="text-base" name="open_in_new" />
                    Open browser
                  </a>
                  {pending.redirect_uri && (
                    <StatusBadge tone="neutral">
                      Redirects to localhost
                    </StatusBadge>
                  )}
                </div>

                <label className="mt-6 block">
                  <span className="text-sm font-semibold text-on-surface">
                    Paste the final redirect URL or raw authorization code
                  </span>
                  <textarea
                    className="mt-3 min-h-28 w-full rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary/30"
                    onChange={(event) => setAuthorizationInput(event.target.value)}
                    placeholder="http://localhost:1455/auth/callback?code=...&state=..."
                    value={authorizationInput}
                  />
                </label>
                <p className="mt-3 text-sm text-secondary">
                  If the browser shows a localhost error page, copy the full URL from the address bar and paste it here.
                </p>

                <div className="mt-4 flex flex-wrap items-center gap-3">
                  <button
                    className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-colors hover:bg-primary/90 disabled:opacity-50"
                    disabled={busy !== null || authorizationInput.trim() === ""}
                    onClick={handleComplete}
                    type="button"
                  >
                    <Icon className="text-base" name="check_circle" />
                    Finish linking
                  </button>
                  <button
                    className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-5 py-2.5 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container disabled:opacity-50"
                    disabled={busy !== null}
                    onClick={handleCancel}
                    type="button"
                  >
                    <Icon className="text-base" name="close" />
                    Cancel
                  </button>
                </div>
              </div>

              <div className="rounded-2xl border border-outline-variant/10 bg-surface p-6 text-sm text-secondary">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Session
                </p>
                <p className="mt-3">
                  Started {timeAgo(pending.started_at)}
                </p>
                <p className="mt-1">
                  Expires {new Date(pending.expires_at).toLocaleTimeString()}
                </p>
                {pending.redirect_uri && (
                  <p className="mt-4 break-all">
                    Callback URI: <span className="font-medium text-on-surface">{pending.redirect_uri}</span>
                  </p>
                )}
                <p className="mt-4">
                  Mode: <span className="font-medium text-on-surface">{pending.mode || "oauth"}</span>
                </p>
              </div>
            </div>
          ) : (
            <div className="rounded-2xl border border-outline-variant/10 bg-surface p-6 text-sm text-secondary">
              {loading ? (
                <p>Checking the local ChatGPT/Codex link state.</p>
              ) : status?.linked ? (
                <div className="space-y-2">
                  <p>
                    This device is linked via <span className="font-semibold text-on-surface">{authLabel}</span> and managed by{" "}
                    <span className="font-semibold text-on-surface">{sourceLabel}</span>.
                  </p>
                  {status.email && (
                    <p>
                      Account: <span className="font-semibold text-on-surface">{status.email}</span>
                    </p>
                  )}
                  {status.account_id && (
                    <p>
                      ChatGPT account id: <span className="font-mono text-xs text-on-surface">{status.account_id}</span>
                    </p>
                  )}
                  {status.auth_source === "cli_managed" && status.bin_path && (
                    <p>
                      Legacy CLI path: <span className="font-mono text-xs text-on-surface">{status.bin_path}</span>
                    </p>
                  )}
                  {status.auth_source === "cli_managed" && (
                    <p>
                      Reconnect here when you want sky10 to take over token storage and refresh instead of relying on the Codex CLI.
                    </p>
                  )}
                </div>
              ) : (
                <p>
                  Start the browser sign-in to link ChatGPT directly in sky10. No API key or visible Codex CLI setup is required for this flow.
                </p>
              )}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
