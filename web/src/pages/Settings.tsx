import { Icon } from "../components/Icon";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { skyfs, skylink, identity } from "../lib/rpc";
import { useRPC, truncAddr } from "../lib/useRPC";

export default function Settings() {
  const { data: health } = useRPC(() => skyfs.health(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });
  const { data: linkStatus } = useRPC(() => skylink.status(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idInfo } = useRPC(() => identity.show(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: idDevices } = useRPC(() => identity.devices(), [], {
    refreshIntervalMs: 10_000,
  });
  const { data: deviceData } = useRPC(() => skyfs.deviceList(), [], {
    live: STORAGE_EVENT_TYPES,
    refreshIntervalMs: 10_000,
  });

  const thisDevice = (deviceData?.devices ?? []).find(
    (d) => d.pubkey === deviceData?.this_device
  );

  const version = health?.version ?? "";
  const versionParts = version.match(
    /^(v[\d.]+(?:-\w+)?)\s+\((\w+)\)\s+built\s+(.+)$/
  );

  return (
    <div className="p-12 max-w-6xl mx-auto space-y-12">
      {/* Hero title */}
      <div className="flex flex-col gap-2">
        <h2 className="text-5xl font-bold tracking-tight text-on-surface">
          Settings
        </h2>
        <p className="text-secondary max-w-md">
          Configure your vault identity, storage parameters, and network
          visibility.
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
                  Your unique identity across all devices.
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
                <span className="text-sm font-semibold">
                  {versionParts?.[1] ?? version}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Commit</span>
                <span className="text-xs font-mono bg-surface-container-lowest px-2 py-0.5 rounded">
                  {versionParts?.[2] ?? ""}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Build Date</span>
                <span className="text-sm">
                  {versionParts?.[3]?.split("T")[0] ?? ""}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">Uptime</span>
                <span className="text-sm font-semibold">
                  {health?.uptime ?? "..."}
                </span>
              </div>
              <div className="flex justify-between border-b border-outline-variant/10 pb-3">
                <span className="text-sm text-secondary">RPC Clients</span>
                <span className="text-sm font-semibold">
                  {health?.rpc_clients ?? 0}
                </span>
              </div>
            </div>
          </div>
        </section>

        {/* Skylink mode */}
        {linkStatus && (
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
              <button
                className={`flex-1 py-2 text-xs font-bold rounded-full ${linkStatus.mode === "private" ? "bg-white text-primary" : "text-primary-fixed-dim hover:text-white transition-colors"}`}
              >
                Private
              </button>
              <button
                className={`flex-1 py-2 text-xs font-bold rounded-full ${linkStatus.mode === "network" ? "bg-white text-primary" : "text-primary-fixed-dim hover:text-white transition-colors"}`}
              >
                Network
              </button>
            </div>
            <div className="relative z-10 space-y-4">
              <div className="space-y-1">
                <p className="text-[10px] uppercase tracking-wider font-bold opacity-70">
                  Connected Peers
                </p>
                <p className="text-2xl font-bold">{linkStatus.peers}</p>
              </div>
              <div className="space-y-2">
                <p className="text-[10px] uppercase tracking-wider font-bold opacity-70">
                  Listen Addresses
                </p>
                <div className="bg-white/10 rounded p-2 font-mono text-[10px] space-y-1">
                  {linkStatus.addrs.map((addr) => (
                    <p key={addr}>{addr}</p>
                  ))}
                </div>
              </div>
            </div>
          </section>
        )}

        {/* Authorized devices (manifest) */}
        <section className="col-span-12 lg:col-span-8 bg-surface-container-lowest rounded-xl p-8 border border-transparent space-y-6">
          <div className="space-y-1">
            <h3 className="text-xl font-semibold flex items-center gap-2">
              <Icon name="devices" className="text-tertiary" />
              Authorized Devices
            </h3>
            <p className="text-sm text-secondary">
              Devices signed into this identity's manifest.
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
                    className={
                      dev.current ? "text-primary" : "text-secondary"
                    }
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
    </div>
  );
}
