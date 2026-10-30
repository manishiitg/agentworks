import React, { useState, useEffect, useCallback, useRef } from 'react'
import { Loader2, ServerOff, RefreshCw } from 'lucide-react'
import { getApiBaseUrl, getAuthToken } from '../services/api'
import { GATEWAY_LOGIN_HEADER, gatewayLoginTarget, redirectToGatewayLogin } from '../utils/gatewayAuth'

const RETRY_MS = { min: 1000, max: 5000 }
const TIMEOUT_MS = 5000

type State = 'connecting' | 'connected' | 'error'
type HealthResult = 'healthy' | 'failed' | 'authentication-required'

export default function ServerConnectionStatus({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<State>('connecting')
  const [retryCount, setRetryCount] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const mounted = useRef(true)
  const displayApiBaseUrl = getApiBaseUrl()

  const checkHealth = useCallback(async (): Promise<HealthResult> => {
    const ac = new AbortController()
    const t = setTimeout(() => ac.abort(), TIMEOUT_MS)
    const apiBaseUrl = getApiBaseUrl()
    const healthUrl = `${apiBaseUrl}/api/health`
    try {
      console.info('[ServerConnectionStatus] checking health', {
        apiBaseUrl,
        healthUrl,
        runtimeConfig: typeof window !== 'undefined' ? window.__APP_RUNTIME_CONFIG__ : undefined,
      })
      // Connected-server health goes through the authenticated local proxy.
      const token = getAuthToken()
      const res = await fetch(healthUrl, {
        signal: ac.signal,
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      clearTimeout(t)
      const loginTarget = gatewayLoginTarget(res.status, res.headers.get(GATEWAY_LOGIN_HEADER))
      if (redirectToGatewayLogin(loginTarget)) return 'authentication-required'
      if (!res.ok) {
        setError(`Server returned ${res.status}`)
        return 'failed'
      }
      const data = await res.json().catch(() => ({}))
      return data.status === 'healthy' ? 'healthy' : 'failed'
    } catch (e) {
      clearTimeout(t)
      setError(e instanceof Error ? (e.name === 'AbortError' ? 'Connection timed out' : e.message) : 'Connection failed')
      return 'failed'
    }
  }, [])

  const tryConnect = useCallback(async () => {
    if (!mounted.current) return
    const result = await checkHealth()
    if (!mounted.current) return
    if (result === 'authentication-required') return
    if (result === 'healthy') {
      setState('connected')
      setRetryCount(0)
      setError(null)
      return
    }
    setState('error')
    setRetryCount(c => c + 1)
    const delay = Math.min(RETRY_MS.min * Math.pow(1.5, retryCount + 1), RETRY_MS.max)
    timeoutRef.current = setTimeout(() => {
      if (mounted.current) {
        setState('connecting')
        tryConnect()
      }
    }, delay)
  }, [checkHealth, retryCount])

  useEffect(() => {
    mounted.current = true
    tryConnect()
    return () => {
      mounted.current = false
      if (timeoutRef.current) clearTimeout(timeoutRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const retry = useCallback(() => {
    if (timeoutRef.current) clearTimeout(timeoutRef.current)
    setState('connecting')
    setRetryCount(0)
    tryConnect()
  }, [tryConnect])

  // Don't render children until connected - this prevents:
  // 1. App components from mounting and processing events
  // 2. Keyboard navigation/tab focus on hidden elements
  // 3. Race conditions from rendering children then hiding them
  if (state === 'connected') return <>{children}</>

  const overlay = (
    <div className="fixed inset-0 z-[99999] flex items-center justify-center bg-gray-900/95 backdrop-blur-sm" role="dialog" aria-modal>
      <div className="text-center space-y-6 p-8 max-w-md">
        <div className="space-y-2">
          <h1 className="text-2xl font-semibold text-white">AgentWorks</h1>
          <p className="text-gray-400 text-sm">Connecting to server...</p>
        </div>
        <div className="flex justify-center">
          {state === 'connecting' ? (
            <div className="relative">
              <div className="w-16 h-16 rounded-full border-4 border-gray-700 border-t-blue-500 animate-spin" />
              <Loader2 className="w-8 h-8 text-blue-500 absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 animate-pulse" />
            </div>
          ) : (
            <div className="w-16 h-16 rounded-full bg-red-500/20 flex items-center justify-center">
              <ServerOff className="w-8 h-8 text-red-400" />
            </div>
          )}
        </div>
        <div className="space-y-2">
          {state === 'connecting' ? (
            <p className="text-gray-300">
              {retryCount === 0 ? 'Establishing connection...' : `Retrying... (attempt ${retryCount + 1})`}
            </p>
          ) : (
            <>
              <p className="text-red-400 font-medium">Unable to connect to server</p>
              {error && <p className="text-gray-500 text-sm">{error}</p>}
            </>
          )}
        </div>
        <div className="bg-gray-800/50 rounded-lg px-4 py-2 inline-block">
          <code className="text-gray-400 text-sm">{displayApiBaseUrl}</code>
        </div>
        {state === 'error' && (
          <button onClick={retry} className="flex items-center gap-2 mx-auto px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition-colors">
            <RefreshCw className="w-4 h-4" />
            Retry Now
          </button>
        )}
        <p className="text-gray-500 text-xs">Make sure the backend server is running and accessible</p>
      </div>
    </div>
  )
  // Render overlay directly (not via portal) since children aren't rendered
  // This ensures no app content exists underneath
  return overlay
}
