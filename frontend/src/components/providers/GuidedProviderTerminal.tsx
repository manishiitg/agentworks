import { useEffect, useRef, useState } from 'react'
import { ExternalLink, Loader2, RotateCcw, Square, X } from 'lucide-react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import {
  llmConfigService,
  type ProviderSetupSession,
} from '../../services/llm-config-api'

interface GuidedProviderTerminalProps {
  session: ProviderSetupSession
  onFinished: (session: ProviderSetupSession) => void
  onClose: () => void
}

const terminalTheme = {
  background: '#090d16',
  foreground: '#e5e7eb',
  cursor: '#a78bfa',
  selectionBackground: '#6d28d955',
  black: '#111827',
  red: '#f87171',
  green: '#34d399',
  yellow: '#fbbf24',
  blue: '#60a5fa',
  magenta: '#c084fc',
  cyan: '#22d3ee',
  white: '#f3f4f6',
}

const PROVIDER_NAMES: Record<string, string> = {
  'claude-code': 'Claude Code',
  'codex-cli': 'Codex',
  'cursor-cli': 'Cursor',
  'pi-cli': 'Pi',
  'muse-cli': 'Muse',
}

const providerName = (provider: string) => PROVIDER_NAMES[provider] || provider

const latestURL = (text: string): string | null => {
  // Replace terminal ESC bytes before matching so ANSI colour sequences do
  // not become part of a detected login URL.
  const printableText = Array.from(text, character => character.charCodeAt(0) === 27 ? ' ' : character).join('')
  const matches = printableText.match(/https?:\/\/[^\s<>"']+/g)
  if (!matches?.length) return null
  return matches[matches.length - 1].replace(/[),.;]+$/, '')
}

export default function GuidedProviderTerminal({ session, onFinished, onClose }: GuidedProviderTerminalProps) {
  const displayName = providerName(session.provider)
  const isInspection = session.action === 'inspect'
  const isUsage = session.action === 'usage'
  const isInstallation = session.action === 'install'
  const mountRef = useRef<HTMLDivElement | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const sessionRef = useRef(session)
  const onFinishedRef = useRef(onFinished)
  const [connection, setConnection] = useState<'connecting' | 'live' | 'finished' | 'disconnected'>('connecting')
  const [loginURL, setLoginURL] = useState<string | null>(null)
  const [cancelling, setCancelling] = useState(false)
  const [terminalError, setTerminalError] = useState<string | null>(null)
  const [reconnectToken, setReconnectToken] = useState(0)

  useEffect(() => {
    sessionRef.current = session
  }, [session])

  useEffect(() => {
    onFinishedRef.current = onFinished
  }, [onFinished])

  useEffect(() => {
    const mount = mountRef.current
    if (!mount) return

    const terminal = new XTerm({
      cursorBlink: true,
      convertEol: true,
      fontFamily: 'JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 12,
      lineHeight: 1.2,
      scrollback: 5000,
      theme: terminalTheme,
    })
    const fit = new FitAddon()
    terminal.loadAddon(fit)
    terminal.open(mount)

    let disposed = false
    let outputText = ''
    let resizeTimer: number | undefined
    const sendResize = () => {
      if (disposed || mount.clientWidth === 0 || mount.clientHeight === 0) return
      try {
        fit.fit()
      } catch {
        return
      }
      const socket = socketRef.current
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'resize', cols: terminal.cols, rows: terminal.rows }))
      }
    }
    const scheduleResize = () => {
      if (resizeTimer !== undefined) window.clearTimeout(resizeTimer)
      resizeTimer = window.setTimeout(sendResize, 80)
    }
    const resizeObserver = new ResizeObserver(scheduleResize)
    resizeObserver.observe(mount)
    scheduleResize()

    const socket = new WebSocket(llmConfigService.getProviderSetupStreamUrl(session.id))
    socket.binaryType = 'arraybuffer'
    socketRef.current = socket
    socket.onopen = () => {
      if (disposed) return
      setConnection('live')
      sendResize()
      terminal.focus()
    }
    socket.onmessage = event => {
      if (disposed) return
      if (typeof event.data === 'string') {
        try {
          const message = JSON.parse(event.data) as { type?: string; session?: ProviderSetupSession }
          if (message.type === 'exit' && message.session) {
            sessionRef.current = message.session
            setConnection('finished')
            onFinishedRef.current(message.session)
          }
        } catch {
          terminal.write(event.data)
        }
        return
      }
      const bytes = new Uint8Array(event.data as ArrayBuffer)
      terminal.write(bytes)
      outputText = `${outputText}${new TextDecoder().decode(bytes)}`.slice(-64 * 1024)
      const foundURL = latestURL(outputText)
      if (foundURL) setLoginURL(foundURL)
    }
    socket.onerror = () => {
      if (!disposed) setTerminalError('The guided terminal could not connect to the server.')
    }
    socket.onclose = () => {
      if (!disposed) {
        setConnection(current => current === 'finished' ? current : 'disconnected')
      }
    }

    const input = terminal.onData(data => {
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: 'input', data }))
      }
    })

    return () => {
      disposed = true
      if (resizeTimer !== undefined) window.clearTimeout(resizeTimer)
      resizeObserver.disconnect()
      input.dispose()
      socket.close()
      socketRef.current = null
      terminal.dispose()
    }
  }, [reconnectToken, session.id])

  const cancel = async () => {
    setCancelling(true)
    setTerminalError(null)
    try {
      await llmConfigService.cancelProviderSetup(session.id)
      setConnection('finished')
      onFinished({ ...sessionRef.current, status: 'cancelled', error: 'Cancelled by user' })
    } catch (error) {
      setTerminalError(error instanceof Error ? error.message : 'Could not cancel provider setup')
    } finally {
      setCancelling(false)
    }
  }

  const reconnect = () => {
    setTerminalError(null)
    setConnection('connecting')
    setReconnectToken(token => token + 1)
  }

  const isRunning = connection === 'connecting' || connection === 'live'

  return (
    <section className="mb-6 overflow-hidden rounded-xl border border-violet-300 bg-gray-950 shadow-lg dark:border-violet-500/50">
      <div className="flex items-center justify-between gap-3 border-b border-white/10 px-3 py-2 text-gray-100">
        <div className="flex min-w-0 items-center gap-2 text-xs">
          {connection === 'connecting' && <Loader2 className="h-3.5 w-3.5 animate-spin text-violet-300" />}
          <span className={`h-2 w-2 rounded-full ${connection === 'live' ? 'bg-emerald-400' : connection === 'finished' ? 'bg-gray-400' : 'bg-amber-400'}`} />
          <span className="truncate font-medium">{displayName} {isInstallation ? 'installation' : isUsage ? 'usage' : isInspection ? 'terminal' : 'sign-in'}</span>
          <span className="text-gray-500">·</span>
          <span className="capitalize text-gray-400">{connection}</span>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {loginURL && (
            <a
              href={loginURL}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 rounded-md bg-violet-500/20 px-2 py-1 text-xs font-medium text-violet-200 hover:bg-violet-500/30"
            >
              <ExternalLink className="h-3 w-3" /> Open login page
            </a>
          )}
          {connection === 'disconnected' && (
            <button type="button" onClick={reconnect} className="rounded p-1.5 text-gray-400 hover:bg-white/10 hover:text-white" aria-label="Reconnect guided terminal">
              <RotateCcw className="h-3.5 w-3.5" />
            </button>
          )}
          {isRunning ? (
            <button type="button" onClick={cancel} disabled={cancelling} className="rounded p-1.5 text-gray-400 hover:bg-red-500/20 hover:text-red-300 disabled:opacity-50" aria-label="Cancel provider setup">
              {cancelling ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />}
            </button>
          ) : (
            <button type="button" onClick={onClose} className="rounded p-1.5 text-gray-400 hover:bg-white/10 hover:text-white" aria-label="Close guided terminal">
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>
      {/* The account terminal is for signing in and checking usage only; real work belongs in a
          Crew, Code or workflow, where tools, files and history are set up. */}
      <p data-testid="provider-terminal-purpose" className="border-b border-white/10 bg-violet-500/10 px-3 py-2 text-[11px] leading-4 text-violet-100">
        {isInstallation ? `Installing ${displayName} on this computer. When installation finishes, connect your account.` : <>Use this terminal only to sign in and check usage{usageCommandHint(displayName)}. For real work, use a Crew, Code or workflow.</>}
      </p>
      <div ref={mountRef} className="h-[min(65vh,640px)] w-full p-2" aria-label={`Interactive ${displayName} setup terminal`} />
      {terminalError && <p className="border-t border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">{terminalError}</p>}
      <p className="border-t border-white/10 px-3 py-2 text-[11px] text-gray-500">
        {isInstallation ? `This window runs the platform's approved ${displayName} installer.` : isUsage
          ? `AgentWorks opened a restricted ${displayName} session and submitted its usage command. Close it when you finish reviewing the result.`
          : isInspection
          ? `This administrator-only window opens the approved ${displayName} CLI with the configured inspection restrictions. Use its built-in commands, then exit the CLI when finished.`
          : `This restricted setup window runs only the approved ${displayName} login flow. It does not provide shell or SSH access.`}
      </p>
    </section>
  )
}

// The CLI's own usage command, when it has one, so the note says what to type.
function usageCommandHint(displayName: string): string {
  const name = displayName.toLowerCase()
  if (name.includes('codex')) return ' (type /status)'
  if (name.includes('claude') || name.includes('muse')) return ' (type /usage)'
  return ''
}
