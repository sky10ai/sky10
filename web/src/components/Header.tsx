import { Icon } from "./Icon";

const tabs = [
  { label: "Explorer", id: "explorer" },
  { label: "Vault", id: "vault" },
  { label: "Activity", id: "activity" },
];

export function Header({
  activeTab = "explorer",
  searchPlaceholder = "Search resources...",
}: {
  activeTab?: string;
  searchPlaceholder?: string;
}) {
  return (
    <header className="flex justify-between items-center px-8 sticky top-0 z-30 h-16 w-full glass-effect border-b border-transparent text-sm">
      <div className="flex items-center gap-8">
        <div className="flex gap-6 items-center">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              className={`cursor-pointer opacity-90 hover:opacity-100 transition-colors ${
                tab.id === activeTab
                  ? "text-on-surface dark:text-white font-semibold border-b-2 border-[#007AFF] pb-1"
                  : "text-[#71717a] hover:text-on-surface dark:hover:text-white"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      <div className="flex items-center gap-6">
        <div className="relative group">
          <span className="absolute left-3 top-1/2 -translate-y-1/2">
            <Icon name="search" className="text-outline text-lg" />
          </span>
          <input
            type="text"
            className="bg-surface-container-high border-none rounded-full py-1.5 pl-10 pr-4 w-64 focus:ring-1 focus:ring-primary text-xs transition-all"
            placeholder={searchPlaceholder}
          />
        </div>
        <div className="flex items-center gap-4 text-secondary">
          <button className="hover:text-primary transition-colors cursor-pointer">
            <Icon name="notifications" />
          </button>
          <button className="hover:text-primary transition-colors cursor-pointer">
            <Icon name="terminal" />
          </button>
          <button className="hover:text-primary transition-colors cursor-pointer">
            <Icon name="account_circle" />
          </button>
        </div>
      </div>
    </header>
  );
}
