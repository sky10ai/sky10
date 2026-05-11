import { useEffect, useState } from "react";

export const ROOT_AGENT_HELPER_HIDDEN_STORAGE_KEY =
  "sky10:rootAgent:helperHidden";

const ROOT_AGENT_HELPER_VISIBILITY_EVENT = "sky10:rootAgentHelperVisibility";

function getLocalStorage() {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function getRootAgentHelperHidden() {
  const storage = getLocalStorage();
  if (!storage) return false;

  try {
    return storage.getItem(ROOT_AGENT_HELPER_HIDDEN_STORAGE_KEY) === "1";
  } catch {
    return false;
  }
}

export function setRootAgentHelperHidden(hidden: boolean) {
  const storage = getLocalStorage();

  try {
    if (storage) {
      if (hidden) {
        storage.setItem(ROOT_AGENT_HELPER_HIDDEN_STORAGE_KEY, "1");
      } else {
        storage.removeItem(ROOT_AGENT_HELPER_HIDDEN_STORAGE_KEY);
      }
    }
  } catch {
    // Ignore localStorage write failures; the in-memory control can still update.
  }

  try {
    window.dispatchEvent(new Event(ROOT_AGENT_HELPER_VISIBILITY_EVENT));
  } catch {
    // Ignore event failures in non-browser environments.
  }
}

export function useRootAgentHelperHidden() {
  const [hidden, setHidden] = useState(getRootAgentHelperHidden);

  useEffect(() => {
    const refresh = () => setHidden(getRootAgentHelperHidden());
    const onStorage = (event: StorageEvent) => {
      if (event.key === ROOT_AGENT_HELPER_HIDDEN_STORAGE_KEY) refresh();
    };

    window.addEventListener(ROOT_AGENT_HELPER_VISIBILITY_EVENT, refresh);
    window.addEventListener("storage", onStorage);
    return () => {
      window.removeEventListener(ROOT_AGENT_HELPER_VISIBILITY_EVENT, refresh);
      window.removeEventListener("storage", onStorage);
    };
  }, []);

  const updateHidden = (next: boolean) => {
    setHidden(next);
    setRootAgentHelperHidden(next);
  };

  return [hidden, updateHidden] as const;
}
