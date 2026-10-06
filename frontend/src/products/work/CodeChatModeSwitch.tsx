import { useState } from 'react'
import { Button } from '../../components/ui/Button'
import { useCodeFilesPreference, writeCodeFilesPreference } from './codeLocalFiles'

/** Changes this chat's tool policy; the selected model and conversation stay. */
export function CodeChatModeSwitch({ sessionId, disabled = false }: { sessionId: string; disabled?: boolean }) {
  const preference = useCodeFilesPreference(sessionId)
  const local = preference.location === 'computer'
  const [error, setError] = useState('')
  const select = (mode: 'server' | 'local') => {
    try {
      writeCodeFilesPreference(sessionId, mode === 'server' ? { location: 'server' } : preference.location === 'computer' ? preference : { location: 'computer' })
      setError('')
    } catch { setError('Could not save this chat mode.') }
  }
  return <div className="mb-2 flex flex-wrap items-center gap-2">
    <div role="group" aria-label="Code chat mode" className="inline-flex gap-1 rounded-md border border-border p-0.5">
      <Button type="button" size="sm" variant={!local ? 'secondary' : 'ghost'} aria-pressed={!local} disabled={disabled || !sessionId} onClick={() => select('server')}>Server</Button>
      <Button type="button" size="sm" variant={local ? 'secondary' : 'ghost'} aria-pressed={local} disabled={disabled || !sessionId} onClick={() => select('local')}>Local</Button>
    </div>
    <span className="text-xs text-muted-foreground">{local ? 'Minimal tools · local files via CLI' : 'Website tools and server files'}</span>
    {error && <span role="alert" className="text-xs text-destructive">{error}</span>}
  </div>
}
