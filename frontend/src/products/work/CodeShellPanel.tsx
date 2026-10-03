import { useCallback, useEffect, useRef, useState } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'
import '@xterm/xterm/css/xterm.css'
import { ChevronDown, ChevronUp, ClipboardPaste, Copy, Eraser, Loader2, Maximize2, Minimize2, Minus, Plus, Power, RefreshCw, Search, Terminal as TerminalIcon, X } from 'lucide-react'
import api, { getApiBaseUrl, getAuthToken } from '../../services/api'
import { useTheme } from '../../hooks/useTheme'
import { RAW_XTERM_FONT_FAMILY, RAW_XTERM_FONT_SIZE, RAW_XTERM_THEMES } from '../../components/TerminalCenter'
import {
  SHELL_FONT_SIZE_KEY,
  SHELL_RECONNECT_ATTEMPTS,
  clampShellFontSize,
  isOpenableTerminalLink,
  readShellFontSize,
  shellReconnectDelayMs,
  shellStreamUrl,
} from './codeShellPanelHelpers'

type ShellState = 'connecting' | 'connected' | 'reconnecting' | 'closed' | 'stopped'

const SEARCH_DECORATIONS = {
  matchBackground: '#facc1540',
  matchBorder: '#facc15aa',
  matchOverviewRuler: '#facc15',
  activeMatchBackground: '#f97316aa',
  activeMatchBorder: '#f97316',
  activeMatchColorOverviewRuler: '#f97316',
}

function storage(): Storage | undefined {
  try { return window.localStorage } catch { return undefined }
}

const toolbarButton = 'inline-flex h-6 w-6 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-40'

