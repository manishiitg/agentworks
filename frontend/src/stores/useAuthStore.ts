import { rememberSharedReturnPath, sharedReturnPath } from '../utils/sharedLinks'
import { connectionConsentReturnPath, rememberMcpConsentReturnPath } from '../utils/mcpOAuthReturn'
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { authApi, getAuthToken, setAuthToken, clearAuthToken } from '../services/api'
import type { AuthUser, AuthProvider } from '../services/api'
import { getWorkspaceScopedStorageKey } from './useWorkspaceConnectionStore'
import { switchChatAccount, migrateLegacyChatStateForVerifiedAccount } from './useChatStore'
import { invalidateChatIdentity } from '../utils/chatIdentity'

let authOperation = 0

// Key for storing OAuth state in sessionStorage
const OAUTH_STATE_KEY = 'oauth_state'

interface AuthState {
  // User state
  user: AuthUser | null
  isAuthenticated: boolean
  isLoading: boolean
  error: string | null

  // Multi-user mode flag
  isMultiUserMode: boolean
  isMultiUserModeChecked: boolean

  // Available auth providers
  providers: AuthProvider[]

  // Actions
  checkAuthMode: () => Promise<void>
  login: (username: string, password: string, provider?: string) => Promise<void>
  loginWithOAuth: (provider: string) => Promise<void>
  handleOAuthCallback: (code: string, state: string) => Promise<void>
  logout: () => Promise<void>
  checkAuth: () => Promise<void>
  clearError: () => void
}

// Helper to get redirect URI for OAuth
function getOAuthRedirectUri(): string {
  return `${window.location.origin}/auth/callback`
}

// Helper to save OAuth state to sessionStorage
function saveOAuthState(state: string, provider: string): void {
  sessionStorage.setItem(OAUTH_STATE_KEY, JSON.stringify({ state, provider }))
}

// Helper to get and clear OAuth state from sessionStorage
function getAndClearOAuthState(): { state: string; provider: string } | null {
  const data = sessionStorage.getItem(OAUTH_STATE_KEY)
  if (!data) return null
  sessionStorage.removeItem(OAUTH_STATE_KEY)
  try {
    return JSON.parse(data)
  } catch {
    return null
  }
}

// Peek at the stored OAuth state without clearing it. Supabase social login
// does not echo our CSRF state back to /auth/callback (only ?code=...), so
// the callback page falls back to this when the URL carries no state. The
// binding still holds: the value lives in per-tab sessionStorage, and the
// server additionally requires the PKCE verifier created for this flow to
// exchange the code.
export function peekStoredOAuthState(): string | null {
  const data = sessionStorage.getItem(OAUTH_STATE_KEY)
  if (!data) return null
  try {
    return (JSON.parse(data) as { state: string }).state ?? null
  } catch {
    return null
  }
}

