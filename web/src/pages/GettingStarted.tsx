import { useNavigate } from "react-router";
import { skyfs } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";
import { Icon } from "../components/Icon";

export default function GettingStarted() {
  const navigate = useNavigate();
  const { data } = useRPC(() => skyfs.deviceList(), [], {
    refreshIntervalMs: 5_000,
  });

  const deviceCount = data?.devices?.length ?? 0;

  return (
    <div className="flex-1 flex items-center justify-center p-8">
      <div className="max-w-lg w-full space-y-8">
        <div className="text-center space-y-3">
          <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-primary/10">
            <Icon name="rocket_launch" className="text-primary text-3xl" />
          </div>
          <h1 className="text-3xl font-bold text-on-surface">
            Welcome to sky10
          </h1>
          <p className="text-secondary text-lg">
            Your node is running. Add another device to start syncing.
          </p>
        </div>

        <div className="space-y-3">
          <button
            onClick={() => navigate("/devices/invite")}
            className="w-full flex items-center gap-4 p-4 rounded-xl bg-primary text-on-primary hover:bg-primary/90 transition-colors"
          >
            <Icon name="person_add" className="text-2xl" />
            <div className="text-left">
              <div className="font-semibold">Invite a device</div>
              <div className="text-sm opacity-80">
                Generate an invite code for another device to join
              </div>
            </div>
          </button>

          <button
            onClick={() => navigate("/devices")}
            className="w-full flex items-center gap-4 p-4 rounded-xl bg-surface-container hover:bg-surface-container-high transition-colors text-on-surface"
          >
            <Icon name="devices" className="text-2xl text-secondary" />
            <div className="text-left">
              <div className="font-semibold">
                View devices
                {deviceCount > 0 && (
                  <span className="ml-2 text-sm text-secondary font-normal">
                    ({deviceCount} connected)
                  </span>
                )}
              </div>
              <div className="text-sm text-secondary">
                See this device and manage your network
              </div>
            </div>
          </button>

          <button
            onClick={() => navigate("/kv")}
            className="w-full flex items-center gap-4 p-4 rounded-xl bg-surface-container hover:bg-surface-container-high transition-colors text-on-surface"
          >
            <Icon name="database" className="text-2xl text-secondary" />
            <div className="text-left">
              <div className="font-semibold">Key-Value store</div>
              <div className="text-sm text-secondary">
                Replicated state that syncs across all devices
              </div>
            </div>
          </button>

          <button
            onClick={() => navigate("/network")}
            className="w-full flex items-center gap-4 p-4 rounded-xl bg-surface-container hover:bg-surface-container-high transition-colors text-on-surface"
          >
            <Icon name="hub" className="text-2xl text-secondary" />
            <div className="text-left">
              <div className="font-semibold">Network</div>
              <div className="text-sm text-secondary">
                P2P connections, peers, and link status
              </div>
            </div>
          </button>
        </div>
      </div>
    </div>
  );
}
