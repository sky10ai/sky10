import { useState } from "react";
import { useNavigate } from "react-router";
import { identity } from "../lib/rpc";
import { useRPC } from "../lib/useRPC";
import { Icon } from "../components/Icon";

export default function GettingStarted() {
  const navigate = useNavigate();
  const [inviteCode, setInviteCode] = useState("");
  const [joinError, setJoinError] = useState("");
  const [joining, setJoining] = useState(false);
  const { data } = useRPC(() => identity.deviceList(), [], {
    refreshIntervalMs: 5_000,
  });

  const deviceCount = data?.devices?.length ?? 0;

  const handleJoin = async () => {
    const code = inviteCode.trim();
    if (!code) return;
    setJoinError("");
    setJoining(true);
    try {
      // TODO: wire to sky10 join RPC when available in daemon
      setJoinError("Join via web UI coming soon — use 'sky10 join " + code + "' from the CLI");
    } finally {
      setJoining(false);
    }
  };

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

          <div className="w-full rounded-xl bg-surface-container p-4 space-y-3">
            <div className="flex items-center gap-3">
              <Icon name="login" className="text-2xl text-secondary" />
              <div>
                <div className="font-semibold text-on-surface">Join another device</div>
                <div className="text-sm text-secondary">
                  Paste an invite code from an existing sky10 node
                </div>
              </div>
            </div>
            <div className="flex gap-2">
              <input
                type="text"
                value={inviteCode}
                onChange={(e) => { setInviteCode(e.target.value); setJoinError(""); }}
                placeholder="sky10p2p_..."
                className="flex-1 rounded-lg bg-surface-container-lowest px-3 py-2 text-sm text-on-surface placeholder:text-outline border border-outline-variant/20 focus:border-primary/40 focus:outline-none"
              />
              <button
                onClick={handleJoin}
                disabled={!inviteCode.trim() || joining}
                className="rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-on-primary hover:bg-primary/90 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
              >
                {joining ? "Joining..." : "Join"}
              </button>
            </div>
            {joinError && (
              <p className="text-xs text-error">{joinError}</p>
            )}
          </div>

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
