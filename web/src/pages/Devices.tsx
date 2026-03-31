import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";

interface DeviceInfo {
  name: string;
  icon: string;
  location: string;
  address: string;
  status: "online" | "offline";
  isSelf?: boolean;
  lastSeen?: string;
  files: { label: string; value: string; icon?: string; active?: boolean };
  kv: { label: string; value: string; icon?: string; active?: boolean };
  link: { label: string; value: string; icon?: string; active?: boolean };
}

const devices: DeviceInfo[] = [
  {
    name: 'MacBook Pro 16"',
    icon: "laptop_mac",
    location: "San Francisco, USA",
    address: "sk1_8f29...a92b",
    status: "online",
    isSelf: true,
    files: { label: "Files", value: "Synced", icon: "folder_shared" },
    kv: { label: "KV", value: "Active", icon: "database", active: true },
    link: { label: "Link", value: "100%", icon: "link" },
  },
  {
    name: "Home Server",
    icon: "dns",
    location: "London, UK",
    address: "sk1_d492...ff31",
    status: "online",
    lastSeen: "2m ago",
    files: { label: "Files", value: "Idle", icon: "folder_shared" },
    kv: { label: "KV", value: "Synced", icon: "database" },
    link: { label: "Link", value: "Skylink", icon: "link" },
  },
  {
    name: "iPhone 15 Pro",
    icon: "smartphone",
    location: "San Francisco, USA",
    address: "sk1_99e1...12ab",
    status: "offline",
    lastSeen: "14h ago",
    files: { label: "Files", value: "Offline", icon: "cloud_off" },
    kv: { label: "KV", value: "Offline", icon: "database_off" },
    link: { label: "Link", value: "None", icon: "link_off" },
  },
  {
    name: "Workstation",
    icon: "desktop_windows",
    location: "Berlin, Germany",
    address: "sk1_721b...e00c",
    status: "online",
    lastSeen: "Just now",
    files: { label: "Files", value: "94%", icon: "sync", active: true },
    kv: { label: "KV", value: "Synced", icon: "database" },
    link: { label: "Link", value: "Skylink", icon: "link" },
  },
];

