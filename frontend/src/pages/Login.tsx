import { useState, useEffect } from 'react'
import { useAuthStore } from '../stores/useAuthStore'
import { Button } from '../components/ui/Button'
import { Input } from '../components/ui/Input'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '../components/ui/Card'
import { Alert, AlertDescription } from '../components/ui/alert'
import { Loader2, KeyRound, Mail, User, Lock, AlertCircle } from 'lucide-react'
import type { AuthProvider } from '../services/api'
import { RunloopMark } from '../components/branding/RunloopLogo'
import { getRuntimeAppName, runtimeBrandAsset } from '../runtime-branding'

const runtimeAppName =
  getRuntimeAppName(
    typeof window !== 'undefined'
      ? (window as Window & { __APP_RUNTIME_CONFIG__?: { appName?: unknown } }).__APP_RUNTIME_CONFIG__
      : undefined
  ) ?? 'AgentWorks'

// AWS Cognito icon (official mark)
const CognitoIcon = () => (
  <svg className="w-5 h-5" viewBox="0 0 40 40" xmlns="http://www.w3.org/2000/svg">
    <defs>
      <linearGradient x1="0%" y1="100%" x2="100%" y2="0%" id="cognito-a">
        <stop stopColor="#BD0816" offset="0%"/>
        <stop stopColor="#FF5252" offset="100%"/>
      </linearGradient>
    </defs>
    <g fill="none" fillRule="evenodd">
      <path d="M0 0h40v40H0z" fill="url(#cognito-a)"/>
      <path d="M20 8c-4.42 0-8 3.58-8 8 0 2.8 1.44 5.26 3.62 6.7L14 28h12l-1.62-5.3A7.97 7.97 0 0028 16c0-4.42-3.58-8-8-8zm0 2a6 6 0 110 12 6 6 0 010-12zm-4 20h8v2h-8v-2z" fill="#FFF"/>
    </g>
  </svg>
)

