import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { WebglAddon } from '@xterm/addon-webgl'
import '@xterm/xterm/css/xterm.css'
import { ChevronDown, ChevronUp, ClipboardPaste, Copy, Eraser, Loader2, Maximize2, Minimize2, Minus, MoreHorizontal, Palette, Plus, Power, RefreshCw, Search, Terminal as TerminalIcon, X } from 'lucide-react'
import api, { getApiBaseUrl, getAuthToken } from '../../services/api'
import { useTheme } from '../../hooks/useTheme'
import { RAW_XTERM_FONT_FAMILY, RAW_XTERM_FONT_SIZE, RAW_XTERM_THEMES } from '../../components/TerminalCenter'
import { SHELL_THEME_KEY, SHELL_THEME_LABELS, nextShellTheme, readShellTheme, shellTheme, type ShellThemeName } from './codeShellTheme'
import {
  SHELL_FONT_SIZE_KEY,
  SHELL_MAX_TABS,
  SHELL_RECONNECT_ATTEMPTS,
  clampShellFontSize,
  closeShellTab,
  nextShellTab,
  readShellTabs,
  shellShortcut,
  shellShortcutLabel,
  writeShellTabs,
  type ShellAction,
  type ShellTabs,
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

const IS_MAC = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

const toolbarButton = 'inline-flex h-6 w-6 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-40'

// A Code workspace's terminal: a real shell in this Code's folder, run as your own account where the server has per-user accounts
// (see docs/DECISIONS.md, 2026-10-03). It looks like the coding-tool terminals (same theme and font) and adds the usual comforts:
// clickable links, search (Ctrl/Cmd+F), copy and paste, font size, fullscreen, and a quiet reconnect. It keeps running while the panel
// is closed and stops after 30 idle minutes, on Stop, or when the Code is deleted.
// Up to SHELL_MAX_TABS terminals per Code, each its own shell (same sandbox, same home). The tab strip sits above the active one;
// the others stay connected while hidden, so switching back keeps their output. The open tabs are remembered per Code.
export function CodeShellPanel({ projectId }: { projectId: string }) {
  const [state, setState] = useState<ShellTabs>(() => readShellTabs(projectId, storage()))
  useEffect(() => { setState(readShellTabs(projectId, storage())) }, [projectId])
  const update = useCallback((next: ShellTabs) => {
    setState(next)
    writeShellTabs(projectId, next, storage())
  }, [projectId])
  const free = nextShellTab(state.tabs)
  const addTab = () => {
    if (free == null) return
    update({ tabs: [...state.tabs, free].sort((a, b) => a - b), active: free })
  }
  const closeTab = (tab: number) => {
    if (state.tabs.length <= 1) return
    update(closeShellTab(state, tab))
    // Closing a tab ends its shell (and whatever it started); the others keep running.
    void api.post(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stop${tab > 1 ? `?tab=${tab}` : ''}`).catch(() => undefined)
  }
  // Tab shortcuts typed in any terminal (new terminal, switch to 1..3).
  const onTabAction = (action: ShellAction) => {
    if (action === 'newTab') addTab()
    const wanted = action === 'tab1' ? 1 : action === 'tab2' ? 2 : action === 'tab3' ? 3 : 0
    if (wanted && state.tabs.includes(wanted)) update({ ...state, active: wanted })
  }
  const tabStrip = (
    <div className="flex min-w-0 items-center gap-0.5" role="tablist" aria-label="Terminals">
      {state.tabs.map(tab => {
        const selected = tab === state.active
        return (
          <div key={tab} className={`group flex items-center rounded-md ${selected ? 'bg-muted text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}>
            <button type="button" role="tab" aria-selected={selected} onClick={() => update({ ...state, active: tab })} className="flex items-center gap-1 whitespace-nowrap px-2 py-0.5 font-medium" data-testid={`code-shell-tab-${tab}`}>
              <TerminalIcon className="h-3 w-3" /> Terminal {tab}
            </button>
            {state.tabs.length > 1 && (
              <button type="button" onClick={() => closeTab(tab)} className="mr-1 rounded p-0.5 opacity-60 hover:bg-muted hover:opacity-100" title={`Close Terminal ${tab} (stops it)`} aria-label={`Close Terminal ${tab}`}>
                <X className="h-3 w-3" />
              </button>
            )}
          </div>
        )
      })}
      <button type="button" onClick={addTab} disabled={free == null} className={toolbarButton} title={free == null ? `At most ${SHELL_MAX_TABS} terminals per Code` : `New terminal (${shellShortcutLabel('newTab', IS_MAC)})`} aria-label="New terminal" data-testid="code-shell-new-tab">
        <Plus className="h-3.5 w-3.5" />
      </button>
    </div>
  )
  return (
    <>
      {state.tabs.map(tab => (
        <CodeShellTerminal key={`${projectId}:${tab}`} projectId={projectId} tab={tab} active={tab === state.active} tabStrip={tabStrip} onTabAction={onTabAction} />
      ))}
    </>
  )
}

