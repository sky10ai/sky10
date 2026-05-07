import { Link } from "react-router";
import { Icon } from "../components/Icon";
import { SettingsPage } from "../components/SettingsPage";

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
    description: "Local identity and device peer",
    icon: "fingerprint",
    iconClassName: "bg-primary/10 text-primary",
    label: "Identity",
    to: "/settings/identity",
  },
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
    description: "Named AI endpoints and credentials",
    icon: "smart_toy",
    iconClassName: "bg-sky-500/10 text-sky-700 dark:text-sky-200",
    label: "AI Connections",
    to: "/settings/ai-connections",
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
    </SettingsPage>
  );
}
