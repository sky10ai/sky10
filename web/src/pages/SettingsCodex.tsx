import { useCallback, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { StatusBadge } from "../components/StatusBadge";
import { CODEX_EVENT_TYPES } from "../lib/events";
import { codex } from "../lib/rpc";
import { timeAgo, useRPC } from "../lib/useRPC";

type AgentAudience = "for_me" | "for_others";

function parseAudience(value: string | null): AgentAudience | null {
  if (value === "for_me" || value === "for_others") {
    return value;
  }
  return null;
}

export default function SettingsCodex() {
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
  const [busy, setBusy] = useState<"connect" | "cancel" | "logout" | null>(null);

  const pending = status?.pending_login ?? null;
  const authLabel = status?.auth_label || (status?.auth_mode === "chatgpt" ? "ChatGPT" : status?.auth_mode === "apikey" ? "API key" : "Unknown");
  const backHref = audience ? `/start/setup?audience=${audience}` : "/settings";
  const continueHref = audience ? `/ai?audience=${audience}` : "/ai";
  const linkedWithChatGPT = status?.linked && status?.auth_mode === "chatgpt";

  const headingDescription = useMemo(() => {
    if (audience === "for_others") {
      return "Use the local Codex CLI device flow to link ChatGPT-backed Codex access before you publish or automate an agent for other people.";
    }
    if (audience === "for_me") {
      return "Use the local Codex CLI device flow to link ChatGPT-backed Codex access for your first personal agent.";
    }
    return "Link the local Codex CLI with ChatGPT so sky10 can broker Codex-backed work without asking for an API key first.";
  }, [audience]);

  const handleConnect = useCallback(async () => {
    setBusy("connect");
    setActionError(null);
    setActionMessage(null);

    const popup = window.open("", "_blank", "noopener,noreferrer");
    try {
      const next = await codex.loginStart();
      const verificationURL = next.pending_login?.verification_url;
      if (verificationURL) {
        if (popup) {
          popup.location.href = verificationURL;
        } else {
          window.open(verificationURL, "_blank", "noopener,noreferrer");
        }
        setActionMessage("Opened the Codex device-auth page. Enter the code below to finish linking ChatGPT.");
      } else if (popup) {
        popup.close();
      }
      refetch({ background: true });
    } catch (error: unknown) {
      if (popup) popup.close();
      setActionError(error instanceof Error ? error.message : "Could not start Codex login");
    } finally {
      setBusy(null);
    }
  }, [refetch]);

  const handleCancel = useCallback(async () => {
    setBusy("cancel");
    setActionError(null);
    setActionMessage(null);
    try {
      await codex.loginCancel();
      setActionMessage("Cancelled the pending Codex login.");
      refetch({ background: true });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Could not cancel Codex login");
    } finally {
      setBusy(null);
    }
  }, [refetch]);

  const handleLogout = useCallback(async () => {
    setBusy("logout");
    setActionError(null);
    setActionMessage(null);
    try {
      await codex.logout();
      setActionMessage("Removed the local Codex login.");
      refetch({ background: true });
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : "Could not remove Codex login");
    } finally {
      setBusy(null);
    }
  }, [refetch]);

  const handleCopyCode = useCallback(async () => {
    if (!pending?.user_code) return;
    await navigator.clipboard.writeText(pending.user_code);
    setActionMessage("Copied the device code.");
  }, [pending?.user_code]);

  return (
    <div className="p-12 max-w-5xl mx-auto space-y-10">
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
                Continue
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
                    sky10 uses the local `codex` CLI device flow, then keeps checking the linked state through the daemon.
                  </p>
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge tone={status?.installed ? "success" : "neutral"}>
                  {status?.installed ? "Codex CLI found" : "Codex CLI missing"}
                </StatusBadge>
                {pending ? (
                  <StatusBadge pulse tone="processing">
                    Waiting for device approval
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
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-3">
              {!pending && (
                <button
                  className="inline-flex items-center gap-2 rounded-full bg-primary px-5 py-2.5 text-sm font-semibold text-on-primary shadow-lg transition-colors hover:bg-primary/90 disabled:opacity-50"
                  disabled={!status?.installed || busy !== null}
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
            <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_auto]">
              <div className="rounded-2xl border border-outline-variant/10 bg-surface p-6">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Device Code
                </p>
                <div className="mt-3 flex flex-wrap items-center gap-3">
                  <code className="rounded-2xl bg-surface-container px-4 py-3 text-2xl font-semibold tracking-[0.24em] text-on-surface">
                    {pending.user_code}
                  </code>
                  <button
                    className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
                    onClick={handleCopyCode}
                    type="button"
                  >
                    <Icon className="text-base" name="content_copy" />
                    Copy code
                  </button>
                  <a
                    className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 px-4 py-2 text-sm font-semibold text-on-surface transition-colors hover:bg-surface-container"
                    href={pending.verification_url}
                    rel="noreferrer"
                    target="_blank"
                  >
                    <Icon className="text-base" name="open_in_new" />
                    Open browser
                  </a>
                </div>
                <p className="mt-4 text-sm text-secondary">
                  Sign in to ChatGPT in the browser, enter the code, and this page will refresh automatically once Codex finishes linking.
                </p>
              </div>

              <div className="rounded-2xl border border-outline-variant/10 bg-surface p-6 text-sm text-secondary">
                <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
                  Session
                </p>
                <p className="mt-3">Started {timeAgo(pending.started_at)}</p>
                <p className="mt-1">Expires {new Date(pending.expires_at).toLocaleTimeString()}</p>
              </div>
            </div>
          ) : (
            <div className="rounded-2xl border border-outline-variant/10 bg-surface p-6 text-sm text-secondary">
              {!status?.installed && !loading ? (
                <p>
                  `codex` is not installed or not visible to the daemon. Install the Codex CLI first, then reload this page.
                </p>
              ) : status?.linked ? (
                <p>
                  This device is currently linked via <span className="font-semibold text-on-surface">{authLabel}</span>.
                  {status.bin_path ? ` sky10 found the CLI at ${status.bin_path}.` : ""}
                </p>
              ) : (
                <p>
                  Start the device flow to open ChatGPT in a browser tab and link the local Codex CLI without pasting an API key.
                </p>
              )}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
