/**
 * Typed client for the separate Sky10 backend that brokers Google connection
 * and tool execution. The Sky10 OSS client never holds Google OAuth secrets
 * or provider API keys; everything goes through this broker.
 *
 * Configure with VITE_SKY10_API_URL. See docs/google-agent-auth-client-plan.md
 * and docs/google-agent-auth-backend-prompt.md for the contract.
 */

import { identity } from "./rpc"

export type GoogleConnectionStatus =
  | "none"
  | "pending"
  | "active"
  | "revoked"
  | "error"

export type GoogleProviderName = "composio" | "pipedream" | "arcade"

export interface GoogleConnectionInfo {
  connected: boolean
  status: GoogleConnectionStatus
  provider: GoogleProviderName
  connectedAccountId?: string
  availableTools: string[]
  lastError?: string
}

export interface GoogleConnectStartResponse {
  connectUrl: string
  connectionId: string
  provider: GoogleProviderName
}

export interface GoogleToolExecuteSuccess<T = unknown> {
  ok: true
  result: T
}

export interface GoogleToolExecuteFailure {
  ok: false
  error: { code: string; message: string }
  needsReconnect?: boolean
}

export type GoogleToolExecuteResponse<T = unknown> =
  | GoogleToolExecuteSuccess<T>
  | GoogleToolExecuteFailure

export class Sky10IntegrationsNotConfiguredError extends Error {
  constructor() {
    super(
      "Sky10 backend is not configured. Set VITE_SKY10_API_URL in the build environment.",
    )
    this.name = "Sky10IntegrationsNotConfiguredError"
  }
}

export class GoogleIntegrationError extends Error {
  code: string
  status: number
  needsReconnect: boolean
  constructor(
    code: string,
    message: string,
    status: number,
    needsReconnect = false,
  ) {
    super(message)
    this.name = "GoogleIntegrationError"
    this.code = code
    this.status = status
    this.needsReconnect = needsReconnect
  }
}

type AuthTokenProvider = () => Promise<string> | string

let authTokenProvider: AuthTokenProvider = () => ""

/**
 * Wire a function that returns a bearer token proving the caller controls
 * the current Sky10 identity. In MVP dev this can be a no-op; in production
 * it should call a daemon RPC (e.g., identity.signBrokerAssertion) and
 * return a short-lived ed25519-signed JWT. See client plan, Authentication.
 */
export function setGoogleIntegrationAuthTokenProvider(
  provider: AuthTokenProvider,
) {
  authTokenProvider = provider
}

function readBaseUrl(): string {
  const meta = import.meta as { env?: Record<string, string | undefined> }
  const raw = meta.env?.VITE_SKY10_API_URL
  if (typeof raw !== "string" || raw.trim() === "") {
    throw new Sky10IntegrationsNotConfiguredError()
  }
  return raw.replace(/\/+$/, "")
}

let cachedUserIdPromise: Promise<string> | null = null

async function resolveUserId(): Promise<string> {
  if (!cachedUserIdPromise) {
    cachedUserIdPromise = identity.show().then((info) => {
      if (!info.address) {
        throw new GoogleIntegrationError(
          "no_identity",
          "Local Sky10 identity has no address yet.",
          0,
        )
      }
      return info.address
    })
  }
  try {
    return await cachedUserIdPromise
  } catch (error) {
    cachedUserIdPromise = null
    throw error
  }
}

/** Test/dev helper: clear the cached identity address. */
export function resetGoogleIntegrationIdentityCache() {
  cachedUserIdPromise = null
}

interface RequestOptions {
  method: "GET" | "POST"
  path: string
  query?: Record<string, string | undefined>
  body?: unknown
  confirmed?: boolean
}

async function request<T>(opts: RequestOptions): Promise<T> {
  const base = readBaseUrl()
  const url = new URL(`${base}${opts.path}`)
  if (opts.query) {
    for (const [key, value] of Object.entries(opts.query)) {
      if (value !== undefined) url.searchParams.set(key, value)
    }
  }

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    Accept: "application/json",
  }
  const token = await authTokenProvider()
  if (token) headers["Authorization"] = `Bearer ${token}`
  if (opts.confirmed) headers["X-Sky10-Confirmed"] = "true"

  const res = await fetch(url.toString(), {
    method: opts.method,
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    credentials: "omit",
  })

  let parsed: unknown = null
  const text = await res.text()
  if (text) {
    try {
      parsed = JSON.parse(text)
    } catch {
      parsed = null
    }
  }

  if (!res.ok) {
    const err =
      parsed && typeof parsed === "object" && "error" in parsed
        ? (parsed as { error?: { code?: string; message?: string } }).error
        : undefined
    throw new GoogleIntegrationError(
      err?.code ?? `http_${res.status}`,
      err?.message ?? `HTTP ${res.status}`,
      res.status,
    )
  }

  return parsed as T
}

export const googleIntegration = {
  async startConnect(
    returnUrl?: string,
  ): Promise<GoogleConnectStartResponse> {
    const userId = await resolveUserId()
    return request<GoogleConnectStartResponse>({
      method: "POST",
      path: "/api/integrations/google/connect/start",
      body: { userId, returnUrl },
    })
  },

  async getStatus(): Promise<GoogleConnectionInfo> {
    const userId = await resolveUserId()
    return request<GoogleConnectionInfo>({
      method: "GET",
      path: "/api/integrations/google/status",
      query: { userId },
    })
  },

  async disconnect(): Promise<{ ok: true }> {
    const userId = await resolveUserId()
    return request<{ ok: true }>({
      method: "POST",
      path: "/api/integrations/google/disconnect",
      body: { userId },
    })
  },

  async executeTool<T = unknown>(
    tool: string,
    input: unknown,
    opts: { confirmed?: boolean } = {},
  ): Promise<GoogleToolExecuteResponse<T>> {
    const userId = await resolveUserId()
    try {
      const result = await request<GoogleToolExecuteResponse<T>>({
        method: "POST",
        path: "/api/tools/google/execute",
        body: { userId, tool, input },
        confirmed: opts.confirmed,
      })
      return result
    } catch (error) {
      if (error instanceof GoogleIntegrationError) {
        return {
          ok: false,
          error: { code: error.code, message: error.message },
          needsReconnect: error.needsReconnect,
        }
      }
      throw error
    }
  },
}
