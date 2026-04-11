import { useEffect, useState } from "react";
import { EmptyState } from "../components/EmptyState";
import { Icon } from "../components/Icon";
import { PageHeader } from "../components/PageHeader";
import { RelativeTime } from "../components/RelativeTime";
import { StatusBadge } from "../components/StatusBadge";
import { AGENT_EVENT_TYPES } from "../lib/events";
import {
  agent,
  type MailboxEvent,
  type MailboxRecord,
  type MailboxView,
} from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

type TabKey = "inbox" | "approvals" | "queue" | "outbox" | "failed" | "sent";

const mailboxLiveEvents = [
  ...AGENT_EVENT_TYPES,
  "agent.mailbox.updated",
  "agent.mailbox.claimed",
  "agent.mailbox.completed",
] as const;

const tabLabels: Record<TabKey, string> = {
  inbox: "Inbox",
  approvals: "Approvals",
  queue: "Queue",
  outbox: "Outbox",
  failed: "Failed",
  sent: "Sent",
};

function stateTone(state: string): "danger" | "live" | "neutral" | "processing" | "success" {
  switch (state) {
    case "failed":
    case "dead_lettered":
    case "rejected":
      return "danger";
    case "completed":
    case "approved":
      return "success";
    case "claimed":
    case "delivered":
      return "processing";
    default:
      return "neutral";
  }
}

function itemIcon(kind: string): string {
  switch (kind) {
    case "approval_request":
      return "approval";
    case "task_request":
      return "assignment";
    case "payment_required":
    case "payment_proof":
    case "receipt":
      return "payments";
    case "result":
      return "task_alt";
    case "message":
      return "mail";
    default:
      return "inbox";
  }
}

function eventIcon(type: string): string {
  switch (type) {
    case "delivery_attempted":
      return "send";
    case "handed_off":
      return "outbox";
    case "approved":
      return "check_circle";
    case "rejected":
      return "cancel";
    case "claimed":
      return "bolt";
    case "completed":
      return "task_alt";
    case "delivery_failed":
      return "error";
    case "delivered":
    case "seen":
      return "mark_email_read";
    default:
      return "schedule";
  }
}

function recordTitle(record: MailboxRecord): string {
  const payload = payloadObject(record);
  if (record.item.kind === "approval_request" && typeof payload.summary === "string") {
    return payload.summary;
  }
  if (record.item.kind === "task_request" && typeof payload.summary === "string") {
    return payload.summary;
  }
  if (record.item.kind === "task_request" && typeof payload.method === "string") {
    return payload.method;
  }
  if (record.item.kind === "payment_required" && typeof payload.amount === "string" && typeof payload.asset === "string") {
    return `${payload.amount} ${payload.asset}`;
  }
  if (record.item.kind === "message") {
    const text = extractMessageText(record);
    if (text) return text;
  }
  return record.item.kind.replaceAll("_", " ");
}

function recordSubtitle(record: MailboxRecord): string {
  const payload = payloadObject(record);
  if (record.item.kind === "approval_request" && typeof payload.action === "string") {
    return payload.action;
  }
  if (record.item.kind === "payment_required" && typeof payload.chain === "string") {
    return payload.chain;
  }
  if (record.item.kind === "task_request" && record.item.target_skill) {
    return `skill:${record.item.target_skill}`;
  }
  if (record.item.reply_to) {
    return `reply to ${record.item.reply_to}`;
  }
  return `${record.item.from.id} -> ${record.item.to?.id ?? "queue"}`;
}

function payloadObject(record: MailboxRecord): Record<string, unknown> {
  if (!record.item.payload_inline || typeof record.item.payload_inline !== "object") {
    return {};
  }
  return record.item.payload_inline as Record<string, unknown>;
}

function extractMessageText(record: MailboxRecord): string {
  const payload = payloadObject(record);
  if ("text" in payload && typeof payload.text === "string") {
    return payload.text;
  }
  if ("content" in payload && payload.content && typeof payload.content === "object") {
    const content = payload.content as Record<string, unknown>;
    if ("text" in content && typeof content.text === "string") {
      return content.text;
    }
  }
  return "";
}

