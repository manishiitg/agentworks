import { useEffect, useState } from 'react'
import api from '../../services/api'

const errorText = (cause: unknown) => {
  const data = (cause as { response?: { data?: { error?: string } | string } })?.response?.data
  return typeof data === 'string' ? data : data?.error || 'The sign-in could not finish. Start it again.'
}

/** On a server with several people, an app or Google sign-in comes back here and the server finishes it for the
 * person who started it (the server decides; this only passes the sign-in along and shows the answer). */
export function OAuthReturnCompletion() {
  const [note, setNote] = useState<string | null>(null)
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const state = params.get('oauth_connect')
    if (!state) return
    params.delete('oauth_connect')
    const rest = params.toString()
    window.history.replaceState(null, '', `${window.location.pathname}${rest ? `?${rest}` : ''}${window.location.hash}`)
    setNote('Finishing sign-in…')
    api.post<{ message?: string }>('/api/oauth/complete', { state })
      .then(result => setNote(result.data.message || 'Signed in.'))
      .catch(cause => setNote(errorText(cause)))
  }, [])
  if (!note) return null
  return <div role="status" className="fixed right-4 top-16 z-50 flex max-w-md items-start gap-3 rounded-lg border border-border bg-popover p-3 text-sm text-popover-foreground shadow-lg">
    <span>{note}</span><button type="button" className="text-xs underline" onClick={() => setNote(null)}>Close</button>
  </div>
}
