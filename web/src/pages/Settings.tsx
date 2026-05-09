import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { identity, skylink } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

const settingsTools = [
  {
    description: "Trusted devices and invites",
    icon: "devices",
    label: "Devices",
    to: "/settings/devices",
  },
  {
    description: "Queues, approvals, delivery history",
    icon: "inbox",
    label: "Mailbox",
    to: "/settings/mailbox",
  },
  {
    description: "Peers, relays, delivery health",
    icon: "hub",
    label: "Network",
    to: "/settings/network",
  },
  {
    description: "Live replicated key-value data",
    icon: "database",
    label: "Key-Value",
    to: "/settings/kv",
  },
  {
    description: "Sync work and storage",
    icon: "monitor_heart",
    label: "Activity",
    to: "/settings/activity",
  },
] as const;

const settingsLinks = [
  {
    description: "Theme and display mode",
    icon: "palette",
    iconClassName: "bg-primary/10 text-primary",
    label: "Visuals",
    to: "/settings/visuals",
  },
  {
    description: "Isolated agent runtimes",
    icon: "deployed_code",
    iconClassName: "bg-primary/10 text-primary",
    label: "Sandboxes",
    to: "/settings/sandboxes",
  },
  {
    description: "Codex account sign-in",
    icon: "chat",
    iconClassName: "bg-primary/10 text-primary",
    label: "ChatGPT",
    to: "/settings/codex",
  },
  {
    description: "Slack and app connections",
    icon: "forum",
    iconClassName: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-200",
    label: "Messaging",
    to: "/settings/messaging",
  },
  {
    description: "Paid tools for agents",
    icon: "storefront",
    iconClassName: "bg-amber-500/10 text-amber-700 dark:text-amber-200",
    label: "Services",
    to: "/settings/services",
  },
  {
    description: "Balances, funding, and transfers",
    icon: "account_balance_wallet",
    iconClassName: "bg-tertiary/10 text-tertiary",
    label: "Wallet",
    to: "/settings/wallet",
  },
  {
    description: "Encrypted keys and tokens",
    icon: "key_vertical",
    iconClassName: "bg-primary-fixed/60 text-on-primary-fixed-variant",
    label: "Secrets",
    to: "/settings/secrets",
  },
  {
    description: "Local helper binaries",
    icon: "download",
    iconClassName: "bg-tertiary/10 text-tertiary",
    label: "Managed Apps",
    to: "/settings/apps",
  },
] as const;