// Supabase icon (official mark)
const SupabaseIcon = () => (
  <svg className="w-5 h-5" viewBox="0 0 109 113" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M63.7076 110.284C60.8481 113.885 55.0502 111.912 54.9813 107.314L53.9738 40.0627H99.1935C108.384 40.0627 113.269 51.2094 107.028 57.7512L63.7076 110.284Z" fill="url(#paint0_linear)"/>
    <path d="M63.7076 110.284C60.8481 113.885 55.0502 111.912 54.9813 107.314L53.9738 40.0627H99.1935C108.384 40.0627 113.269 51.2094 107.028 57.7512L63.7076 110.284Z" fill="url(#paint1_linear)" fillOpacity="0.2"/>
    <path d="M45.317 2.07103C48.1765 -1.53037 53.9745 0.442937 54.0434 5.041L54.4849 72.2922H9.83113C0.640869 72.2922 -4.24459 61.1455 1.99622 54.6037L45.317 2.07103Z" fill="#3ECF8E"/>
    <defs>
      <linearGradient id="paint0_linear" x1="53.9738" y1="54.974" x2="94.1635" y2="71.8295" gradientUnits="userSpaceOnUse">
        <stop stopColor="#249361"/>
        <stop offset="1" stopColor="#3ECF8E"/>
      </linearGradient>
      <linearGradient id="paint1_linear" x1="36.1558" y1="30.578" x2="54.4844" y2="65.0806" gradientUnits="userSpaceOnUse">
        <stop/>
        <stop offset="1" stopOpacity="0"/>
      </linearGradient>
    </defs>
  </svg>
)

// Google "G" icon (official mark colors)
const GoogleIcon = () => (
  <svg className="w-5 h-5" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
    <path fill="#4285F4" d="M23.49 12.27c0-.79-.07-1.54-.19-2.27H12v4.51h6.47c-.29 1.48-1.14 2.73-2.4 3.58v3h3.86c2.26-2.09 3.56-5.17 3.56-8.82z"/>
    <path fill="#34A853" d="M12 24c3.24 0 5.95-1.08 7.93-2.91l-3.86-3c-1.08.72-2.45 1.16-4.07 1.16-3.13 0-5.78-2.11-6.73-4.96H1.29v3.09C3.26 21.3 7.31 24 12 24z"/>
    <path fill="#FBBC05" d="M5.27 14.29c-.25-.72-.38-1.49-.38-2.29s.14-1.57.38-2.29V6.62H1.29C.47 8.24 0 10.06 0 12s.47 3.76 1.29 5.38l3.98-3.09z"/>
    <path fill="#EA4335" d="M12 4.75c1.77 0 3.35.61 4.6 1.8l3.42-3.42C17.95 1.19 15.24 0 12 0 7.31 0 3.26 2.7 1.29 6.62l3.98 3.09C6.22 6.86 8.87 4.75 12 4.75z"/>
  </svg>
)

// Provider display configuration
const providerConfig: Record<string, {
  displayName: string
  icon: React.ReactNode
  description: string
  buttonClass?: string
}> = {
  simple: {
    displayName: 'Username & Password',
    icon: <KeyRound className="w-5 h-5" />,
    description: 'Sign in with your credentials'
  },
  cognito: {
    displayName: 'AWS Cognito',
    icon: <CognitoIcon />,
    description: 'Sign in with your work account via AWS Cognito',
    buttonClass: 'bg-white hover:bg-gray-50 text-gray-700 border border-gray-300 dark:bg-gray-800 dark:hover:bg-gray-700 dark:text-gray-200 dark:border-gray-600'
  },
  supabase: {
    displayName: 'Supabase',
    icon: <SupabaseIcon />,
    description: 'Sign in with Supabase email & password'
  },
  'supabase-google': {
    displayName: 'Google',
    icon: <GoogleIcon />,
    description: 'Sign in with your Google account',
    buttonClass: 'bg-white hover:bg-gray-50 text-gray-700 border border-gray-300 dark:bg-gray-800 dark:hover:bg-gray-700 dark:text-gray-200 dark:border-gray-600'
  }
}

// A successful sign-in leaves the browser wherever the gateway's 401 landed
// it (e.g. /login?next=%2F, set in gatewayAuth.ts's redirectToGatewayLogin).
// The app re-renders as authenticated without navigating anywhere, so
// without this the address bar keeps showing /login?next=... indefinitely.
// Same "must be an absolute in-app path" check the gateway itself applies to
// `next` (deploy/aws-ec2/server/auth-gateway.go's safeNext) -- an open value
// here would let a crafted link rewrite history to an attacker's URL.
function cleanUpLoginUrl() {
  if (typeof window === 'undefined') return
  if (['/file', '/folder', '/report', '/oauth/consent', '/oauth/cli'].includes(window.location.pathname)) return
  const next = new URLSearchParams(window.location.search).get('next')
  const target = next && next.startsWith('/') && !next.startsWith('//') && !next.includes('\\') ? next : '/'
  window.history.replaceState(null, '', target)
  window.dispatchEvent(new PopStateEvent('popstate'))
}

export function Login() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [selectedProvider, setSelectedProvider] = useState<string | null>(null)
  const {
    login,
    loginWithOAuth,
    isLoading,
    error,
    clearError,
    providers,
    checkAuthMode,
    isMultiUserModeChecked
  } = useAuthStore()

  // Check auth mode on mount to get available providers
  useEffect(() => {
    if (!isMultiUserModeChecked) {
      checkAuthMode()
    }
  }, [checkAuthMode, isMultiUserModeChecked])

  // Auto-select provider if only one is available
  useEffect(() => {
    if (providers.length === 1) {
      setSelectedProvider(providers[0].name)
    }
  }, [providers])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    clearError()
    try {
      await login(username, password, selectedProvider || undefined)
      cleanUpLoginUrl()
    } catch (err) {
      console.error('Login failed:', err)
    }
  }

  const handleOAuthLogin = async (provider: AuthProvider) => {
    clearError()
    try {
      await loginWithOAuth(provider.name)
      cleanUpLoginUrl()
    } catch (err) {
      console.error('OAuth login failed:', err)
    }
  }

  // Get credentials-based providers
  const credentialsProviders = providers.filter(p => p.type === 'credentials')
  const oauthProviders = providers.filter(p => p.type === 'oauth')

  // Check if we should show credentials form
  const showCredentialsForm = selectedProvider &&
    credentialsProviders.some(p => p.name === selectedProvider)

  // The credentials provider actually in effect right now -- either explicitly
  // selected, or the sole credentials provider when there's nothing to choose
  // between. Drives whether the identity field asks for an email or a plain
  // username (the "simple" AUTH_USERS provider has no email at all).
  const activeCredentialProviderName = showCredentialsForm
    ? selectedProvider
    : credentialsProviders.length === 1
      ? credentialsProviders[0].name
      : null
  const isSimpleCredentialProvider = activeCredentialProviderName === 'simple'

  // Loading state
  if (!isMultiUserModeChecked) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-background to-muted">
        <div className="flex flex-col items-center gap-4">
          <Loader2 className="w-8 h-8 animate-spin text-primary" />
          <p className="text-muted-foreground">Loading...</p>
        </div>
      </div>
    )
  }

  // No providers configured
  if (providers.length === 0) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-background to-muted p-4">
        <Card className="w-full max-w-md">
          <CardHeader className="text-center">
            <div className="mx-auto w-16 h-16 bg-destructive/10 rounded-full flex items-center justify-center mb-4">
              <AlertCircle className="w-8 h-8 text-destructive" />
            </div>
            <CardTitle>Configuration Error</CardTitle>
            <CardDescription>
              No authentication providers have been configured. Please contact your administrator.
            </CardDescription>
          </CardHeader>
        </Card>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-background to-muted p-4">
      <Card className="w-full max-w-md shadow-lg">
        <CardHeader className="text-center pb-2">
          {/* Logo / Branding */}
          <div className="mx-auto mb-4 flex h-20 w-20 items-center justify-center rounded-[1.75rem] bg-slate-950/95 shadow-[0_24px_60px_-30px_rgba(15,23,42,0.95)] ring-1 ring-slate-700/40">
            {runtimeBrandAsset('markUrl')
              ? <img src={runtimeBrandAsset('markUrl') ?? ''} alt={runtimeAppName} className="h-12 w-12" />
              : <RunloopMark className="h-14 w-14" />}
          </div>
          <CardTitle className="text-2xl">Welcome Back</CardTitle>
          <CardDescription className="text-base">
            Sign in to continue to {runtimeAppName}
            {providers.length > 0 && (
              <span className="block text-xs text-muted-foreground/70 mt-1">
                via {providers.map(p => (providerConfig[p.name]?.displayName || p.name)).join(', ')}
              </span>
            )}
          </CardDescription>
        </CardHeader>

        <CardContent className="space-y-4">
          {/* Error Alert */}
          {error && (
            <Alert variant="destructive">
              <AlertCircle className="h-4 w-4" />
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          {/* OAuth Providers */}
          {oauthProviders.length > 0 && (
            <div className="space-y-3">
              {oauthProviders.map((provider) => {
                const config = providerConfig[provider.name] || {
                  displayName: provider.name,
                  icon: <KeyRound className="w-5 h-5" />,
                  description: `Sign in with ${provider.name}`
                }
                return (
                  <Button
                    key={provider.name}
                    onClick={() => handleOAuthLogin(provider)}
                    className={`w-full h-12 text-base font-medium gap-3 ${config.buttonClass || ''}`}
                    disabled={isLoading}
                    variant={config.buttonClass ? undefined : 'outline'}
                  >
                    {isLoading ? (
                      <Loader2 className="w-5 h-5 animate-spin" />
                    ) : (
                      config.icon
                    )}
                    {isLoading ? 'Redirecting...' : config.displayName}
                  </Button>
                )
              })}
            </div>
          )}

          {/* Divider */}
          {oauthProviders.length > 0 && credentialsProviders.length > 0 && (
            <div className="relative">
              <div className="absolute inset-0 flex items-center">
                <span className="w-full border-t border-border" />
              </div>
              <div className="relative flex justify-center text-xs uppercase">
                <span className="bg-card px-2 text-muted-foreground">
                  or continue with
                </span>
              </div>
            </div>
          )}

          {/* Provider Selection (if multiple credentials providers) */}
          {credentialsProviders.length > 1 && !showCredentialsForm && (
            <div className="space-y-3">
              {credentialsProviders.map((provider) => {
                const config = providerConfig[provider.name] || {
                  displayName: provider.name,
                  icon: <KeyRound className="w-5 h-5" />,
                  description: `Sign in with ${provider.name}`
                }
                return (
                  <Button
                    key={provider.name}
                    onClick={() => setSelectedProvider(provider.name)}
                    className="w-full h-11"
                    variant="outline"
                  >
                    {config.icon}
                    {config.displayName}
                  </Button>
                )
              })}
            </div>
          )}

          {/* Credentials Form */}
          {(credentialsProviders.length === 1 || showCredentialsForm) && (
            <form className="space-y-4" onSubmit={handleSubmit}>
              {/* Back button if multiple providers */}
              {credentialsProviders.length > 1 && (
                <button
                  type="button"
                  onClick={() => setSelectedProvider(null)}
                  className="text-sm text-primary hover:underline inline-flex items-center gap-1"
                >
                  <span>&larr;</span> Back to provider selection
                </button>
              )}

              <div className="space-y-4">
                <div className="space-y-2">
                  <label htmlFor="username" className="text-sm font-medium text-foreground">
                    {isSimpleCredentialProvider ? 'Username' : 'Email'}
                  </label>
                  <div className="relative">
                    {isSimpleCredentialProvider ? (
                      <User className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                    ) : (
                      <Mail className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                    )}
                    <Input
                      id="username"
                      name="username"
                      type={isSimpleCredentialProvider ? 'text' : 'email'}
                      autoComplete={isSimpleCredentialProvider ? 'username' : 'email'}
                      required
                      value={username}
                      onChange={(e) => setUsername(e.target.value)}
                      className="pl-10 h-11"
                      placeholder={isSimpleCredentialProvider ? 'Enter your username' : 'you@example.com'}
                    />
                  </div>
                </div>

                <div className="space-y-2">
                  <label htmlFor="password" className="text-sm font-medium text-foreground">
                    Password
                  </label>
                  <div className="relative">
                    <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                    <Input
                      id="password"
                      name="password"
                      type="password"
                      autoComplete="current-password"
                      required
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      className="pl-10 h-11"
                      placeholder="Enter your password"
                    />
                  </div>
                </div>
              </div>

              <Button
                type="submit"
                className="w-full h-11 text-base font-medium"
                disabled={isLoading}
              >
                {isLoading ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" />
                    Signing in...
                  </>
                ) : (
                  'Sign in'
                )}
              </Button>
            </form>
          )}
        </CardContent>

        <CardFooter className="flex flex-col gap-2 text-center text-sm text-muted-foreground">
          <p>
            By signing in, you agree to our terms of service and privacy policy.
          </p>
        </CardFooter>
      </Card>
    </div>
  )
}
