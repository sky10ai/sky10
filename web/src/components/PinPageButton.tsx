import {
  getPinnablePage,
  type PinnablePageID,
} from "../lib/pinnablePages";
import { usePinnedSidebarPages } from "../lib/usePinnedSidebarPages";
import { Icon } from "./Icon";

interface PinPageButtonProps {
  pageID: PinnablePageID;
}

export function PinPageButton({ pageID }: PinPageButtonProps) {
  const page = getPinnablePage(pageID);
  const { isPinned, togglePagePinned } = usePinnedSidebarPages();
  if (!page) return null;

  const pinned = isPinned(pageID);
  const action = pinned ? "Unpin" : "Pin";

  return (
    <button
      aria-label={`${action} ${page.label} ${pinned ? "from" : "to"} sidebar`}
      aria-pressed={pinned}
      className={`inline-flex items-center gap-2 rounded-full border px-4 py-2 text-sm font-semibold transition-colors ${
        pinned
          ? "border-primary/20 bg-primary/10 text-primary hover:bg-primary/15"
          : "border-outline-variant/20 bg-surface-container-lowest text-secondary hover:border-primary/20 hover:bg-surface-container-low hover:text-on-surface"
      }`}
      onClick={() => togglePagePinned(pageID)}
      title={`${action} ${page.label} ${pinned ? "from" : "to"} sidebar`}
      type="button"
    >
      <Icon className="text-base" filled={pinned} name="push_pin" />
      {pinned ? "Pinned" : "Pin"}
    </button>
  );
}