function payloadText(record: MailboxRecord): string {
  if (record.item.payload_inline === undefined) {
    return "{}";
  }
  return JSON.stringify(record.item.payload_inline, null, 2);
}

function latestTimestamp(record: MailboxRecord): string {
  const lastEvent = record.events[record.events.length - 1];
  return lastEvent?.timestamp || record.item.created_at;
}

function latestEventOfType(record: MailboxRecord, type: string): MailboxEvent | undefined {
  for (let i = record.events.length - 1; i >= 0; i -= 1) {
    const event = record.events[i];
    if (event && event.type === type) {
      return event;
    }
  }
  return undefined;
}

function deliveryAttempts(record: MailboxRecord): MailboxEvent[] {
  return record.events.filter((event) => event.type === "delivery_attempted");
}

function relatedRecords(record: MailboxRecord, records: MailboxRecord[]): MailboxRecord[] {
  return records.filter((candidate) => {
    if (candidate.item.id === record.item.id) return false;
    if (record.item.request_id && candidate.item.request_id === record.item.request_id) return true;
    if (candidate.item.reply_to === record.item.id) return true;
    if (record.item.reply_to && candidate.item.id === record.item.reply_to) return true;
    return false;
  });
}

function debugText(value: unknown): string {
  return JSON.stringify(value, null, 2);
}

function viewParams(
  view?: MailboxView,
  filters?: { queue?: string; requestID?: string; replyTo?: string },
) {
  if (!view) return undefined;
  return {
    principal_id: view.principal.id,
    principal_kind: view.principal.kind,
    queue: filters?.queue?.trim() || undefined,
    request_id: filters?.requestID?.trim() || undefined,
    reply_to: filters?.replyTo?.trim() || undefined,
  };
}

function isSenderForView(record: MailboxRecord, view?: MailboxView): boolean {
  return Boolean(view && record.item.from.id === view.principal.id);
}

function isRecipientForView(record: MailboxRecord, view?: MailboxView): boolean {
  return Boolean(view && record.item.to?.id === view.principal.id);
}

function queueEligibleForView(record: MailboxRecord, view?: MailboxView): boolean {
  if (!view || view.role !== "agent") return false;
  if (record.claim?.holder === view.principal.id) return true;
  if (!record.item.target_skill) return false;
  return (view.skills ?? []).includes(record.item.target_skill);
}

function canApproveRecord(record: MailboxRecord, view?: MailboxView): boolean {
  return record.item.kind === "approval_request" &&
    record.state !== "approved" &&
    record.state !== "rejected" &&
    isRecipientForView(record, view);
}

function canClaimRecord(record: MailboxRecord, view?: MailboxView): boolean {
  return record.item.kind === "task_request" &&
    !record.claim &&
    queueEligibleForView(record, view);
}

function canReleaseRecord(record: MailboxRecord, view?: MailboxView): boolean {
  return Boolean(record.claim && view && record.claim.holder === view.principal.id);
}

function canCompleteRecord(record: MailboxRecord, view?: MailboxView): boolean {
  if (!view || record.item.kind !== "task_request" || record.state === "completed") {
    return false;
  }
  if (record.claim) {
    return record.claim.holder === view.principal.id;
  }
  return isRecipientForView(record, view) && view.role === "agent";
}

function canRetryRecord(record: MailboxRecord, view?: MailboxView): boolean {
  if (!view || (record.state !== "queued" && record.state !== "failed" && record.state !== "dead_lettered")) {
    return false;
  }
  return isSenderForView(record, view) || isRecipientForView(record, view) || record.claim?.holder === view.principal.id;
}

function canAckRecord(record: MailboxRecord, view?: MailboxView): boolean {
  return record.state === "delivered" && isRecipientForView(record, view);
}

