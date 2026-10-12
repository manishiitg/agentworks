import { productReturnPath } from '../utils/productRoutes'
import { consumeSharedReturnPath } from '../utils/sharedLinks'
import { consumeMcpConsentReturnPath } from '../utils/mcpOAuthReturn'
import { useEffect, useState } from 'react'
import { useAuthStore, peekStoredOAuthState } from '../stores/useAuthStore'

export function AuthCallback() {
  const { handleOAuthCallback, error, isAuthenticated } = useAuthStore()
  const [callbackError, setCallbackError] = useState<string | null>(null)
  const [callbackReturn, setCallbackReturn] = useState<string | null | undefined>(undefined)
  const [callbackState, setCallbackState] = useState<string | null>(null)

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code')
    // Supabase social login returns only ?code=... without echoing our
    // state; fall back to the state this tab stored when it started the flow.
    const state = params.get('state') ?? peekStoredOAuthState()
    const errorParam = params.get('error')
    const errorDescription = params.get('error_description')

    // Handle error from OAuth provider
    if (errorParam) {
      setCallbackError(errorDescription || errorParam)
      return
    }

    // Validate required parameters
    if (!code || !state) {
      setCallbackError('Missing authorization code or state parameter')
      return
    }

    // Process the callback
    setCallbackState(state)
    handleOAuthCallback(code, state)
      .then(target => setCallbackReturn(target ?? null))
      .catch((err) => {
        console.error('OAuth callback failed:', err)
        setCallbackError(err.message || 'Authentication failed')
      })
  }, [handleOAuthCallback])

  // Redirect on successful authentication
  useEffect(() => {
    if (isAuthenticated && callbackState && callbackReturn !== undefined) {
      const returnTo = productReturnPath(callbackReturn) || consumeMcpConsentReturnPath(callbackState) || consumeSharedReturnPath(callbackState)
      window.location.href = returnTo || '/'
    }
  }, [isAuthenticated, callbackState, callbackReturn])

  const displayError = callbackError || error

  const handleBackToLogin = () => {
    window.location.href = '/'
  }

  if (displayError) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900">
        <div className="max-w-md w-full space-y-8 p-8 bg-white dark:bg-gray-800 rounded-lg shadow-lg">
          <div className="text-center">
            <h2 className="text-xl font-bold text-red-600 dark:text-red-400">
              Authentication Failed
            </h2>
            <p className="mt-4 text-gray-600 dark:text-gray-400">
              {displayError}
            </p>
            <button
              onClick={handleBackToLogin}
              className="mt-6 px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 transition-colors"
            >
              Back to Login
            </button>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-100 dark:bg-gray-900">
      <div className="max-w-md w-full space-y-8 p-8 bg-white dark:bg-gray-800 rounded-lg shadow-lg">
        <div className="text-center">
          <h2 className="text-xl font-bold text-gray-900 dark:text-white">
            Completing sign in...
          </h2>
          <div className="mt-4 flex justify-center">
            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
          </div>
        </div>
      </div>
    </div>
  )
}
