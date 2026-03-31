import { Icon } from "../components/Icon";

export default function InviteDevice() {
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
                acts as a digital key, valid for only 15 minutes to maintain
                absolute security.
              </p>
              <div className="flex flex-col md:flex-row gap-12 items-center">
                <div className="flex-1 w-full">
                  <label className="text-[0.75rem] font-semibold tracking-[0.05em] uppercase text-outline mb-3 block">
                    Invite Token
                  </label>
                  <div className="flex items-center gap-4 bg-surface-container-low p-5 rounded-lg group/code transition-all hover:bg-surface-container-high">
                    <code className="font-mono text-xl text-primary font-medium tracking-widest break-all">
                      SKY-8821-X99Q-P2P
                    </code>
                    <button className="ml-auto text-outline hover:text-primary transition-colors">
                      <Icon name="content_copy" />
                    </button>
                  </div>
                  <div className="mt-6 flex items-center gap-2 text-secondary text-sm">
                    <Icon name="timer" className="text-sm" />
                    <span>Expires in 14:52</span>
                  </div>
                </div>
                {/* QR placeholder */}
                <div className="flex flex-col items-center gap-4">
                  <div className="w-48 h-48 bg-white p-3 rounded-xl border border-outline-variant/10 shadow-sm flex items-center justify-center">
                    <div className="w-full h-full bg-surface-container-high rounded-lg flex items-center justify-center">
                      <Icon
                        name="qr_code_2"
                        className="text-6xl text-outline"
                      />
                    </div>
                  </div>
                  <span className="text-[10px] font-semibold text-outline uppercase tracking-widest">
                    Scan with Sky10 Mobile
                  </span>
                </div>
              </div>
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
                Waiting for device to join...
              </p>
              <div className="flex gap-1.5 items-center justify-center">
                <div className="w-2 h-2 rounded-full bg-primary/30" />
                <div className="w-2 h-2 rounded-full bg-primary/60" />
                <div className="w-2 h-2 rounded-full bg-primary" />
              </div>
              <div className="mt-12 text-xs text-outline italic">
                Keep this window open to complete the secure handshake.
              </div>
            </div>
          </div>

          {/* Step 3: Approval */}
          <div className="col-span-12 bg-surface-container-lowest p-10 rounded-xl shadow-[0_20px_40px_rgba(26,28,29,0.04)] border border-primary/10">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-8">
              <div className="flex-1">
                <div className="flex items-center gap-3 mb-6">
                  <span className="w-8 h-8 rounded-full bg-tertiary-fixed text-tertiary flex items-center justify-center font-bold text-sm">
                    3
                  </span>
                  <h2 className="text-2xl font-semibold tracking-tight">
                    Approval confirmation
                  </h2>
                </div>
                <div className="flex items-start gap-6 bg-surface p-6 rounded-lg border border-outline-variant/15">
                  <div className="w-16 h-16 bg-surface-container-highest rounded-full flex items-center justify-center">
                    <Icon
                      name="laptop_mac"
                      className="text-3xl text-on-surface-variant"
                    />
                  </div>
                  <div>
                    <h4 className="text-xl font-medium mb-1">
                      MacBook Pro M3 (Alpha-1)
                    </h4>
                    <div className="flex items-center gap-3 text-secondary text-sm">
                      <span className="font-mono bg-surface-container-high px-2 py-0.5 rounded">
                        0x9e...a24b
                      </span>
                      <span className="text-outline-variant">&bull;</span>
                      <span>California, US</span>
                    </div>
                    <p className="text-on-surface-variant text-sm mt-3 leading-relaxed">
                      Verify that the identifier above matches the one shown on
                      your new device. Unauthorized devices can never be
                      recovered if mistakenly approved.
                    </p>
                  </div>
                </div>
              </div>
              <div className="flex flex-col gap-3 min-w-[200px]">
                <button className="lithic-gradient text-white py-4 px-8 rounded-full font-semibold shadow-lg shadow-primary/20 hover:shadow-primary/30 transition-shadow flex items-center justify-center gap-2 active:scale-95">
                  <Icon name="verified_user" />
                  Approve
                </button>
                <button className="bg-surface text-error py-3 px-8 rounded-full font-medium hover:bg-error-container/20 transition-colors flex items-center justify-center gap-2 active:scale-95">
                  <Icon name="block" />
                  Reject
                </button>
              </div>
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="mt-16 text-center border-t border-outline-variant/10 pt-8">
          <p className="text-outline text-sm">
            Having trouble connecting?{" "}
            <button className="text-primary font-medium hover:underline">
              Read the P2P connection guide
            </button>{" "}
            or{" "}
            <button className="text-primary font-medium hover:underline">
              Contact vault security
            </button>
            .
          </p>
        </div>
      </div>
    </div>
  );
}
