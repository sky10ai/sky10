import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { identity, skylink } from "../lib/rpc";
import { truncAddr, useRPC } from "../lib/useRPC";

export default function SettingsIdentity() {
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idInfo } = useRPC(() => identity.show(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: deviceData } = useRPC(() => identity.deviceList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const thisDevice = (deviceData?.devices ?? []).find(
    (d) => d.id === deviceData?.this_device,
  );

  return (
    <SettingsPage
      backHref="/settings"
      description="View the local identity, peer ID, and device hostname."
      title="Identity"
      width="narrow"
    >
      <section className="rounded-xl border border-outline-variant/10 bg-surface-container-lowest p-8 shadow-sm">
        <div className="space-y-6">
          <div className="flex items-start justify-between gap-4">
            <div className="space-y-1">
              <h3 className="flex items-center gap-2 text-xl font-semibold">
                <Icon className="text-primary" name="fingerprint" />
                Identity
              </h3>
              <p className="text-sm text-secondary">Local identity.</p>
            </div>
            <span className="rounded-full bg-primary/10 px-3 py-1 text-[10px] font-bold uppercase tracking-widest text-primary">
              Active
            </span>
          </div>
          <div className="space-y-4">
            <div className="space-y-2">
              <label className="text-[10px] font-bold uppercase tracking-wider text-secondary-fixed-dim">
                Identity Address
              </label>
              <div className="group/addr flex items-center gap-3 rounded-lg bg-surface-container p-4">
                <code className="flex-1 break-all font-mono text-sm text-primary">
                  {idInfo?.address ?? linkStatus?.address ?? "loading..."}
                </code>
                <Icon
                  className="text-secondary transition-colors group-hover/addr:text-primary"
                  name="content_copy"
                />
              </div>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <label className="text-[10px] font-bold uppercase tracking-wider text-secondary-fixed-dim">
                  Device Peer ID
                </label>
                <p className="truncate rounded bg-surface-container-low p-2 font-mono text-xs text-on-surface">
                  {linkStatus?.peer_id ? truncAddr(linkStatus.peer_id) : "..."}
                </p>
              </div>
              <div className="space-y-2">
                <label className="text-[10px] font-bold uppercase tracking-wider text-secondary-fixed-dim">
                  Hostname
                </label>
                <p className="rounded bg-surface-container-low p-2 text-xs text-on-surface">
                  {thisDevice?.name ?? "..."}
                </p>
              </div>
            </div>
          </div>
        </div>
      </section>
    </SettingsPage>
  );
}
