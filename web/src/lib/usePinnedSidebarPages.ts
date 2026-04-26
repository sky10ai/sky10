import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  PINNED_SIDEBAR_PAGES_CHANGED_EVENT,
  PINNED_SIDEBAR_PAGES_STORAGE_KEY,
  normalizePinnedPageIDs,
  parsePinnedPageIDs,
  readPinnedPageIDs,
  resolvePinnedPages,
  writePinnedPageIDs,
  type PinnablePageID,
} from "./pinnablePages";

export function usePinnedSidebarPages() {
  const [pinnedPageIDs, setPinnedPageIDsState] =
    useState<PinnablePageID[]>(readPinnedPageIDs);
  const pinnedPageIDsRef = useRef(pinnedPageIDs);

  const replacePinnedPageIDs = useCallback((ids: readonly PinnablePageID[]) => {
    const normalized = writePinnedPageIDs(ids);
    pinnedPageIDsRef.current = normalized;
    setPinnedPageIDsState(normalized);
  }, []);

  const syncPinnedPageIDs = useCallback((ids: readonly PinnablePageID[]) => {
    pinnedPageIDsRef.current = [...ids];
    setPinnedPageIDsState([...ids]);
  }, []);

  useEffect(() => {
    function syncFromChangeEvent(event: Event) {
      const hasDetail =
        event instanceof CustomEvent && Array.isArray(event.detail);
      syncPinnedPageIDs(
        hasDetail ? normalizePinnedPageIDs(event.detail) : readPinnedPageIDs(),
      );
    }

    function syncFromStorageEvent(event: StorageEvent) {
      if (event.key !== PINNED_SIDEBAR_PAGES_STORAGE_KEY) return;
      syncPinnedPageIDs(parsePinnedPageIDs(event.newValue));
    }

    window.addEventListener("storage", syncFromStorageEvent);
    window.addEventListener(
      PINNED_SIDEBAR_PAGES_CHANGED_EVENT,
      syncFromChangeEvent,
    );

    return () => {
      window.removeEventListener("storage", syncFromStorageEvent);
      window.removeEventListener(
        PINNED_SIDEBAR_PAGES_CHANGED_EVENT,
        syncFromChangeEvent,
      );
    };
  }, [syncPinnedPageIDs]);

  const pinPage = useCallback(
    (id: PinnablePageID) => {
      if (pinnedPageIDsRef.current.includes(id)) return;
      replacePinnedPageIDs([...pinnedPageIDsRef.current, id]);
    },
    [replacePinnedPageIDs],
  );

  const unpinPage = useCallback(
    (id: PinnablePageID) => {
      replacePinnedPageIDs(
        pinnedPageIDsRef.current.filter((currentID) => currentID !== id),
      );
    },
    [replacePinnedPageIDs],
  );

  const togglePagePinned = useCallback(
    (id: PinnablePageID) => {
      if (pinnedPageIDsRef.current.includes(id)) {
        unpinPage(id);
      } else {
        pinPage(id);
      }
    },
    [pinPage, unpinPage],
  );

  const isPinned = useCallback(
    (id: PinnablePageID) => pinnedPageIDs.includes(id),
    [pinnedPageIDs],
  );

  const pinnedPages = useMemo(
    () => resolvePinnedPages(pinnedPageIDs),
    [pinnedPageIDs],
  );

  return {
    isPinned,
    pinPage,
    pinnedPageIDs,
    pinnedPages,
    togglePagePinned,
    unpinPage,
  };
}