export default function Settings() {
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idInfo } = useRPC(() => identity.show(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idDevices } = useRPC(() => identity.devices(), [], {
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
      description="Manage node, integrations, and tools."
      title="Settings"
      width="wide"
    >
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {settingsLinks.map((link) => (
          <Link
            className="group flex min-h-32 items-center justify-between gap-4 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-6 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
            key={link.to}
            to={link.to}
          >
            <div className="flex min-w-0 items-center gap-4">
              <div
                className={`flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl ${link.iconClassName}`}
              >
                <Icon className="text-2xl" name={link.icon} />
              </div>
              <div className="min-w-0 space-y-1">
                <h3 className="text-xl font-semibold text-on-surface">
                  {link.label}
                </h3>
                <p className="text-sm text-secondary">{link.description}</p>
              </div>
            </div>
            <Icon
              className="text-base text-primary transition-colors group-hover:text-on-surface"
              name="arrow_forward"
            />
          </Link>
        ))}
      </div>

      <section className="space-y-4">
        <div className="space-y-1">
          <h3 className="text-xl font-semibold text-on-surface">Operations</h3>
        </div>
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
          {settingsTools.map((tool) => (
            <Link
              key={tool.to}
              className="group flex items-center justify-between gap-4 rounded-2xl border border-outline-variant/10 bg-surface-container-lowest p-5 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg"
              to={tool.to}
            >
              <div className="flex min-w-0 items-center gap-3">
                <Icon className="text-xl text-primary" name={tool.icon} />
                <div className="min-w-0">
                  <h4 className="text-lg font-semibold text-on-surface">
                    {tool.label}
                  </h4>
                  <p className="text-sm text-secondary">{tool.description}</p>
                </div>
              </div>
              <Icon
                className="text-base text-primary transition-colors group-hover:text-on-surface"
                name="arrow_forward"
              />
            </Link>
          ))}
        </div>
      </section>

      <div className="grid grid-cols-12 gap-6">
        <section className="col-span-12 bg-surface-container-lowest rounded-xl p-8 flex flex-col justify-between group hover:shadow-xl transition-all duration-500 border border-transparent">
          <div className="space-y-6">
            <div className="flex justify-between items-start">
              <div className="space-y-1">
                <h3 className="text-xl font-semibold flex items-center gap-2">
                  <Icon name="fingerprint" className="text-primary" />
                  Identity
                </h3>
                <p className="text-sm text-secondary">
                  Local identity.
                </p>
              </div>
              <span className="bg-primary/10 text-primary px-3 py-1 rounded-full text-[10px] font-bold uppercase tracking-widest">
                Active
              </span>
            </div>
            <div className="space-y-4">
              <div className="space-y-2">
                <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                  Identity Address
                </label>
                <div className="flex items-center gap-3 bg-surface-container p-4 rounded-lg group/addr cursor-pointer">
                  <code className="text-sm font-mono text-primary flex-1 break-all">
                    {idInfo?.address ?? linkStatus?.address ?? "loading..."}
                  </code>
                  <Icon
                    name="content_copy"
                    className="text-secondary group-hover/addr:text-primary transition-colors"
                  />
                </div>
              </div>
              <div className="grid grid-cols-3 gap-4">
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Device Peer ID
                  </label>
                  <p className="font-mono text-xs text-on-surface bg-surface-container-low p-2 rounded truncate">
                    {linkStatus?.peer_id
                      ? truncAddr(linkStatus.peer_id)
                      : "..."}
                  </p>
                </div>
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Hostname
                  </label>
                  <p className="text-xs text-on-surface bg-surface-container-low p-2 rounded">
                    {thisDevice?.name ?? "..."}
                  </p>
                </div>
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Authorized Devices
                  </label>
                  <p className="text-xs text-on-surface bg-surface-container-low p-2 rounded">
                    {idInfo?.device_count ?? "..."}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section className="col-span-12 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-6">
          <div className="space-y-1">
            <h3 className="text-xl font-semibold flex items-center gap-2">
              <Icon name="devices" className="text-tertiary" />
              Authorized Devices
            </h3>
            <p className="text-sm text-secondary">
              Signed-in devices.
            </p>
          </div>
          <div className="space-y-3">
            {(idDevices?.devices ?? []).map((dev) => (
              <div
                key={dev.public_key}
                className={`flex items-center justify-between p-4 rounded-lg ${
                  dev.current
                    ? "bg-primary/5 border border-primary/20"
                    : "bg-surface-container"
                }`}
              >
                <div className="flex items-center gap-3">
                  <Icon
                    name={dev.current ? "laptop_mac" : "devices_other"}
                    className={dev.current ? "text-primary" : "text-secondary"}
                  />
                  <div>
                    <p className="text-sm font-medium">
                      {dev.name}
                      {dev.current && (
                        <span className="ml-2 text-[10px] font-bold uppercase tracking-widest text-primary bg-primary/10 px-2 py-0.5 rounded-full">
                          This Device
                        </span>
                      )}
                    </p>
                    <p className="text-xs text-secondary font-mono">
                      {dev.public_key.slice(0, 16)}...
                    </p>
                  </div>
                </div>
                <p className="text-xs text-secondary">
                  Added {dev.added_at.split("T")[0]}
                </p>
              </div>
            ))}
            {(idDevices?.devices ?? []).length === 0 && (
              <p className="text-sm text-secondary py-4 text-center">
                Loading device manifest...
              </p>
            )}
          </div>
        </section>
      </div>
    </SettingsPage>
  );
}
