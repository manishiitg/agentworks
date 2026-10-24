import { useEffect, useRef, useState } from 'react'
import api from '../services/api'

export default function MCPAccountSignIn({ requestId }: { requestId: string }) {
  const started = useRef(false)
  const [error, setError] = useState('')
  useEffect(() => {
    if (started.current) return
    started.current = true
    void api.post<{ url: string }>(`/api/mcp/elicitation/${encodeURIComponent(requestId)}/open`)
      .then(({ data }) => window.location.replace(data.url))
      .catch((err: unknown) => {
        const response = (err as { response?: { data?: { error?: { message?: string } | string } } }).response?.data?.error
        setError(typeof response === 'string' ? response : response?.message || 'This sign-in link is no longer available. Return to your AI chat and ask to connect again.')
      })
  }, [requestId])
  return <main className="flex min-h-screen items-center justify-center bg-background p-6 text-foreground">
    <div className="max-w-md space-y-3 text-center">
      <h1 className="text-xl font-semibold">Connect your account</h1>
      <p role={error ? 'alert' : 'status'}>{error || 'Opening account sign-in…'}</p>
    </div>
  </main>
}
