import { useState } from "react";
import { Icon } from "../components/Icon";
import { skyfs, type InviteResult } from "../lib/rpc";

export default function InviteDevice() {
  const [invite, setInvite] = useState<InviteResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const generateInvite = async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await skyfs.invite();
      setInvite(result);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to generate invite");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex-1 p-12 flex items-center justify-center">
      <div className="max-w-5xl w-full">
        {/* Editorial header */}
        <div className="mb-16">
          <h1 className="text-[3.5rem] font-bold tracking-tight text-on-surface leading-tight max-w-2xl">
            Expand Your <span className="text-primary">Ethereal Vault</span>
          </h1>
          <p className="text-outline text-lg mt-4 max-w-xl">
            Bring a new node into your secure mesh. Our multi-step handshake
            ensures your data remains encapsulated and private during transit.
          </p>
        </div>

        {error && (
          <div className="mb-8 p-4 bg-error-container/20 text-error rounded-xl text-sm">
            {error}
          </div>
        )}

        {/* Steps grid */}
        <div className="grid grid-cols-12 gap-6">
          {/* Step 1: Generate invite code */}
          <div className="col-span-12 lg:col-span-8 bg-surface-container-lowest p-10 rounded-xl shadow-[0_20px_40px_rgba(26,28,29,0.04)] relative overflow-hidden group">
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
                Share this unique identifier with the device you want to add. It
                acts as a digital key — valid for a limited time to maintain
                security.
              </p>

              {invite ? (
                <div className="flex-1 w-full">
                  <label className="text-[0.75rem] font-semibold tracking-[0.05em] uppercase text-outline mb-3 block">
                    Invite Token
                  </label>
                  <div className="flex items-center gap-4 bg-surface-container-low p-5 rounded-lg">
                    <code className="font-mono text-xl text-primary font-medium tracking-widest break-all">
                      {invite.code}
                    </code>
                    <button
                      onClick={() =>
                        navigator.clipboard.writeText(invite.code)
                      }
                      className="ml-auto text-outline hover:text-primary transition-colors"
                    >
                      <Icon name="content_copy" />
                    </button>
                  </div>
                  {invite.expires && (
                    <div className="mt-4 flex items-center gap-2 text-secondary text-sm">
                      <Icon name="timer" className="text-sm" />
                      <span>Expires: {invite.expires}</span>
                    </div>
                  )}
                </div>
              ) : (
                <button
                  onClick={generateInvite}
                  disabled={loading}
                  className="lithic-gradient text-white py-4 px-8 rounded-full font-semibold shadow-lg shadow-primary/20 flex items-center justify-center gap-2 active:scale-95 disabled:opacity-50"
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

          {/* Step 2: Waiting state */}
          <div className="col-span-12 lg:col-span-4 flex flex-col">
            <div className="bg-surface-container-low p-10 rounded-xl flex-1 flex flex-col justify-center items-center text-center relative overflow-hidden">
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
                <h3 className="text-lg font-semibold">Waiting state</h3>
              </div>
              <p className="text-on-surface-variant text-sm mb-6">
                {invite
                  ? "Waiting for device to join..."
                  : "Generate an invite code first."}
              </p>
              {invite && (
                <div className="flex gap-1.5 items-center justify-center">
                  <div className="w-2 h-2 rounded-full bg-primary/30 animate-pulse" />
                  <div className="w-2 h-2 rounded-full bg-primary/60 animate-pulse [animation-delay:0.2s]" />
                  <div className="w-2 h-2 rounded-full bg-primary animate-pulse [animation-delay:0.4s]" />
                </div>
              )}
              <div className="mt-12 text-xs text-outline italic">
                Keep this window open to complete the secure handshake.
              </div>
            </div>
          </div>

          {/* Step 3: Approval */}
          <div className="col-span-12 bg-surface-container-lowest p-10 rounded-xl shadow-[0_20px_40px_rgba(26,28,29,0.04)] border border-outline-variant/10">
            <div className="flex items-center gap-3 mb-6">
              <span className="w-8 h-8 rounded-full bg-tertiary-fixed text-tertiary flex items-center justify-center font-bold text-sm">
                3
              </span>
              <h2 className="text-2xl font-semibold tracking-tight">
                Approval confirmation
              </h2>
            </div>
            <p className="text-on-surface-variant max-w-lg">
              When a device joins using your invite code, it will appear here
              for you to approve. Verify the device identity before accepting.
            </p>
          </div>
        </div>

        {/* Footer */}
        <div className="mt-16 text-center border-t border-outline-variant/10 pt-8">
          <p className="text-outline text-sm">
            Having trouble connecting?{" "}
            <button className="text-primary font-medium hover:underline">
              Read the P2P connection guide
            </button>
          </p>
        </div>
      </div>
    </div>
  );
}