export default function Mailbox() {
  const views = useRPC(() => agent.mailbox.views(), [], {
    live: AGENT_EVENT_TYPES,
    refreshIntervalMs: 5_000,
  });
  const [selectedViewId, setSelectedViewId] = useState<string | null>(null);
  const [requestFilter, setRequestFilter] = useState("");
  const [replyToFilter, setReplyToFilter] = useState("");
  const [queueFilter, setQueueFilter] = useState("");

  const availableViews = views.data?.views ?? [];
  const currentView =
    availableViews.find((view) => view.view_id === selectedViewId) ??
    availableViews.find((view) => view.view_id === views.data?.default_view_id) ??
    availableViews[0];
  const params = viewParams(currentView, {
    queue: queueFilter,
    requestID: requestFilter,
    replyTo: replyToFilter,
  });

  const inbox = useRPC(
    async () => currentView ? agent.mailbox.listInbox(params) : { items: [], count: 0 },
    [currentView?.view_id, queueFilter, replyToFilter, requestFilter],
    {
      live: mailboxLiveEvents,
      refreshIntervalMs: 5_000,
    },
  );
  const outbox = useRPC(
    async () => currentView ? agent.mailbox.listOutbox(params) : { items: [], count: 0 },
    [currentView?.view_id, queueFilter, replyToFilter, requestFilter],
    {
      live: mailboxLiveEvents,
      refreshIntervalMs: 5_000,
    },
  );
  const queue = useRPC(
    async () => currentView ? agent.mailbox.listQueue(params) : { items: [], count: 0 },
    [currentView?.view_id, queueFilter, replyToFilter, requestFilter],
    {
      live: mailboxLiveEvents,
      refreshIntervalMs: 5_000,
    },
  );
  const failed = useRPC(
    async () => currentView ? agent.mailbox.listFailed(params) : { items: [], count: 0 },
    [currentView?.view_id, queueFilter, replyToFilter, requestFilter],
    {
      live: mailboxLiveEvents,
      refreshIntervalMs: 5_000,
    },
  );
  const sent = useRPC(
    async () => currentView ? agent.mailbox.listSent(params) : { items: [], count: 0 },
    [currentView?.view_id, queueFilter, replyToFilter, requestFilter],
    {
      live: mailboxLiveEvents,
      refreshIntervalMs: 5_000,
    },
  );

  const [tab, setTab] = useState<TabKey>("inbox");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyAction, setBusyAction] = useState<string | null>(null);

  const inboxItems = inbox.data?.items ?? [];
  const outboxItems = outbox.data?.items ?? [];
  const queueItems = queue.data?.items ?? [];
  const failedItems = failed.data?.items ?? [];
  const sentItems = sent.data?.items ?? [];
  const approvalItems = inboxItems.filter((item) => item.item.kind === "approval_request");
  const availableTabs: TabKey[] =
    currentView?.role === "agent"
      ? ["inbox", "approvals", "queue", "outbox", "failed", "sent"]
      : ["inbox", "approvals", "outbox", "failed", "sent"];

  const allRecords = new Map<string, MailboxRecord>();
  for (const list of [inboxItems, outboxItems, queueItems, failedItems, sentItems]) {
    for (const record of list) {
      allRecords.set(record.item.id, record);
    }
  }

  const tabItems: Record<TabKey, MailboxRecord[]> = {
    inbox: inboxItems,
    approvals: approvalItems,
    queue: queueItems,
    outbox: outboxItems,
    failed: failedItems,
    sent: sentItems,
  };

  const currentItems = tabItems[tab];
  const selected = (selectedId ? allRecords.get(selectedId) : undefined) ?? currentItems[0];
  const selectedAttemptEvents = selected ? deliveryAttempts(selected) : [];
  const lastDeliveryFailure = selected ? latestEventOfType(selected, "delivery_failed") : undefined;
  const lastHandoff = selected ? latestEventOfType(selected, "handed_off") : undefined;
  const related = selected ? relatedRecords(selected, Array.from(allRecords.values())) : [];
  const filtersActive = Boolean(requestFilter.trim() || replyToFilter.trim() || queueFilter.trim());

  useEffect(() => {
    if (!currentView) return;
    if (selectedViewId !== currentView.view_id) {
      setSelectedViewId(currentView.view_id);
    }
  }, [currentView, selectedViewId]);

  useEffect(() => {
    if (!availableTabs.includes(tab)) {
      setTab(availableTabs[0] ?? "inbox");
    }
  }, [availableTabs, tab]);

  useEffect(() => {
    if (selected && selected.item.id !== selectedId) {
      setSelectedId(selected.item.id);
      return;
    }
    if (!selected && currentItems.length === 0) {
      setSelectedId(null);
    }
  }, [currentItems, selected, selectedId]);

  function refetchAll() {
    views.refetch({ background: true });
    inbox.refetch({ background: true });
    outbox.refetch({ background: true });
    queue.refetch({ background: true });
    failed.refetch({ background: true });
    sent.refetch({ background: true });
  }

  async function runAction(key: string, fn: () => Promise<void>) {
    setBusyAction(key);
    setActionError(null);
    try {
      await fn();
      refetchAll();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : "Mailbox action failed");
    } finally {
      setBusyAction(null);
    }
  }

  const isLoading =
    views.loading ||
    inbox.loading ||
    outbox.loading ||
    queue.loading ||
    failed.loading ||
    sent.loading;

  return (
    <section className="mx-auto flex w-full max-w-7xl flex-1 flex-col gap-8 p-12">
      <PageHeader
        eyebrow="Async Control Plane"
        title={currentView ? `Mailbox: ${currentView.label}` : "Mailbox"}
        description={
          currentView
            ? `Durable mailbox state projected for ${currentView.label}, with actions and queue visibility scoped to that principal.`
            : "Durable mailbox state projected per principal instead of one ambiguous global inbox."
        }
        actions={
          <>
            <StatusBadge pulse tone="live">
              Live
            </StatusBadge>
            <button
              onClick={refetchAll}
              className="inline-flex items-center gap-2 rounded-full border border-outline-variant/20 bg-surface-container-lowest px-4 py-2 text-xs font-semibold uppercase tracking-[0.16em] text-secondary hover:bg-surface-container-low"
            >
              <Icon name="refresh" className="text-sm" />
              Refresh
            </button>
          </>
        }
      />

      <div className="flex flex-wrap gap-2">
        {availableViews.map((view) => (
          <button
            key={view.view_id}
            onClick={() => {
              setSelectedViewId(view.view_id);
              setSelectedId(null);
              setActionError(null);
            }}
            className={`inline-flex items-center gap-2 rounded-full px-4 py-2 text-xs font-bold uppercase tracking-[0.16em] transition-colors ${
              currentView?.view_id === view.view_id
                ? "bg-primary text-on-primary"
                : "bg-surface-container-lowest text-secondary hover:bg-surface-container-low"
            }`}
          >
            <span>{view.label}</span>
            <span className="rounded-full bg-black/10 px-2 py-0.5 text-[10px]">
              {view.role}
            </span>
          </button>
        ))}
      </div>

      <div className="grid gap-3 rounded-3xl border border-outline-variant/10 bg-surface-container-lowest p-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
        <label className="space-y-2">
          <span className="text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
            Request ID
          </span>
          <input
            value={requestFilter}
            onChange={(event) => setRequestFilter(event.target.value)}
            placeholder="req-123"
            className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary"
          />
        </label>
        <label className="space-y-2">
          <span className="text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
            Reply To
          </span>
          <input
            value={replyToFilter}
            onChange={(event) => setReplyToFilter(event.target.value)}
            placeholder="mailbox-item-id"
            className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary"
          />
        </label>
        <label className="space-y-2">
          <span className="text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
            Queue
          </span>
          <input
            value={queueFilter}
            onChange={(event) => setQueueFilter(event.target.value)}
            placeholder="skill:research"
            className="w-full rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 text-sm text-on-surface outline-none transition-colors placeholder:text-outline focus:border-primary"
          />
        </label>
        <div className="flex items-end">
          <button
            onClick={() => {
              setRequestFilter("");
              setReplyToFilter("");
              setQueueFilter("");
            }}
            disabled={!filtersActive}
            className="inline-flex w-full items-center justify-center gap-2 rounded-2xl border border-outline-variant/20 bg-surface-container-low px-4 py-3 text-xs font-bold uppercase tracking-[0.16em] text-secondary transition-colors hover:bg-surface-container disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Icon name="filter_alt_off" className="text-sm" />
            Clear Filters
          </button>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-4">
        <SummaryCard label="Inbox" value={inboxItems.length} icon="inbox" />
        <SummaryCard label="Approvals" value={approvalItems.length} icon="approval" />
        <SummaryCard label={currentView?.role === "agent" ? "Queue" : "Failed"} value={currentView?.role === "agent" ? queueItems.length : failedItems.length} icon={currentView?.role === "agent" ? "assignment" : "error"} />
        <SummaryCard label="Outbox" value={outboxItems.length} icon="outbox" />
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.1fr)_minmax(22rem,0.9fr)]">
        <div className="space-y-4">
          <div className="flex flex-wrap gap-2">
            {availableTabs.map((key) => (
              <button
                key={key}
                onClick={() => setTab(key)}
                className={`inline-flex items-center gap-2 rounded-full px-4 py-2 text-xs font-bold uppercase tracking-[0.16em] transition-colors ${
                  tab === key
                    ? "bg-primary text-on-primary"
                    : "bg-surface-container-lowest text-secondary hover:bg-surface-container-low"
                }`}
              >
                <span>{tabLabels[key]}</span>
                <span className="rounded-full bg-black/10 px-2 py-0.5 text-[10px]">
                  {tabItems[key].length}
                </span>
              </button>
            ))}
          </div>

          {actionError && (
            <div className="rounded-2xl border border-error/20 bg-error-container/20 px-4 py-3 text-sm text-error">
              {actionError}
            </div>
          )}

          <div className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
            <div className="border-b border-outline-variant/10 px-6 py-4">
              <div className="flex items-center justify-between">
                <h2 className="text-lg font-semibold text-on-surface">
                  {tab === "approvals" ? "Approvals Center" : tabLabels[tab]}
                </h2>
                <div className="flex items-center gap-2">
                  {filtersActive && (
                    <StatusBadge tone="processing">
                      filtered
                    </StatusBadge>
                  )}
                  <StatusBadge tone="neutral">
                    {tabItems[tab].length} items
                  </StatusBadge>
                </div>
              </div>
            </div>

            <div className="max-h-[42rem] overflow-y-auto">
              {!isLoading && currentItems.length === 0 ? (
                <div className="p-6">
                  <EmptyState
                    icon={tab === "queue" ? "assignment" : "inbox"}
                    title={`No ${tabLabels[tab].toLowerCase()} items`}
                    description={currentView ? `Mailbox state for ${currentView.label} will appear here once that principal participates in durable async work.` : "Mailbox state will appear here once agents or humans create durable async work."}
                  />
                </div>
              ) : (
                <div className="divide-y divide-outline-variant/10">
                  {currentItems.map((record) => {
                    const isSelected = selected?.item.id === record.item.id;
                    return (
                      <button
                        key={record.item.id}
                        onClick={() => setSelectedId(record.item.id)}
                        className={`flex w-full items-start gap-4 px-6 py-5 text-left transition-colors ${
                          isSelected ? "bg-primary/5" : "hover:bg-surface-container-low"
                        }`}
                      >
                        <div className="mt-1 flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl bg-surface-container-high text-primary">
                          <Icon name={itemIcon(record.item.kind)} className="text-2xl" />
                        </div>
                        <div className="min-w-0 flex-1 space-y-2">
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0">
                              <p className="truncate text-sm font-semibold text-on-surface">
                                {recordTitle(record)}
                              </p>
                              <p className="truncate text-xs text-secondary">
                                {recordSubtitle(record)}
                              </p>
                            </div>
                            <StatusBadge tone={stateTone(record.state)}>
                              {record.state}
                            </StatusBadge>
                          </div>
                          <div className="flex flex-wrap items-center gap-3 text-[11px] text-outline">
                            <span>{record.item.kind}</span>
                            <span>{record.item.from.id}</span>
                            <RelativeTime value={latestTimestamp(record)} />
                          </div>
                        </div>
                      </button>
                    );
                  })}
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="rounded-3xl border border-outline-variant/10 bg-surface-container-lowest shadow-sm">
          {selected ? (
            <div className="flex h-full flex-col">
              <div className="border-b border-outline-variant/10 px-6 py-5">
                <div className="flex items-start justify-between gap-4">
                  <div>
                    <p className="text-xs font-bold uppercase tracking-[0.16em] text-outline">
                      Item Detail
                    </p>
                    <h3 className="mt-2 text-2xl font-semibold text-on-surface">
                      {recordTitle(selected)}
                    </h3>
                    <p className="mt-1 text-sm text-secondary">
                      {recordSubtitle(selected)}
                    </p>
                  </div>
                  <StatusBadge tone={stateTone(selected.state)}>
                    {selected.state}
                  </StatusBadge>
                </div>

                <div className="mt-5 flex flex-wrap gap-2">
                  {canApproveRecord(selected, currentView) && (
                    <>
                      <ActionButton
                        busy={busyAction === "approve"}
                        label="Approve"
                        icon="check_circle"
                        onClick={() =>
                          runAction("approve", async () => {
                            await agent.mailbox.approve({
                              item_id: selected.item.id,
                              actor_id: currentView?.principal.id,
                              actor_kind: currentView?.principal.kind,
                            });
                          })
                        }
                      />
                      <ActionButton
                        busy={busyAction === "reject"}
                        label="Reject"
                        icon="cancel"
                        tone="danger"
                        onClick={() =>
                          runAction("reject", async () => {
                            await agent.mailbox.reject({
                              item_id: selected.item.id,
                              actor_id: currentView?.principal.id,
                              actor_kind: currentView?.principal.kind,
                            });
                          })
                        }
                      />
                    </>
                  )}

                  {canClaimRecord(selected, currentView) && (
                    <ActionButton
                      busy={busyAction === "claim"}
                      label="Claim"
                      icon="bolt"
                      onClick={() =>
                        runAction("claim", async () => {
                          await agent.mailbox.claim({
                            item_id: selected.item.id,
                            actor_id: currentView?.principal.id,
                            actor_kind: currentView?.principal.kind,
                          });
                        })
                      }
                    />
                  )}

                  {canReleaseRecord(selected, currentView) && (
                    <ActionButton
                      busy={busyAction === "release"}
                      label="Release"
                      icon="undo"
                      onClick={() =>
                        runAction("release", async () => {
                          await agent.mailbox.release({
                            item_id: selected.item.id,
                            actor_id: currentView?.principal.id,
                            actor_kind: currentView?.principal.kind,
                            token: selected.claim?.token,
                          });
                        })
                      }
                    />
                  )}

                  {canCompleteRecord(selected, currentView) && (
                    <ActionButton
                      busy={busyAction === "complete"}
                      label="Complete"
                      icon="task_alt"
                      onClick={() =>
                        runAction("complete", async () => {
                          await agent.mailbox.complete({
                            item_id: selected.item.id,
                            actor_id: currentView?.principal.id,
                            actor_kind: currentView?.principal.kind,
                          });
                        })
                      }
                    />
                  )}

                  {canRetryRecord(selected, currentView) && (
                    <ActionButton
                      busy={busyAction === "retry"}
                      label={selected.state === "dead_lettered" ? "Replay" : "Retry"}
                      icon="refresh"
                      onClick={() =>
                        runAction("retry", async () => {
                          await agent.mailbox.retry({
                            item_id: selected.item.id,
                            actor_id: currentView?.principal.id,
                            actor_kind: currentView?.principal.kind,
                          });
                        })
                      }
                    />
                  )}

                  {canAckRecord(selected, currentView) && (
                    <ActionButton
                      busy={busyAction === "ack"}
                      label="Mark Seen"
                      icon="visibility"
                      onClick={() =>
                        runAction("ack", async () => {
                          await agent.mailbox.ack({
                            item_id: selected.item.id,
                            actor_id: currentView?.principal.id,
                            actor_kind: currentView?.principal.kind,
                          });
                        })
                      }
                    />
                  )}
                </div>
              </div>

              <div className="grid gap-6 px-6 py-6">
                <div className="grid gap-4 sm:grid-cols-2">
                  <DetailField label="Request ID" value={selected.item.request_id || "-"} />
                  <DetailField label="Reply To" value={selected.item.reply_to || "-"} />
                  <DetailField label="From" value={selected.item.from.id} />
                  <DetailField label="To" value={selected.item.to?.id || selected.item.target_skill || "-"} />
                  <DetailField label="Created" value={selected.item.created_at} />
                  <DetailField label="Expires" value={selected.item.expires_at || "-"} />
                </div>

                <div className="grid gap-4 sm:grid-cols-3">
                  <DetailField label="Delivery Attempts" value={String(selectedAttemptEvents.length)} />
                  <DetailField label="Last Error" value={lastDeliveryFailure?.error || "-"} />
                  <DetailField
                    label="Last Transport"
                    value={lastHandoff?.meta?.transport || lastDeliveryFailure?.meta?.transport || selectedAttemptEvents[selectedAttemptEvents.length - 1]?.meta?.transport || "-"}
                  />
                </div>

                {selected.claim && (
                  <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4">
                    <div className="mb-2 flex items-center gap-2">
                      <Icon name="bolt" className="text-primary" />
                      <p className="text-sm font-semibold text-on-surface">Active Claim</p>
                    </div>
                    <div className="grid gap-2 text-sm text-secondary">
                      <div>Holder: {selected.claim.holder}</div>
                      <div>Queue: {selected.claim.queue}</div>
                      <div>Expires: {selected.claim.expires_at}</div>
                    </div>
                  </div>
                )}

                {selected.item.payload_ref && (
                  <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4">
                    <div className="mb-2 flex items-center gap-2">
                      <Icon name="attachment" className="text-primary" />
                      <p className="text-sm font-semibold text-on-surface">Payload Ref</p>
                    </div>
                    <pre className="overflow-auto rounded-xl bg-[#111315] p-3 text-[11px] leading-5 text-[#d2d7dc]">
                      {debugText(selected.item.payload_ref)}
                    </pre>
                  </div>
                )}

                {related.length > 0 && (
                  <div className="space-y-3">
                    <div className="flex items-center gap-2">
                      <Icon name="account_tree" className="text-secondary" />
                      <p className="text-sm font-semibold text-on-surface">Related Items</p>
                    </div>
                    <div className="space-y-2">
                      {related.map((record) => (
                        <button
                          key={record.item.id}
                          onClick={() => setSelectedId(record.item.id)}
                          className="flex w-full items-center justify-between gap-3 rounded-2xl border border-outline-variant/10 bg-surface-container-low px-4 py-3 text-left transition-colors hover:bg-surface-container"
                        >
                          <div className="min-w-0">
                            <p className="truncate text-sm font-semibold text-on-surface">
                              {recordTitle(record)}
                            </p>
                            <p className="truncate text-xs text-secondary">
                              {record.item.request_id || record.item.reply_to || record.item.id}
                            </p>
                          </div>
                          <StatusBadge tone={stateTone(record.state)}>
                            {record.state}
                          </StatusBadge>
                        </button>
                      ))}
                    </div>
                  </div>
                )}

                <div className="space-y-3">
                  <div className="flex items-center gap-2">
                    <Icon name="code" className="text-secondary" />
                    <p className="text-sm font-semibold text-on-surface">Payload</p>
                  </div>
                  <pre className="max-h-64 overflow-auto rounded-2xl bg-[#111315] p-4 text-xs leading-6 text-[#d2d7dc]">
                    {payloadText(selected)}
                  </pre>
                </div>

                <div className="space-y-3">
                  <div className="flex items-center gap-2">
                    <Icon name="timeline" className="text-secondary" />
                    <p className="text-sm font-semibold text-on-surface">Timeline</p>
                  </div>
                  <div className="space-y-3">
                    {selected.events.length === 0 ? (
                      <p className="text-sm text-secondary">No events yet.</p>
                    ) : (
                      selected.events.map((event) => (
                        <TimelineRow key={event.event_id || `${event.type}-${event.timestamp}`} event={event} />
                      ))
                    )}
                  </div>
                </div>

                <details className="rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4">
                  <summary className="flex cursor-pointer list-none items-center gap-2 text-sm font-semibold text-on-surface">
                    <Icon name="bug_report" className="text-secondary" />
                    Debug JSON
                  </summary>
                  <div className="mt-4 space-y-4">
                    <div>
                      <p className="mb-2 text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
                        Record
                      </p>
                      <pre className="overflow-auto rounded-xl bg-[#111315] p-3 text-[11px] leading-5 text-[#d2d7dc]">
                        {debugText(selected)}
                      </pre>
                    </div>
                    {selectedAttemptEvents.length > 0 && (
                      <div>
                        <p className="mb-2 text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
                          Delivery Attempts
                        </p>
                        <pre className="overflow-auto rounded-xl bg-[#111315] p-3 text-[11px] leading-5 text-[#d2d7dc]">
                          {debugText(selectedAttemptEvents)}
                        </pre>
                      </div>
                    )}
                  </div>
                </details>
              </div>
            </div>
          ) : (
            <div className="p-6">
              <EmptyState
                icon="inbox"
                title="Select a mailbox item"
                description="Choose an inbox, outbox, approval, or queue item to inspect its payload and timeline."
              />
            </div>
          )}
        </div>
      </div>
    </section>
  );
}