function CodeShellTerminal({ projectId, tab, active, tabStrip, onTabAction }: { projectId: string; tab: number; active: boolean; tabStrip: ReactNode; onTabAction: (action: ShellAction) => void }) {
  const { theme } = useTheme()
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  // The key handler is installed once per terminal; it calls the latest actions through this ref.
  const actionRef = useRef<(action: ShellAction) => void>(() => undefined)
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const searchRef = useRef<SearchAddon | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const socketRef = useRef<WebSocket | null>(null)
  const attemptRef = useRef(0)
  const themeRef = useRef(theme)
  const fontSizeRef = useRef(readShellFontSize(RAW_XTERM_FONT_SIZE, storage()))
  const [colourScheme, setColourScheme] = useState<ShellThemeName>(() => readShellTheme(storage()))
  const schemeRef = useRef(colourScheme)
  schemeRef.current = colourScheme
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
      theme: shellTheme(schemeRef.current, RAW_XTERM_THEMES[themeRef.current]),
    })
    const fit = new FitAddon()
    fitRef.current = fit
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
      if (event.type !== 'keydown') return true
      const action = shellShortcut(event, IS_MAC)
      if (!action) return true
      // Paste on a Mac (⌘V) is the browser's own paste event, which xterm already handles.
      if (action === 'paste' && IS_MAC) return true
      event.preventDefault()
      actionRef.current(action)
      return false
    })

    const encoder = new TextEncoder()
    const connect = () => {
      if (disposed) return
      const socket = new WebSocket(shellStreamUrl(projectId, term.cols || 80, term.rows || 24, getApiBaseUrl() || window.location.origin, getAuthToken(), tab))
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

    // The wheel scrolls tmux's history on the server (tmux's mouse is off so a drag is the browser's selection and copy works).
    // Wheel deltas add up so a trackpad scrolls smoothly; the first keystroke after scrolling back returns to the prompt.
    let wheelPixels = 0
    let scrolledBack = false
    term.attachCustomWheelEventHandler(event => {
      const socket = socketRef.current
      if (!socket || socket.readyState !== WebSocket.OPEN) return false
      wheelPixels += event.deltaMode === 1 ? event.deltaY * 16 : event.deltaY
      const lines = Math.trunc(wheelPixels / 16)
      if (lines !== 0) {
        wheelPixels -= lines * 16
        if (lines < 0) scrolledBack = true
        socket.send(JSON.stringify({ type: 'scroll', lines: -lines }))
      }
      return false
    })
    const input = term.onData(data => {
      const socket = socketRef.current
      if (!socket || socket.readyState !== WebSocket.OPEN) return
      if (scrolledBack) {
        scrolledBack = false
        socket.send(JSON.stringify({ type: 'scroll', cancel: true }))
      }
      socket.send(encoder.encode(data))
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
  }, [projectId, tab, generation])

  // A tab shown again is fitted to the space it now has (a hidden one has none) and takes the keyboard.
  useEffect(() => {
    if (!active) return
    const frame = requestAnimationFrame(() => {
      try { fitRef.current?.fit() } catch { /* hidden pane */ }
      termRef.current?.focus()
    })
    return () => cancelAnimationFrame(frame)
  }, [active])

  // The colour scheme (and, for Classic, the app's light/dark switch) recolors the terminal in place.
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = shellTheme(colourScheme, RAW_XTERM_THEMES[theme])
  }, [theme, colourScheme])

  const switchColourScheme = () => {
    const next = nextShellTheme(colourScheme)
    setColourScheme(next)
    try { storage()?.setItem(SHELL_THEME_KEY, next) } catch { /* private window */ }
  }

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
      await api.post(`/api/agent-profiles/code/projects/${encodeURIComponent(projectId)}/shell/stop${tab > 1 ? `?tab=${tab}` : ''}`)
    } catch {
      // The shell also stops on its own after 30 idle minutes.
    }
  }

  const runAction = (action: ShellAction) => {
    setMenuOpen(false)
    switch (action) {
      case 'search': setSearchOpen(true); requestAnimationFrame(() => searchInputRef.current?.focus()); return
      case 'copy': void copySelection(); return
      case 'paste': void pasteClipboard(); return
      case 'clear': termRef.current?.clear(); termRef.current?.focus(); return
      case 'larger': changeFontSize(1); return
      case 'smaller': changeFontSize(-1); return
      case 'fullscreen': setExpanded(value => !value); return
      default: onTabAction(action)
    }
  }
  actionRef.current = runAction

  useEffect(() => {
    if (!menuOpen) return
    const closeOnOutsideClick = (event: MouseEvent) => { if (!menuRef.current?.contains(event.target as Node)) setMenuOpen(false) }
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setMenuOpen(false); termRef.current?.focus() } }
    document.addEventListener('mousedown', closeOnOutsideClick)
    document.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('mousedown', closeOnOutsideClick)
      document.removeEventListener('keydown', closeOnEscape)
    }
  }, [menuOpen])

  const menuItem = (label: string, action: ShellAction, icon: ReactNode, disabled = false) => (
    <button type="button" role="menuitem" disabled={disabled} onClick={() => runAction(action)}
      className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs text-foreground hover:bg-muted disabled:opacity-40">
      <span className="text-muted-foreground">{icon}</span>
      <span className="flex-1">{label}</span>
      <span className="font-mono text-[10px] text-muted-foreground">{shellShortcutLabel(action, IS_MAC)}</span>
    </button>
  )

  const statusLabel = state === 'connecting' ? 'Connecting…'
    : state === 'reconnecting' ? 'Reconnecting…'
      : state === 'closed' ? 'Disconnected'
        : state === 'stopped' ? 'Stopped'
          : 'Connected'
  const dot = state === 'connected' ? 'bg-emerald-500' : state === 'connecting' || state === 'reconnecting' ? 'bg-amber-500' : 'bg-muted-foreground/60'
  const themeBackground = shellTheme(colourScheme, RAW_XTERM_THEMES[theme]).background

  return (
    <div className={`${active ? 'flex' : 'hidden'} min-h-0 flex-col bg-background ${expanded ? 'fixed inset-0 z-50' : 'h-full'}`} data-testid="code-shell-panel" data-tab={tab}>
      {/* One header: the terminals' tabs, this one's state, then its actions. */}
      <div className="flex items-center gap-1.5 border-b border-border px-2 py-1 text-xs text-muted-foreground">
        {tabStrip}
        <span className="ml-1 flex shrink-0 items-center gap-1.5" aria-live="polite" title={`${statusLabel} · runs in this Code’s folder`}>
          <span className={`h-1.5 w-1.5 rounded-full ${dot}`} aria-hidden />
          {state === 'connecting' || state === 'reconnecting' ? <Loader2 className="h-3 w-3 animate-spin" aria-hidden /> : null}
          {/* Connected is the dot alone; anything else is said in words. */}
          <span className={state === 'connected' ? 'sr-only' : ''}>{statusLabel}</span>
        </span>
        <span className="flex-1" />
        <button type="button" className={toolbarButton} title={`Search (${shellShortcutLabel('search', IS_MAC)})`} aria-label="Search the terminal" onClick={() => runAction('search')}><Search className="h-3.5 w-3.5" /></button>
        <div ref={menuRef} className="relative">
          <button type="button" className={toolbarButton} title="Terminal menu" aria-label="Terminal menu" aria-haspopup="menu" aria-expanded={menuOpen} onClick={() => setMenuOpen(open => !open)} data-testid="code-shell-menu">
            <MoreHorizontal className="h-3.5 w-3.5" />
          </button>
          {menuOpen && (
            <div role="menu" aria-label="Terminal" className="absolute right-0 top-[calc(100%+4px)] z-50 w-64 rounded-lg border border-border bg-popover p-1 shadow-xl">
              {menuItem('Copy selection', 'copy', <Copy className="h-3.5 w-3.5" />)}
              {menuItem('Paste', 'paste', <ClipboardPaste className="h-3.5 w-3.5" />)}
              {menuItem('Clear screen', 'clear', <Eraser className="h-3.5 w-3.5" />)}
              {menuItem('Search', 'search', <Search className="h-3.5 w-3.5" />)}
              <div className="my-1 h-px bg-border" />
              {menuItem('Larger text', 'larger', <Plus className="h-3.5 w-3.5" />, fontSize >= 22)}
              {menuItem('Smaller text', 'smaller', <Minus className="h-3.5 w-3.5" />, fontSize <= 10)}
              <button type="button" role="menuitem" onClick={() => { switchColourScheme(); setMenuOpen(false) }}
                className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-xs text-foreground hover:bg-muted">
                <span className="text-muted-foreground"><Palette className="h-3.5 w-3.5" /></span>
                <span className="flex-1">Colours: {SHELL_THEME_LABELS[colourScheme]}</span>
                <span className="text-[10px] text-muted-foreground">→ {SHELL_THEME_LABELS[nextShellTheme(colourScheme)]}</span>
              </button>
              {menuItem(expanded ? 'Exit full screen' : 'Full screen', 'fullscreen', expanded ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />)}
              <div className="my-1 h-px bg-border" />
              {menuItem('New terminal', 'newTab', <TerminalIcon className="h-3.5 w-3.5" />)}
              <p className="px-2 pb-1 pt-0.5 text-[10px] text-muted-foreground">Switch terminals: {shellShortcutLabel('tab1', IS_MAC)}…{shellShortcutLabel('tab3', IS_MAC).slice(-1)} · text size {fontSize}</p>
            </div>
          )}
        </div>
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
