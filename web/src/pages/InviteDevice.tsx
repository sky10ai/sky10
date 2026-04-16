import { useState, useEffect, useRef } from "react";
import { useNavigate } from "react-router";
import { Icon } from "../components/Icon";
import { identity, type InviteResult } from "../lib/rpc";

export default function InviteDevice() {
  const navigate = useNavigate();
  const [invite, setInvite] = useState<InviteResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [joined, setJoined] = useState<string | null>(null); // device name
  const initialDeviceCount = useRef<number | null>(null);

  const generateInvite = async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await identity.invite();
      setInvite(result);
      // Capture current device count to detect new joins.
      const devices = await identity.deviceList();
      initialDeviceCount.current = devices.devices?.length ?? 0;
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to generate invite");
    } finally {
      setLoading(false);
    }
  };

  // Poll for new devices after invite is generated.
  useEffect(() => {
    if (!invite || joined) return;

    const interval = setInterval(async () => {
      try {
        const devices = await identity.deviceList();
        const count = devices.devices?.length ?? 0;
        if (
          initialDeviceCount.current !== null &&
          count > initialDeviceCount.current
        ) {
          // Find the newest device (not this_device).
          const newDev = devices.devices?.find(
            (d: { id: string; name?: string }) => d.id !== devices.this_device
          );
          setJoined(newDev?.name || "New device");
        }
      } catch {
        // ignore poll errors
      }
    }, 2000);

    return () => clearInterval(interval);
  }, [invite, joined]);

  return (
    <div className="flex-1 p-12 flex items-center justify-center">
      <div className="max-w-5xl w-full">
        <div className="mb-16">
          <h1 className="text-[3.5rem] font-bold tracking-tight text-on-surface leading-tight max-w-2xl">
            Expand <span className="text-primary">sky10</span>
          </h1>
          <p className="text-outline text-lg mt-4 max-w-xl">
            Add another device to your network. Share the invite code and
            they'll sync automatically over P2P.
          </p>
        </div>

        {error && (
          <div className="mb-8 p-4 bg-error-container/20 text-error rounded-xl text-sm">
            {error}
          </div>
        )}

        <div className="grid grid-cols-12 gap-6">
          {/* Step 1: Generate invite code */}
          <div className="group relative col-span-12 overflow-hidden rounded-xl border border-outline-variant/10 bg-surface-container-lowest p-10 shadow-sm lg:col-span-8">
            <div className="relative z-10">
              <div className="flex items-center gap-3 mb-8">
                <span className="w-8 h-8 rounded-full bg-primary-fixed text-primary flex items-center justify-center font-bold text-sm">
                  1
                </span>
                <h2 className="text-2xl font-semibold tracking-tight">
                  Generate invite code
                </h2>
              </div>
              <p className="text-on-surface-variant mb-10 max-w-md leading-relaxed">
                Share this code with the device you want to add. On the other
                device, run <code className="text-primary">sky10 join &lt;code&gt;</code>.
              </p>

              {invite ? (
                <div className="flex-1 w-full">
                  <label className="text-[0.75rem] font-semibold tracking-[0.05em] uppercase text-outline mb-3 block">
                    Invite Code
                  </label>
                  <div className="flex items-center gap-4 bg-surface-container-low p-5 rounded-lg">
                    <code className="font-mono text-xl text-primary font-medium tracking-widest break-all">
                      {invite.code}
                    </code>
                    <button
                      onClick={() => {
                        navigator.clipboard.writeText(invite.code);
                        setCopied(true);
                        setTimeout(() => setCopied(false), 2000);
                      }}
                      className={`ml-auto transition-colors ${copied ? "text-primary" : "text-outline hover:text-primary"}`}
                    >
                      <Icon name={copied ? "check" : "content_copy"} />
                    </button>
                  </div>
                </div>
              ) : (
                <button
                  onClick={generateInvite}
                  disabled={loading}
                  className="lithic-gradient flex items-center justify-center gap-2 rounded-full px-8 py-4 font-semibold text-on-primary shadow-lg shadow-primary/20 active:scale-95 disabled:opacity-50"
                >
                  {loading ? (
                    <Icon name="sync" className="animate-spin" />
                  ) : (
                    <Icon name="vpn_key" />
                  )}
                  {loading ? "Generating..." : "Generate Invite Code"}
                </button>
              )}
            </div>
            <div className="absolute -right-20 -bottom-20 w-80 h-80 bg-primary/5 rounded-full blur-3xl group-hover:bg-primary/10 transition-colors" />
          </div>

          {/* Step 2: Status */}
          <div className="col-span-12 lg:col-span-4 flex flex-col">
            <div className="relative flex flex-1 flex-col items-center justify-center overflow-hidden rounded-xl border border-outline-variant/10 bg-surface-container-low p-10 text-center">
              {joined ? (
                <>
                  <div className="mb-6">
                    <div className="w-20 h-20 flex items-center justify-center rounded-full bg-primary/10">
                      <Icon name="check_circle" className="text-primary text-5xl" />
                    </div>
                  </div>
                  <h3 className="text-xl font-bold text-on-surface mb-2">Device joined!</h3>
                  <p className="text-secondary text-sm mb-6">
                    <span className="font-semibold text-on-surface">{joined}</span> is now
                    part of your network and syncing.
                  </p>
                  <button
                    onClick={() => navigate("/devices")}
                    className="lithic-gradient rounded-full px-6 py-3 text-sm font-semibold text-on-primary"
                  >
                    View Devices
                  </button>
                </>
              ) : (
                <>
                  <div className="mb-8">
                    <div className="relative w-20 h-20 flex items-center justify-center">
                      <div className="absolute inset-0 border-2 border-primary rounded-full opacity-20 scale-125" />
                      <div className="absolute inset-0 border-2 border-primary rounded-full opacity-40 scale-100" />
                      <Icon name="radar" className="text-primary text-4xl" />
                    </div>
                  </div>
                  <div className="flex items-center gap-2 justify-center mb-4">
                    <span className="w-6 h-6 rounded-full bg-surface-container-highest text-secondary flex items-center justify-center font-bold text-xs">
                      2
                    </span>
                    <h3 className="text-lg font-semibold">
                      {invite ? "Listening..." : "Waiting"}
                    </h3>
                  </div>
                  <p className="text-on-surface-variant text-sm mb-6">
                    {invite
                      ? "Waiting for the other device to join..."
                      : "Generate an invite code first."}
                  </p>
                  {invite && (
                    <div className="flex gap-1.5 items-center justify-center">
                      <div className="w-2 h-2 rounded-full bg-primary/30 animate-pulse" />
                      <div className="w-2 h-2 rounded-full bg-primary/60 animate-pulse [animation-delay:0.2s]" />
                      <div className="w-2 h-2 rounded-full bg-primary animate-pulse [animation-delay:0.4s]" />
                    </div>
                  )}
                </>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