function SummaryCard({
  icon,
  label,
  value,
}: {
  icon: string;
  label: string;
  value: number;
}) {
  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-lowest px-5 py-4 shadow-sm">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[10px] font-bold uppercase tracking-[0.18em] text-outline">
            {label}
          </p>
          <p className="mt-2 text-3xl font-semibold tracking-tight text-on-surface">
            {value}
          </p>
        </div>
        <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-surface-container-high text-primary">
          <Icon name={icon} className="text-2xl" />
        </div>
      </div>
    </div>
  );
}

function ActionButton({
  busy,
  icon,
  label,
  onClick,
  tone = "primary",
}: {
  busy: boolean;
  icon: string;
  label: string;
  onClick: () => void;
  tone?: "danger" | "primary";
}) {
  const className =
    tone === "danger"
      ? "bg-error text-on-primary"
      : "bg-primary text-on-primary";
  return (
    <button
      onClick={onClick}
      disabled={busy}
      className={`inline-flex items-center gap-2 rounded-full px-4 py-2 text-xs font-bold uppercase tracking-[0.16em] ${className} disabled:cursor-wait disabled:opacity-60`}
    >
      <Icon name={busy ? "progress_activity" : icon} className="text-sm" />
      {busy ? "Working" : label}
    </button>
  );
}

function DetailField({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4">
      <p className="text-[10px] font-bold uppercase tracking-[0.16em] text-outline">
        {label}
      </p>
      <p className="mt-2 break-all text-sm text-on-surface">{value}</p>
    </div>
  );
}

function TimelineRow({ event }: { event: MailboxEvent }) {
  return (
    <div className="flex gap-3 rounded-2xl border border-outline-variant/10 bg-surface-container-low p-4">
      <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-surface-container-high text-primary">
        <Icon name={eventIcon(event.type)} className="text-lg" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <p className="text-sm font-semibold text-on-surface">{event.type.replaceAll("_", " ")}</p>
          {event.timestamp && <RelativeTime className="text-xs text-outline" value={event.timestamp} />}
        </div>
        <p className="mt-1 text-xs text-secondary">Actor: {event.actor.id}</p>
        {event.error && <p className="mt-2 text-xs text-error">{event.error}</p>}
        {event.meta && Object.keys(event.meta).length > 0 && (
          <pre className="mt-3 overflow-auto rounded-xl bg-[#111315] p-3 text-[11px] leading-5 text-[#d2d7dc]">
            {JSON.stringify(event.meta, null, 2)}
          </pre>
        )}
      </div>
    </div>
  );
}
