import {
  startTransition,
  useCallback,
  useEffect,
  useEffectEvent,
  useRef,
  useState,
} from "react";
import { subscribe, type EventHandler } from "./events";

export type LiveEventMatcher =
  | string
  | ((event: string, data: unknown) => boolean);

interface UseRPCOptions {
  debounceMs?: number;
  keepPreviousData?: boolean;
  live?: readonly LiveEventMatcher[];
  refreshIntervalMs?: number;
}

interface RefetchOptions {
  background?: boolean;
}

type MutateUpdater<T> = T | null | ((previous: T | null) => T | null);

/** Minimal hook for fetching RPC data with loading/error states. */
export function useRPC<T>(
  fetcher: () => Promise<T>,
  deps: unknown[] = [],
  options: UseRPCOptions = {}
) {
  const {
    debounceMs = 250,
    keepPreviousData = true,
    live = [],
    refreshIntervalMs,
  } = options;
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const dataRef = useRef<T | null>(null);
  const requestIDRef = useRef(0);
  const debounceTimerRef = useRef<number | null>(null);
  const pausedRef = useRef(false);

  useEffect(() => {
    dataRef.current = data;
  }, [data]);

  const executeFetch = useEffectEvent(async (background: boolean) => {
    const requestID = ++requestIDRef.current;
    const hasData = dataRef.current !== null;
    const useBackgroundRefresh = background && keepPreviousData && hasData;

    if (useBackgroundRefresh) {
      setRefreshing(true);
    } else {
      setLoading(true);
      if (!keepPreviousData && hasData) {
        setData(null);
        dataRef.current = null;
      }
    }

    setError(null);

    try {
      const result = await fetcher();
      if (requestID !== requestIDRef.current) return;

      startTransition(() => {
        dataRef.current = result;
        setData(result);
      });
    } catch (e: unknown) {
      if (requestID !== requestIDRef.current) return;
      setError(e instanceof Error ? e.message : "Request failed");
    } finally {
      if (requestID === requestIDRef.current) {
        setLoading(false);
        setRefreshing(false);
      }
    }
  });

  const refetch = useCallback((refetchOptions: RefetchOptions = {}) => {
    void executeFetch(Boolean(refetchOptions.background));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  const mutate = useCallback((updater: MutateUpdater<T>) => {
    startTransition(() => {
      setData((previous) => {
        const next =
          typeof updater === "function"
            ? (updater as (value: T | null) => T | null)(previous)
            : updater;
        dataRef.current = next;
        return next;
      });
    });
  }, []);

  useEffect(() => {
    refetch();
  }, [refetch]);

  const handleLiveEvent = useEffectEvent<EventHandler>((event, eventData) => {
    const matches = live.some((matcher) =>
      typeof matcher === "string"
        ? matcher === event
        : matcher(event, eventData)
    );

    if (!matches) return;

    if (debounceMs <= 0) {
      refetch({ background: true });
      return;
    }

    if (debounceTimerRef.current !== null) {
      window.clearTimeout(debounceTimerRef.current);
    }

    debounceTimerRef.current = window.setTimeout(() => {
      refetch({ background: true });
      debounceTimerRef.current = null;
    }, debounceMs);
  });

  useEffect(() => {
    if (live.length === 0) return;

    const unsubscribe = subscribe((event, eventData) => {
      handleLiveEvent(event, eventData);
    });

    return () => {
      if (debounceTimerRef.current !== null) {
        window.clearTimeout(debounceTimerRef.current);
        debounceTimerRef.current = null;
      }
      unsubscribe();
    };
  }, [debounceMs, live.length]);

  useEffect(() => {
    if (!refreshIntervalMs) return;

    const interval = window.setInterval(() => {
      if (pausedRef.current) return;
      refetch({ background: dataRef.current !== null });
    }, refreshIntervalMs);

    return () => window.clearInterval(interval);
  }, [refetch, refreshIntervalMs]);

  const pause = useCallback(() => { pausedRef.current = true; }, []);
  const resume = useCallback(() => { pausedRef.current = false; }, []);

  return { data, error, loading, mutate, pause, refreshing, refetch, resume };
}

/** Format bytes to human-readable string. */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val < 10 ? val.toFixed(1) : Math.round(val)} ${units[i]}`;
}

/** Format an ISO timestamp to relative time (e.g., "2m ago"). */
export function timeAgo(iso: string): string {
  if (!iso) return "-";
  const timestamp = new Date(iso).getTime();
  if (Number.isNaN(timestamp)) return "-";
  const diff = Date.now() - timestamp;
  if (diff < 0) return "just now";
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

/** Truncate a sky10 address for display. */
export function truncAddr(addr: string): string {
  if (addr.length <= 16) return addr;
  return addr.slice(0, 10) + "..." + addr.slice(-4);
}
