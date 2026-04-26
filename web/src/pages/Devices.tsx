import { useState } from "react";
import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";
import { RelativeTime } from "../components/RelativeTime";
import { SettingsPage } from "../components/SettingsPage";
import { STORAGE_EVENT_TYPES } from "../lib/events";
import { identity } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";

export default function Devices() {
  const navigate = useNavigate();
  const [actionError, setActionError] = useState<string | null>(null);
  const { data, loading, error, refetch } = useRPC(
    () => identity.deviceList(),
    [],
    {
      live: STORAGE_EVENT_TYPES,
      refreshIntervalMs: 10_000,
    },
  );

  const devices = data?.devices ?? [];
  const thisDevice = data?.this_device ?? "";

  return (
    <SettingsPage
      actions={
        <button
          onClick={() => navigate("/settings/devices/invite")}
          className="bg-primary text-on-primary px-6 py-2.5 rounded-full font-semibold flex items-center gap-2 text-sm shadow-lg shadow-primary/20 hover:shadow-primary/40 transition-all active:scale-95"
          type="button"
        >
          <Icon name="person_add" />
          Invite Device
        </button>
      }
      backHref="/settings"
      description={
        devices.length === 0
          ? "No devices registered yet."
          : `${devices.length} device${devices.length !== 1 ? "s" : ""} in your network.`
      }
      title="Connected Devices"
      width="wide"
    >
      {(error || actionError) && (
        <div className="flex justify-between rounded-xl bg-error-container/20 p-4 text-sm text-error">
          <span>{actionError ?? error}</span>
          {actionError && (
            <button
              className="text-xs hover:underline"
              onClick={() => setActionError(null)}
            >
              dismiss
            </button>
          )}
        </div>
      )}

      {loading && devices.length === 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          {[1, 2].map((i) => (
            <div
              key={i}
              className="bg-surface-container-lowest p-6 rounded-xl h-[280px] animate-pulse"
            />
          ))}
        </div>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {devices.map((device) => {
          const isSelf = device.id === thisDevice;
          const displayName = device.alias || device.name;
          const platformIcon =
            device.platform === "macOS"
              ? "laptop_mac"
              : device.platform === "Linux"
                ? "dns"
                : "smartphone";

          return (
            <div
              key={device.id}
              className={`rounded-xl p-6 shadow-sm hover:shadow-xl transition-all duration-500 ${
                isSelf
                  ? "bg-surface-container-lowest ring-1 ring-primary/20"
                  : "bg-surface-container-lowest ring-1 ring-outline-variant/10"
              }`}
            >
              <div className="mb-3 h-5">
                {isSelf && (
                  <span className="bg-primary-fixed text-on-primary-fixed-variant text-[10px] font-bold px-2 py-1 rounded-full uppercase tracking-wider">
                    This Device
                  </span>
                )}
              </div>
              <div className="flex items-start gap-4 mb-6">
                <div
                  className={`w-14 h-14 rounded-2xl flex items-center justify-center ${
                    isSelf
                      ? "bg-primary-fixed/30 text-primary"
                      : "bg-secondary-fixed/50 text-on-surface-variant"
                  }`}
                >
                  <Icon name={platformIcon} className="text-3xl" />
                </div>
                <div className="flex-1 min-w-0">
                  <h3 className="text-xl font-bold text-on-surface truncate">
                    {displayName}
                  </h3>
                  <p className="text-xs text-secondary flex items-center gap-1">
                    <Icon name="location_on" className="text-xs" />
                    {device.location || device.ip}
                  </p>
                </div>
              </div>

              <div className="space-y-4">
                <div>
                  <label className="text-[10px] font-bold text-secondary uppercase tracking-widest block mb-1">
                    Device ID
                  </label>
                  <div className="flex items-center justify-between bg-surface-container-low px-3 py-2 rounded-lg font-mono text-xs text-on-surface-variant transition-colors hover:bg-surface-container-high cursor-pointer">
                    <span>{device.id}</span>
                    <Icon name="content_copy" className="text-sm" />
                  </div>
                </div>
                <div className="flex items-center justify-between text-xs py-2 border-b border-surface-container-high">
                  <span className="text-secondary font-medium">Platform</span>
                  <span className="text-on-surface font-semibold">
                    {device.platform}
                  </span>
                </div>
                <div className="flex items-center justify-between text-xs py-2 border-b border-surface-container-high">
                  <span className="text-secondary font-medium">Last seen</span>
                  <RelativeTime
                    className="font-semibold text-on-surface"
                    value={device.last_seen}
                  />
                </div>
                <div className="flex items-center justify-between text-xs py-2 border-b border-surface-container-high">
                  <span className="text-secondary font-medium">Version</span>
                  <span className="text-on-surface font-mono text-[11px]">
                    {(device.version || "—").split(" ")[0]}
                  </span>
                </div>
                <div className="flex items-center justify-between text-xs py-2">
                  <span className="text-secondary font-medium">P2P Addrs</span>
                  <span className="text-on-surface font-semibold">
                    {device.multiaddrs?.length ?? 0}
                  </span>
                </div>
              </div>
              {!isSelf && (
                <button
                  className="mt-4 flex w-full items-center justify-center gap-2 rounded-lg border border-error/20 px-3 py-2 text-xs font-medium text-error transition-colors hover:bg-error-container/20"
                  onClick={async () => {
                    if (!confirm(`Remove device "${displayName}"?`)) return;
                    try {
                      await identity.deviceRemove({ pubkey: device.pubkey });
                      refetch();
                    } catch (e: unknown) {
                      setActionError(
                        e instanceof Error
                          ? e.message
                          : "Failed to remove device",
                      );
                    }
                  }}
                  type="button"
                >
                  <Icon className="text-sm" name="person_remove" />
                  Remove Device
                </button>
              )}
            </div>
          );
        })}
      </div>
    </SettingsPage>
  );
}
