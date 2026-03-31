import { Icon } from "../components/Icon";

export default function Settings() {
  return (
    <div className="p-12 max-w-6xl mx-auto space-y-12">
      {/* Hero title */}
      <div className="flex flex-col gap-2">
        <h2 className="text-5xl font-bold tracking-tight text-on-surface">
          Settings
        </h2>
        <p className="text-secondary max-w-md">
          Configure your ethereal vault identity, storage parameters, and
          network visibility.
        </p>
      </div>

      {/* Bento grid */}
      <div className="grid grid-cols-12 gap-6">
        {/* Identity */}
        <section className="col-span-12 lg:col-span-7 bg-surface-container-lowest rounded-xl p-8 flex flex-col justify-between group hover:shadow-xl transition-all duration-500 border border-transparent">
          <div className="space-y-6">
            <div className="flex justify-between items-start">
              <div className="space-y-1">
                <h3 className="text-xl font-semibold flex items-center gap-2">
                  <Icon name="fingerprint" className="text-primary" />
                  Identity
                </h3>
                <p className="text-sm text-secondary">
                  Your unique decentralized footprint on the sky10 network.
                </p>
              </div>
              <span className="bg-primary/10 text-primary px-3 py-1 rounded-full text-[10px] font-bold uppercase tracking-widest">
                Active
              </span>
            </div>
            <div className="space-y-4">
              <div className="space-y-2">
                <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                  sky10 Address
                </label>
                <div className="flex items-center gap-3 bg-surface-container p-4 rounded-lg group/addr cursor-pointer">
                  <code className="text-sm font-mono text-primary flex-1 break-all">
                    sky_vault_0x92f...728b928c001f2e91
                  </code>
                  <Icon
                    name="content_copy"
                    className="text-secondary group-hover/addr:text-primary transition-colors"
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Peer ID
                  </label>
                  <p className="font-mono text-xs text-on-surface bg-surface-container-low p-2 rounded">
                    QmXoyp...3V8
                  </p>
                </div>
                <div className="space-y-2">
                  <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                    Vault Created
                  </label>
                  <p className="text-xs text-on-surface bg-surface-container-low p-2 rounded">
                    Oct 12, 2023
                  </p>
                </div>
              </div>
            </div>
          </div>
          <div className="mt-8 flex items-end justify-between">
            <div className="bg-white p-2 rounded-lg shadow-sm border border-outline-variant/20">
              <div className="w-20 h-20 bg-surface-container-high rounded flex items-center justify-center">
                <Icon name="qr_code_2" className="text-4xl text-outline" />
              </div>
            </div>
            <button className="text-xs font-semibold text-primary hover:underline">
              Revoke Identity
            </button>
          </div>
        </section>

        {/* About */}
        <section className="col-span-12 lg:col-span-5 bg-surface-container-high rounded-xl p-8 flex flex-col justify-between border border-transparent">
          <div className="space-y-6">
            <div className="space-y-1">
              <h3 className="text-xl font-semibold flex items-center gap-2">
                <Icon name="info" className="text-secondary" />
                About
              </h3>
              <p className="text-sm text-secondary">
                System core information.
              </p>
            </div>
            <div className="space-y-4">
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Version</span>
                <span className="text-sm font-semibold">0.4.2-alpha</span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Commit Hash</span>
                <span className="text-xs font-mono bg-surface-container-lowest px-2 py-0.5 rounded">
                  af38b21
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Build Date</span>
                <span className="text-sm">2024-05-18</span>
              </div>
            </div>
          </div>
          <div className="mt-8 grid grid-cols-2 gap-3">
            <button className="flex items-center justify-center gap-2 bg-surface-container-lowest py-3 rounded-lg text-xs font-semibold hover:bg-white transition-colors">
              <Icon name="description" className="text-sm" />
              Documentation
            </button>
            <button className="flex items-center justify-center gap-2 bg-surface-container-lowest py-3 rounded-lg text-xs font-semibold hover:bg-white transition-colors">
              <Icon name="terminal" className="text-sm" />
              Changelog
            </button>
          </div>
        </section>

        {/* S3 Storage */}
        <section className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-8">
          <div className="flex justify-between items-center">
            <div className="space-y-1">
              <h3 className="text-xl font-semibold flex items-center gap-2">
                <Icon name="cloud" className="text-tertiary" />
                S3 Storage
              </h3>
              <p className="text-sm text-secondary">
                Managing your backend storage infrastructure.
              </p>
            </div>
            <div className="flex items-center gap-2 text-emerald-500 bg-emerald-50 px-3 py-1 rounded-full">
              <Icon name="check_circle" className="text-sm" />
              <span className="text-[10px] font-bold uppercase tracking-widest">
                Connected
              </span>
            </div>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
            <div className="space-y-4">
              <div className="bg-surface-container-low p-4 rounded-xl space-y-4">
                {[
                  { label: "Endpoint", value: "s3.us-east-1.amazonaws.com" },
                  { label: "Bucket", value: "sky10-primary-vault-01" },
                  { label: "Region", value: "us-east-1" },
                ].map((item) => (
                  <div key={item.label} className="space-y-1">
                    <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                      {item.label}
                    </label>
                    <p className="text-sm font-medium">{item.value}</p>
                  </div>
                ))}
              </div>
            </div>
            <div className="space-y-4">
              <div className="flex justify-between items-end">
                <label className="text-[10px] uppercase tracking-wider font-bold text-secondary-fixed-dim">
                  Usage Breakdown
                </label>
                <span className="text-lg font-bold">
                  12.4 GB{" "}
                  <span className="text-xs text-secondary font-normal">
                    / 100 GB
                  </span>
                </span>
              </div>
              <div className="h-2 w-full bg-surface-container-highest rounded-full overflow-hidden flex">
                <div className="h-full bg-primary" style={{ width: "45%" }} />
                <div className="h-full bg-tertiary" style={{ width: "20%" }} />
                <div
                  className="h-full bg-secondary-fixed-dim"
                  style={{ width: "5%" }}
                />
              </div>
              <div className="space-y-2 pt-2">
                {[
                  { color: "bg-primary", label: "Primary Drive", size: "8.2 GB" },
                  { color: "bg-tertiary", label: "KV Database", size: "3.1 GB" },
                  {
                    color: "bg-secondary-fixed-dim",
                    label: "System Logs",
                    size: "1.1 GB",
                  },
                ].map((item) => (
                  <div
                    key={item.label}
                    className="flex justify-between items-center text-xs"
                  >
                    <span className="flex items-center gap-2">
                      <span
                        className={`w-2 h-2 rounded-full ${item.color}`}
                      />{" "}
                      {item.label}
                    </span>
                    <span className="font-semibold">{item.size}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        </section>

        {/* Skylink mode */}
        <section className="col-span-12 lg:col-span-4 bg-primary text-white rounded-xl p-8 flex flex-col gap-8 relative overflow-hidden">
          <div className="relative z-10 space-y-2">
            <h3 className="text-xl font-bold flex items-center gap-2">
              <Icon name="wifi_tethering" />
              Skylink Mode
            </h3>
            <p className="text-xs text-primary-fixed-dim">
              Control how this vault interacts with the decentralized cloud.
            </p>
          </div>
          <div className="relative z-10 flex bg-on-primary-fixed-variant/40 p-1 rounded-full">
            <button className="flex-1 py-2 text-xs font-bold rounded-full bg-white text-primary">
              Private
            </button>
            <button className="flex-1 py-2 text-xs font-bold rounded-full text-primary-fixed-dim hover:text-white transition-colors">
              Network
            </button>
          </div>
          <div className="relative z-10 space-y-4">
            <div className="space-y-1">
              <p className="text-[10px] uppercase tracking-wider font-bold opacity-70">
                Status
              </p>
              <p className="text-sm">
                Vault is currently <strong>Invisible</strong>. Only explicitly
                linked peers can discover your storage endpoints.
              </p>
            </div>
            <div className="space-y-2">
              <p className="text-[10px] uppercase tracking-wider font-bold opacity-70">
                Listen Addresses
              </p>
              <div className="bg-white/10 rounded p-2 font-mono text-[10px] space-y-1">
                <p>/ip4/127.0.0.1/tcp/4001</p>
                <p>/ip4/192.168.1.15/tcp/4001</p>
              </div>
            </div>
          </div>
          <div className="relative z-10 mt-auto">
            <button className="w-full bg-white text-primary py-3 rounded-lg text-sm font-bold flex items-center justify-center gap-2 hover:bg-primary-fixed transition-all">
              Manage Authorized Peers (12)
            </button>
          </div>
        </section>

        {/* Security actions */}
        <section className="col-span-12 grid grid-cols-1 md:grid-cols-3 gap-6">
          {[
            {
              icon: "lock_reset",
              color: "text-error",
              hoverBg: "hover:bg-error-container/30",
              hoverIcon: "group-hover:bg-error/10",
              title: "Rotate Keys",
              desc: "Generate new encryption seeds",
            },
            {
              icon: "backup",
              color: "text-primary",
              hoverBg: "hover:bg-primary-fixed/30",
              hoverIcon: "group-hover:bg-primary/10",
              title: "Backup Vault",
              desc: "Export identity & config file",
            },
            {
              icon: "history",
              color: "text-on-surface",
              hoverBg: "hover:bg-secondary-container",
              hoverIcon: "group-hover:bg-on-surface/10",
              title: "Audit Logs",
              desc: "Review access history",
            },
          ].map((action) => (
            <div
              key={action.title}
              className={`bg-surface-container-low p-6 rounded-xl flex items-center gap-4 group cursor-pointer ${action.hoverBg} transition-colors`}
            >
              <div
                className={`w-12 h-12 rounded-full bg-surface-container flex items-center justify-center ${action.hoverIcon}`}
              >
                <Icon name={action.icon} className={action.color} />
              </div>
              <div>
                <h4 className="font-semibold text-sm">{action.title}</h4>
                <p className="text-[11px] text-secondary">{action.desc}</p>
              </div>
            </div>
          ))}
        </section>
      </div>
    </div>
  );
}