export const useAuthStore = create<AuthState>()(
    persist(
      (set) => ({
        // Initial state
        user: null,
        isAuthenticated: false,
        isLoading: false,
        error: null,
        isMultiUserMode: false,
        isMultiUserModeChecked: false,
        providers: [],

        // Check if server is in multi-user mode and get available providers
        checkAuthMode: async () => {
          try {
            const response = await authApi.getAuthMode()
            if (!response.multi_user_mode) migrateLegacyChatStateForVerifiedAccount(null)
            set({
              isMultiUserMode: response.multi_user_mode,
              isMultiUserModeChecked: true,
              providers: response.providers || [],
              error: null,
            })
          } catch (error) {
            console.error('[AUTH] Failed to check auth mode:', error)
            // An unreachable mode endpoint is not evidence of a single-user server.
            set({
              isMultiUserMode: true,
              isMultiUserModeChecked: true,
              providers: [],
              error: 'Cannot verify server authentication mode. Please retry.',
            })
          }
        },

        // Login action for credentials-based providers
        login: async (username: string, password: string, provider?: string) => {
          const operation = ++authOperation
          set({ isLoading: true, error: null })
          try {
            const response = await authApi.login(username, password, provider)
            if (operation !== authOperation) return
            migrateLegacyChatStateForVerifiedAccount(response.user.id)
            switchChatAccount(response.user.id)
            setAuthToken(response.token)
            set({
              user: response.user,
              isAuthenticated: true,
              isLoading: false,
              error: null
            })
          } catch (error: unknown) {
            if (operation !== authOperation) return
            const message = error instanceof Error ? error.message : 'Login failed'
            set({ isLoading: false, error: message })
            throw error
          }
        },

        // Start OAuth flow for a provider
        loginWithOAuth: async (provider: string) => {
          set({ isLoading: true, error: null })
          try {
            const returnTo = sharedReturnPath(window.location.pathname + window.location.search)
              || sharedReturnPath(new URLSearchParams(window.location.search).get('next'))
            const redirectUri = getOAuthRedirectUri()
            const response = await authApi.startOAuth(provider, redirectUri)

            rememberSharedReturnPath(returnTo, response.state)
            rememberMcpConsentReturnPath(connectionConsentReturnPath(window.location.pathname + window.location.search)
              || connectionConsentReturnPath(new URLSearchParams(window.location.search).get('next')), response.state)

            // Save state to sessionStorage for verification on callback
            saveOAuthState(response.state, provider)

            // Redirect to OAuth provider
            window.location.href = response.auth_url
          } catch (error: unknown) {
            const message = error instanceof Error ? error.message : 'Failed to start OAuth flow'
            set({ isLoading: false, error: message })
            throw error
          }
        },

        // Handle OAuth callback - exchange code for app JWT
        handleOAuthCallback: async (code: string, state: string) => {
          const operation = ++authOperation
          set({ isLoading: true, error: null })
          try {
            // Verify state matches what we stored
            const savedState = getAndClearOAuthState()
            if (!savedState || savedState.state !== state) {
              throw new Error('Invalid OAuth state - possible CSRF attack')
            }

            const response = await authApi.handleOAuthCallback(code, state)
            if (operation !== authOperation) return
            migrateLegacyChatStateForVerifiedAccount(response.user.id)
            switchChatAccount(response.user.id)
            setAuthToken(response.token)
            set({
              user: response.user,
              isAuthenticated: true,
              isLoading: false,
              error: null
            })
          } catch (error: unknown) {
            if (operation !== authOperation) return
            const message = error instanceof Error ? error.message : 'OAuth callback failed'
            set({ isLoading: false, error: message })
            throw error
          }
        },

        // Logout action
        logout: async () => {
          ++authOperation
          // Start revocation with the old token, then invalidate local identity
          // immediately. A slow logout must never clear a later login's token.
          const revocation = authApi.logout()
          invalidateChatIdentity()
          switchChatAccount(null)
          clearAuthToken()
          set({ user: null, isAuthenticated: false, isLoading: false, error: null })
          try { await revocation }
          catch (error) { console.error('[AUTH] Logout error:', error) }
        },

        // Check current authentication status
        checkAuth: async () => {
          const operation = ++authOperation
          const token = getAuthToken()
          if (!token) {
            switchChatAccount(null)
            set({ user: null, isAuthenticated: false })
            return
          }

          set({ isLoading: true })
          try {
            const user = await authApi.getCurrentUser()
            if (operation !== authOperation) return
            migrateLegacyChatStateForVerifiedAccount(user.id)
            switchChatAccount(user.id)
            set({
              user,
              isAuthenticated: true,
              isLoading: false
            })
          } catch (error) {
            if (operation !== authOperation) return
            console.error('[AUTH] Auth check failed:', error)
            switchChatAccount(null)
            clearAuthToken()
            set({
              user: null,
              isAuthenticated: false,
              isLoading: false
            })
          }
        },

        // Clear error
        clearError: () => set({ error: null }),
      }),
      {
        name: getWorkspaceScopedStorageKey('auth-storage'),
        partialize: (state) => ({
          // Only persist user data, not loading/error states
          user: state.user,
          isAuthenticated: state.isAuthenticated,
        }),
      }
    )
)

// Account changes in another window require a fresh authenticated bootstrap.
// Reload also drops old component callbacks and prevents any stale auth-store
// write from publishing the previous user back into shared localStorage.
if (typeof window !== 'undefined') {
  window.addEventListener('storage', event => {
    if (event.key !== getWorkspaceScopedStorageKey('auth-storage')) return
    let nextOwner: string | null = null
    try {
      const saved = event.newValue ? JSON.parse(event.newValue).state : null
      nextOwner = saved?.isAuthenticated ? saved.user?.id ?? null : null
    } catch { /* Invalid auth state is handled by the fresh bootstrap. */ }
    if (nextOwner === (useAuthStore.getState().user?.id ?? null)) return
    invalidateChatIdentity()
    switchChatAccount(null)
    window.location.reload()
  })
}
