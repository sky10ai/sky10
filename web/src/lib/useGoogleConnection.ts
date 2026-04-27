/**
 * React hook for the Sky10 broker Google connection. Pairs with
 * googleIntegrationClient. The hook owns status loading and the
 * connect/disconnect actions; the calling page is responsible for the
 * OAuth popup lifecycle (use web/src/lib/oauthPopup.ts).
 */

import { startTransition, useCallback, useEffect, useRef, useState } from "react"
import {
  googleIntegration,
  GoogleIntegrationError,
  Sky10IntegrationsNotConfiguredError,
  type GoogleConnectionInfo,
  type GoogleConnectStartResponse,
} from "./googleIntegrationClient"

export type GoogleConnectionUIState =
  | "loading"
  | "configured_missing"
  | "ready"

export interface UseGoogleConnectionResult {
  data: GoogleConnectionInfo | null
  error: string | null
  state: GoogleConnectionUIState
  loading: boolean
  refreshing: boolean
  refetch: () => void
  startConnect: (returnUrl?: string) => Promise<GoogleConnectStartResponse>
  disconnect: () => Promise<void>
  pollWhileActive: (until: () => boolean, intervalMs?: number) => void
}

/**
 * Read Google connection status for the current Sky10 identity. The hook
 * does not open OAuth popups — surface that in the page using
 * lib/oauthPopup.ts and call refetch() / pollWhileActive() afterwards.
 */
export function useGoogleConnection(): UseGoogleConnectionResult {
  const [data, setData] = useState<GoogleConnectionInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [state, setState] = useState<GoogleConnectionUIState>("loading")
  const requestIDRef = useRef(0)
  const pollTimerRef = useRef<number | null>(null)

  const fetchStatus = useCallback(async (background: boolean) => {
    const requestID = ++requestIDRef.current
    if (background) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    setError(null)
    try {
      const next = await googleIntegration.getStatus()
      if (requestID !== requestIDRef.current) return
      startTransition(() => {
        setData(next)
        setState("ready")
      })
    } catch (e: unknown) {
      if (requestID !== requestIDRef.current) return
      if (e instanceof Sky10IntegrationsNotConfiguredError) {
        setState("configured_missing")
        setError(e.message)
        setData(null)
        return
      }
      const message =
        e instanceof GoogleIntegrationError
          ? e.message
          : e instanceof Error
            ? e.message
            : "Failed to load Google connection status"
      setError(message)
      setState("ready")
    } finally {
      if (requestID === requestIDRef.current) {
        setLoading(false)
        setRefreshing(false)
      }
    }
  }, [])

  const refetch = useCallback(() => {
    void fetchStatus(true)
  }, [fetchStatus])

  useEffect(() => {
    void fetchStatus(false)
  }, [fetchStatus])

  const clearPollTimer = useCallback(() => {
    if (pollTimerRef.current !== null) {
      window.clearInterval(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }, [])

  const pollWhileActive = useCallback(
    (until: () => boolean, intervalMs = 2000) => {
      clearPollTimer()
      pollTimerRef.current = window.setInterval(() => {
        if (until()) {
          clearPollTimer()
          void fetchStatus(true)
          return
        }
        void fetchStatus(true)
      }, intervalMs)
    },
    [clearPollTimer, fetchStatus],
  )

  useEffect(() => {
    return () => clearPollTimer()
  }, [clearPollTimer])

  const startConnect = useCallback(
    async (returnUrl?: string) => {
      const result = await googleIntegration.startConnect(returnUrl)
      void fetchStatus(true)
      return result
    },
    [fetchStatus],
  )

  const disconnect = useCallback(async () => {
    await googleIntegration.disconnect()
    void fetchStatus(true)
  }, [fetchStatus])

  return {
    data,
    error,
    state,
    loading,
    refreshing,
    refetch,
    startConnect,
    disconnect,
    pollWhileActive,
  }
}