export default function Devices() {
  const navigate = useNavigate();

  return (
    <div className="p-12 max-w-7xl mx-auto">
      <div className="flex justify-between items-end mb-12">
        <div>
          <h1 className="text-4xl font-bold tracking-tight text-on-surface mb-2">
            Connected Devices
          </h1>
          <p className="text-secondary font-medium">
            You have 4 devices active in your private network.
          </p>
        </div>
        <button
          onClick={() => navigate("/devices/invite")}
          className="bg-primary text-on-primary px-6 py-2.5 rounded-full font-semibold flex items-center gap-2 text-sm shadow-lg shadow-primary/20 hover:shadow-primary/40 transition-all active:scale-95"
        >
          <Icon name="person_add" />
          Invite Device
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {devices.map((device) => (
          <div
            key={device.name}
            className={`rounded-xl p-6 shadow-sm hover:shadow-xl transition-all duration-500 ${
              device.status === "offline"
                ? "bg-surface-container-low/50 ring-1 ring-outline-variant/10 grayscale opacity-75"
                : device.isSelf
                  ? "bg-surface-container-lowest ring-1 ring-primary/20 relative overflow-hidden"
                  : "bg-surface-container-lowest ring-1 ring-outline-variant/10"
            }`}
          >
            {device.isSelf && (
              <div className="absolute top-0 right-0 p-4">
                <span className="bg-primary-fixed text-on-primary-fixed-variant text-[10px] font-bold px-2 py-1 rounded-full uppercase tracking-wider">
                  This Device
                </span>
              </div>
            )}
            <div className="flex items-start gap-4 mb-8">
              <div
                className={`w-14 h-14 rounded-2xl flex items-center justify-center ${
                  device.isSelf
                    ? "bg-primary-fixed/30 text-primary"
                    : device.status === "offline"
                      ? "bg-surface-container-high text-secondary"
                      : "bg-secondary-fixed/50 text-on-surface-variant"
                }`}
              >
                <Icon name={device.icon} className="text-3xl" />
              </div>
              <div>
                <h3 className="text-xl font-bold text-on-surface">
                  {device.name}
                </h3>
                <p className="text-xs text-secondary flex items-center gap-1">
                  <Icon name="location_on" className="text-xs" />
                  {device.location}
                </p>
              </div>
              {!device.isSelf && (
                <div className="ml-auto">
                  {device.status === "online" ? (
                    <div className="flex items-center gap-1 bg-green-50 text-green-700 px-2 py-1 rounded-full text-[10px] font-bold">
                      <span className="w-1.5 h-1.5 rounded-full bg-green-600" />
                      Online
                    </div>
                  ) : (
                    <div className="flex items-center gap-1 bg-surface-container-highest text-secondary px-2 py-1 rounded-full text-[10px] font-bold">
                      Offline
                    </div>
                  )}
                </div>
              )}
            </div>

            <div className="space-y-4">
              <div>
                <label className="text-[10px] font-bold text-secondary uppercase tracking-widest block mb-1">
                  Sky10 Address
                </label>
                <div className="flex items-center justify-between bg-surface-container-low px-3 py-2 rounded-lg font-mono text-xs text-on-surface-variant transition-colors hover:bg-surface-container-high cursor-pointer">
                  <span>{device.address}</span>
                  <Icon name="content_copy" className="text-sm" />
                </div>
              </div>
              {device.lastSeen && (
                <div className="flex items-center justify-between text-xs py-2 border-b border-surface-container-high">
                  <span className="text-secondary font-medium">Last seen</span>
                  <span className="text-on-surface font-semibold">
                    {device.lastSeen}
                  </span>
                </div>
              )}
              <div className="grid grid-cols-3 gap-2">
                {[device.files, device.kv, device.link].map((s) => (
                  <div
                    key={s.label}
                    className={`bg-surface-container-low p-2 rounded-lg text-center ${s.active ? "border-b-2 border-primary" : ""} ${device.status === "offline" ? "bg-surface-container-highest" : ""}`}
                  >
                    <Icon
                      name={s.icon ?? "help"}
                      className={`text-sm block mb-1 ${device.status === "offline" ? "" : "text-primary"}`}
                    />
                    <span className="text-[9px] font-bold text-secondary uppercase">
                      {s.label}
                    </span>
                    <span className="block text-xs font-semibold text-on-surface">
                      {s.value}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        ))}

        {/* Network pulse card */}
        <div className="col-span-1 lg:col-span-2 bg-gradient-to-br from-inverse-surface to-on-surface rounded-xl p-8 shadow-2xl relative overflow-hidden flex items-center justify-between">
          <div className="relative z-10">
            <span className="bg-white/10 text-white/80 text-[10px] font-bold px-2 py-1 rounded-full uppercase tracking-widest mb-4 inline-block">
              Network Pulse
            </span>
            <h2 className="text-3xl font-bold text-white mb-2">
              Encrypted Tunnel Active
            </h2>
            <p className="text-white/60 max-w-sm mb-6">
              Your data is being synchronized across 3 continents using P2P
              Skylink. All endpoints are verified.
            </p>
            <div className="flex gap-12">
              <div>
                <span className="block text-white/40 text-[10px] uppercase font-bold tracking-widest">
                  Active nodes
                </span>
                <span className="text-2xl font-mono font-bold text-white">
                  03
                </span>
              </div>
              <div>
                <span className="block text-white/40 text-[10px] uppercase font-bold tracking-widest">
                  Global Latency
                </span>
                <span className="text-2xl font-mono font-bold text-white">
                  42ms
                </span>
              </div>
              <div>
                <span className="block text-white/40 text-[10px] uppercase font-bold tracking-widest">
                  Data Transfer
                </span>
                <span className="text-2xl font-mono font-bold text-white">
                  1.2 TB
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
