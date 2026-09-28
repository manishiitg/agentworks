import { useEffect, useRef, useState } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { Loader2, Power, RefreshCw, SquareTerminal } from 'lucide-react'
import api, { getApiBaseUrl, getAuthToken } from '../../services/api'

type ShellState = 'connecting' | 'connected' | 'closed' | 'stopped'

function shellStreamUrl(projectId: string, cols: number, rows: number): string {
  const httpBase = getApiBaseUrl() || window.location.origin
  const url = new URL(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stream`, httpBase.replace(/^http/i, 'ws'))
  url.searchParams.set('cols', String(cols))
  url.searchParams.set('rows', String(rows))
  const token = getAuthToken()
  if (token) url.searchParams.set('token', token)
  return url.toString()
}

// A Code workspace's plain shell: bash in the same sandbox as the agent's
// shell tool (this workspace's files, private /tmp), one per person with
// editor access. It keeps running while the panel is closed and stops after
// 30 minutes with nobody watching, or on Stop.
export function CodeShellPanel({ projectId }: { projectId: string }) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<ShellState>('connecting')
  const [generation, setGeneration] = useState(0)

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const term = new XTerm({ cursorBlink: true, fontSize: 13, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', scrollback: 5000 })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    try { fit.fit() } catch { /* hidden pane */ }
    setState('connecting')
    const socket = new WebSocket(shellStreamUrl(projectId, term.cols || 80, term.rows || 24))
    socket.binaryType = 'arraybuffer'
    const encoder = new TextEncoder()
    socket.onopen = () => { setState('connected'); term.focus() }
    socket.onmessage = event => {
      term.write(event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : String(event.data))
    }
    socket.onclose = () => setState(current => current === 'stopped' ? current : 'closed')
    const input = term.onData(data => {
      if (socket.readyState === WebSocket.OPEN) socket.send(encoder.encode(data))
    })
    const resize = term.onResize(({ cols, rows }) => {
      if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', cols, rows }))
    })
    const observer = new ResizeObserver(() => { try { fit.fit() } catch { /* hidden pane */ } })
    observer.observe(host)
    return () => {
      observer.disconnect()
      input.dispose()
      resize.dispose()
      socket.close()
      term.dispose()
    }
  }, [projectId, generation])

  const stop = async () => {
    setState('stopped')
    try {
      await api.post(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stop`)
    } catch {
      // The shell also stops on its own after 30 idle minutes.
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex items-center gap-2 border-b border-border px-3 py-1.5 text-xs text-muted-foreground">
        <SquareTerminal className="h-3.5 w-3.5" />
        <span className="min-w-0 flex-1 truncate">
          Shell · this workspace’s files, sandboxed
          {state === 'connecting' ? <><Loader2 className="ml-2 inline h-3 w-3 animate-spin" /> connecting…</> : null}
          {state === 'closed' ? ' · disconnected' : null}
          {state === 'stopped' ? ' · stopped' : null}
        </span>
        {state === 'closed' || state === 'stopped' ? (
          <button type="button" onClick={() => setGeneration(value => value + 1)} className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 hover:bg-muted hover:text-foreground">
            <RefreshCw className="h-3 w-3" /> {state === 'stopped' ? 'Start' : 'Reconnect'}
          </button>
        ) : (
          <button type="button" onClick={() => { void stop() }} title="Stop the shell and everything it started" className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 hover:bg-muted hover:text-foreground">
            <Power className="h-3 w-3" /> Stop
          </button>
        )}
      </div>
      <div ref={hostRef} className="min-h-0 flex-1 bg-[#0b0e14] p-1" data-testid="code-shell" />
    </div>
  )
}