// A Code workspace's terminal: a real shell in this Code's folder, run as your own account where the server has per-user accounts
// (see docs/DECISIONS.md, 2026-10-03). It looks like the coding-tool terminals (same theme and font) and adds the usual comforts:
// clickable links, search (Ctrl/Cmd+F), copy and paste, font size, fullscreen, and a quiet reconnect. It keeps running while the panel
// is closed and stops after 30 idle minutes, on Stop, or when the Code is deleted.
export function CodeShellPanel({ projectId }: { projectId: string }) {
  const { theme } = useTheme()
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const searchRef = useRef<SearchAddon | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const attemptRef = useRef(0)
  const themeRef = useRef(theme)
  const fontSizeRef = useRef(readShellFontSize(RAW_XTERM_FONT_SIZE, storage()))
  const [state, setState] = useState<ShellState>('connecting')
  const [generation, setGeneration] = useState(0)
  const [fontSize, setFontSize] = useState(fontSizeRef.current)
  const [expanded, setExpanded] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)
  const [query, setQuery] = useState('')
  const searchInputRef = useRef<HTMLInputElement>(null)
  themeRef.current = theme

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let disposed = false
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    const term = new XTerm({
      allowProposedApi: true,
      cursorBlink: true,
      fontFamily: RAW_XTERM_FONT_FAMILY,
      fontSize: fontSizeRef.current,
      fontWeight: 400,
      fontWeightBold: 600,
      scrollback: 10000,
      macOptionIsMeta: true,
      theme: RAW_XTERM_THEMES[themeRef.current],
    })
    const fit = new FitAddon()
    const search = new SearchAddon()
    term.loadAddon(fit)
    term.loadAddon(search)
    term.loadAddon(new Unicode11Addon())
    term.loadAddon(new WebLinksAddon((event, uri) => {
      event.preventDefault()
      if (isOpenableTerminalLink(uri)) window.open(uri, '_blank', 'noopener,noreferrer')
    }))
    term.open(host)
    term.unicode.activeVersion = '11'
    try {
      // Drawn by the GPU when it can; xterm falls back to its own renderer if the context is lost or WebGL is unavailable.
      const webgl = new WebglAddon()
      webgl.onContextLoss(() => webgl.dispose())
      term.loadAddon(webgl)
    } catch { /* no WebGL: the default renderer is fine */ }
    try { fit.fit() } catch { /* hidden pane */ }
    termRef.current = term
    searchRef.current = search

    term.attachCustomKeyEventHandler(event => {
      if (event.type === 'keydown' && (event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        setSearchOpen(true)
        requestAnimationFrame(() => searchInputRef.current?.focus())
        return false
      }
      return true
    })

    const encoder = new TextEncoder()
    const connect = () => {
      if (disposed) return
      const socket = new WebSocket(shellStreamUrl(projectId, term.cols || 80, term.rows || 24, getApiBaseUrl() || window.location.origin, getAuthToken()))
      socket.binaryType = 'arraybuffer'
      socketRef.current = socket
      socket.onopen = () => {
        attemptRef.current = 0
        setState('connected')
        term.focus()
        // The size may have changed while connecting.
        try { fit.fit() } catch { /* hidden pane */ }
      }
      socket.onmessage = event => {
        term.write(event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : String(event.data))
      }
      socket.onclose = () => {
        if (disposed) return
        setState(current => {
          if (current === 'stopped') return current
          if (attemptRef.current < SHELL_RECONNECT_ATTEMPTS) {
            reconnectTimer = setTimeout(connect, shellReconnectDelayMs(attemptRef.current))
            attemptRef.current += 1
            return 'reconnecting'
          }
          return 'closed'
        })
      }
    }
    setState('connecting')
    attemptRef.current = 0
    connect()

    const input = term.onData(data => {
      const socket = socketRef.current
      if (socket && socket.readyState === WebSocket.OPEN) socket.send(encoder.encode(data))
    })
    const resize = term.onResize(({ cols, rows }) => {
      const socket = socketRef.current
      if (socket && socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: 'resize', cols, rows }))
    })
    const observer = new ResizeObserver(() => { try { fit.fit() } catch { /* hidden pane */ } })
    observer.observe(host)
    return () => {
      disposed = true
      if (reconnectTimer) clearTimeout(reconnectTimer)
      observer.disconnect()
      input.dispose()
      resize.dispose()
      socketRef.current?.close()
      socketRef.current = null
      termRef.current = null
      searchRef.current = null
      term.dispose()
    }
  }, [projectId, generation])

  // The app's light/dark switch recolors the terminal in place.
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = RAW_XTERM_THEMES[theme]
  }, [theme])

  const changeFontSize = useCallback((delta: number) => {
    const next = clampShellFontSize(fontSizeRef.current + delta, RAW_XTERM_FONT_SIZE)
    fontSizeRef.current = next
    setFontSize(next)
    if (termRef.current) termRef.current.options.fontSize = next
    try { storage()?.setItem(SHELL_FONT_SIZE_KEY, String(next)) } catch { /* private window */ }
  }, [])

  const runSearch = useCallback((direction: 'next' | 'previous', text = query) => {
    const addon = searchRef.current
    if (!addon || !text) return
    const options = { decorations: SEARCH_DECORATIONS }
    if (direction === 'next') addon.findNext(text, options)
    else addon.findPrevious(text, options)
  }, [query])

  const closeSearch = () => {
    setSearchOpen(false)
    searchRef.current?.clearDecorations()
    termRef.current?.focus()
  }

  const copySelection = async () => {
    const text = termRef.current?.getSelection()
    if (text) {
      try { await navigator.clipboard.writeText(text) } catch { /* clipboard blocked */ }
    }
    termRef.current?.focus()
  }

  const pasteClipboard = async () => {
    try {
      const text = await navigator.clipboard.readText()
      if (text) termRef.current?.paste(text)
    } catch { /* clipboard blocked: Ctrl/Cmd+V in the terminal still works */ }
    termRef.current?.focus()
  }

  const stop = async () => {
    setState('stopped')
    socketRef.current?.close()
    try {
      await api.post(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stop`)
    } catch {
      // The shell also stops on its own after 30 idle minutes.
    }
  }

  const statusLabel = state === 'connecting' ? 'Connecting…'
    : state === 'reconnecting' ? 'Reconnecting…'
      : state === 'closed' ? 'Disconnected'
        : state === 'stopped' ? 'Stopped'
          : 'Connected'
  const dot = state === 'connected' ? 'bg-emerald-500' : state === 'connecting' || state === 'reconnecting' ? 'bg-amber-500' : 'bg-muted-foreground/60'
  const themeBackground = RAW_XTERM_THEMES[theme].background

  return (
    <div className={`flex min-h-0 flex-col bg-background ${expanded ? 'fixed inset-0 z-50' : 'h-full'}`} data-testid="code-shell-panel">
      <div className="flex items-center gap-1.5 border-b border-border px-3 py-1.5 text-xs text-muted-foreground">
        <TerminalIcon className="h-3.5 w-3.5 shrink-0" />
        <span className="font-medium text-foreground">Terminal</span>
        <span className="flex items-center gap-1.5" aria-live="polite">
          <span className={`h-1.5 w-1.5 rounded-full ${dot}`} aria-hidden />
          {state === 'connecting' || state === 'reconnecting' ? <Loader2 className="h-3 w-3 animate-spin" aria-hidden /> : null}
          {statusLabel}
        </span>
        <span className="hidden min-w-0 flex-1 truncate sm:block">· runs in this Code’s folder</span>
        <span className="flex-1 sm:hidden" />
        <button type="button" className={toolbarButton} title="Search (Ctrl/Cmd+F)" aria-label="Search the terminal" onClick={() => { setSearchOpen(open => !open); requestAnimationFrame(() => searchInputRef.current?.focus()) }}><Search className="h-3.5 w-3.5" /></button>
        <button type="button" className={toolbarButton} title="Copy the selection" aria-label="Copy the selection" onClick={() => { void copySelection() }}><Copy className="h-3.5 w-3.5" /></button>
        <button type="button" className={toolbarButton} title="Paste" aria-label="Paste" onClick={() => { void pasteClipboard() }}><ClipboardPaste className="h-3.5 w-3.5" /></button>
        <button type="button" className={toolbarButton} title="Clear the screen" aria-label="Clear the screen" onClick={() => { termRef.current?.clear(); termRef.current?.focus() }}><Eraser className="h-3.5 w-3.5" /></button>
        <span className="mx-0.5 h-4 w-px bg-border" aria-hidden />
        <button type="button" className={toolbarButton} title="Smaller text" aria-label="Smaller text" disabled={fontSize <= 10} onClick={() => changeFontSize(-1)}><Minus className="h-3.5 w-3.5" /></button>
        <span className="w-6 text-center tabular-nums" aria-label={`Text size ${fontSize}`}>{fontSize}</span>
        <button type="button" className={toolbarButton} title="Larger text" aria-label="Larger text" disabled={fontSize >= 22} onClick={() => changeFontSize(1)}><Plus className="h-3.5 w-3.5" /></button>
        <button type="button" className={toolbarButton} title={expanded ? 'Exit full screen' : 'Full screen'} aria-label={expanded ? 'Exit full screen' : 'Full screen'} onClick={() => setExpanded(value => !value)}>{expanded ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}</button>
        <span className="mx-0.5 h-4 w-px bg-border" aria-hidden />
        {state === 'closed' || state === 'stopped' ? (
          <button type="button" onClick={() => setGeneration(value => value + 1)} className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 hover:bg-muted hover:text-foreground">
            <RefreshCw className="h-3 w-3" /> {state === 'stopped' ? 'Start' : 'Reconnect'}
          </button>
        ) : (
          <button type="button" onClick={() => { void stop() }} title="Stop the terminal and everything it started" className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 hover:bg-muted hover:text-foreground">
            <Power className="h-3 w-3" /> Stop
          </button>
        )}
      </div>
      {searchOpen && (
        <div className="flex items-center gap-1.5 border-b border-border bg-muted/40 px-3 py-1" role="search">
          <Search className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
          <input
            ref={searchInputRef}
            value={query}
            onChange={event => { setQuery(event.target.value); runSearch('next', event.target.value) }}
            onKeyDown={event => {
              if (event.key === 'Enter') { event.preventDefault(); runSearch(event.shiftKey ? 'previous' : 'next') }
              if (event.key === 'Escape') { event.preventDefault(); closeSearch() }
            }}
            placeholder="Find in the terminal"
            aria-label="Find in the terminal"
            className="h-6 min-w-0 flex-1 bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground"
          />
          <button type="button" className={toolbarButton} title="Previous match (Shift+Enter)" aria-label="Previous match" onClick={() => runSearch('previous')}><ChevronUp className="h-3.5 w-3.5" /></button>
          <button type="button" className={toolbarButton} title="Next match (Enter)" aria-label="Next match" onClick={() => runSearch('next')}><ChevronDown className="h-3.5 w-3.5" /></button>
          <button type="button" className={toolbarButton} title="Close (Esc)" aria-label="Close search" onClick={closeSearch}><X className="h-3.5 w-3.5" /></button>
        </div>
      )}
      <div ref={hostRef} className="min-h-0 flex-1 p-1.5" style={{ backgroundColor: themeBackground }} data-testid="code-shell" />
    </div>
  )
}
