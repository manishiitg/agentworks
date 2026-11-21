import { useCallback, useEffect, useRef, useState, type CSSProperties, type MouseEvent } from 'react'
import { Crosshair, Loader2, X } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { captureChatContextDestination, isChatContextDestinationCurrent, requestBrowserContext, stageCapturedChatContext, type BrowserContextMetadata, type CapturedChatContext, type ChatContextDestination } from '../../utils/chatContextAttachment'
import { WorkspaceImage } from '../ui/WorkspaceImage'

type PickState = { scope: ChatContextDestination; sourceKey: string; phase: 'loading' | 'armed' | 'capturing' | 'preview'; documentToken?: string; capture?: CapturedChatContext }

type PickerOptions = {
  tabId?: string
  browserSession: string
  sourceKey: string
  enabled: boolean
  onBegin: () => void
  point: (x: number, y: number) => { x: number; y: number }
  outline: (rect: BrowserContextMetadata['rect']) => CSSProperties | undefined
}

export function useBrowserContextPicker(options: PickerOptions) {
  const current = useRef(options)
  current.current = options
  const [pick, setPick] = useState<PickState>()
  const generation = useRef(0)
  const request = useRef<AbortController | undefined>(undefined)
  const cancel = useCallback(() => { generation.current++; request.current?.abort(); request.current = undefined; setPick(undefined) }, [])
  useEffect(() => { cancel(); return cancel }, [options.sourceKey, options.enabled, cancel])
  useEffect(() => {
    if (!pick) return
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); cancel() } }
    window.addEventListener('keydown', escape, true)
    return () => window.removeEventListener('keydown', escape, true)
  }, [Boolean(pick), cancel])
  const error = (cause: unknown) => {
    const body = (cause as { response?: { data?: { message?: unknown; error?: unknown } } })?.response?.data
    const message = typeof body?.message === 'string' ? body.message : typeof body?.error === 'string' ? body.error : undefined
    useChatStore.getState().addToast(message || (cause instanceof Error ? cause.message : 'Unable to capture this element. Pick it again.'), 'error')
  }
  const valid = (state: PickState, id: number) => generation.current === id && current.current.enabled && current.current.sourceKey === state.sourceKey && isChatContextDestinationCurrent(state.scope)
  async function begin() {
    const scope = options.tabId ? captureChatContextDestination(options.tabId) : undefined
    if (!scope) { error(new Error('Open the intended Crew chat before picking an element.')); return }
    cancel()
    const id = generation.current
    const state: PickState = { scope, sourceKey: options.sourceKey, phase: 'loading' }
    const controller = new AbortController(); request.current = controller
    options.onBegin()
    setPick(state)
    try {
      const probe = await requestBrowserContext<BrowserContextMetadata>(scope, options.browserSession, undefined, controller.signal)
      if (valid(state, id)) setPick({ ...state, phase: 'armed', documentToken: probe.document_token })
      else cancel()
    } catch (cause) { if (generation.current === id) { cancel(); error(cause) } }
  }
  async function select(event: MouseEvent<HTMLDivElement>) {
    event.preventDefault(); event.stopPropagation()
    if (!pick || pick.phase !== 'armed' || event.button !== 0 || !pick.documentToken) return
    const state = pick, id = generation.current
    const position = options.point(event.clientX, event.clientY)
    setPick({ ...state, phase: 'capturing' })
    const controller = new AbortController(); request.current = controller
    try {
      const captured = await requestBrowserContext<CapturedChatContext>(state.scope, options.browserSession, { document_token: state.documentToken!, ...position }, controller.signal)
      if (valid(state, id)) setPick({ ...state, phase: 'preview', capture: captured })
      else cancel()
    } catch (cause) { if (generation.current === id) { cancel(); error(cause) } }
  }
  function attach() {
    if (!pick?.capture || !valid(pick, generation.current) || !stageCapturedChatContext(pick.scope, pick.capture)) {
      cancel(); error(new Error('The chat or browser changed. Add the selection again.')); return
    }
    cancel()
    useChatStore.getState().addToast('Added to your next message. Nothing has been sent.', 'success')
  }
  const button = options.tabId && options.enabled ? <button type="button" aria-pressed={Boolean(pick)} title={pick ? 'Cancel element pick (Esc)' : 'Pick a page element to add to chat'} onClick={() => pick ? cancel() : void begin()} className="inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border border-border bg-background px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground">
    <Crosshair className="h-3.5 w-3.5" aria-hidden />{pick ? 'Cancel pick' : 'Pick element'}
  </button> : null
  const stop = (event: MouseEvent) => { event.preventDefault(); event.stopPropagation() }
  const captured = pick?.capture
  const overlay = pick && <div data-testid="browser-context-picker" className="absolute inset-0 z-10 cursor-crosshair" onMouseDown={event => void select(event)} onMouseUp={stop} onContextMenu={stop} onWheel={event => { event.preventDefault(); event.stopPropagation() }}>
    {captured && <div className="pointer-events-none absolute rounded-sm border-2 border-primary bg-primary/10" style={options.outline(captured.capture.rect)} />}
    {pick.phase === 'preview' && captured ? <div className="absolute bottom-3 left-3 right-3 mx-auto w-[calc(100%-1.5rem)] max-w-md cursor-default rounded-xl border border-border bg-background p-3 text-foreground shadow-xl" onMouseDown={event => event.stopPropagation()} onMouseUp={event => event.stopPropagation()}>
      <div className="flex items-start gap-3"><WorkspaceImage path={captured.preview_path} alt="Picked page element" className="h-20 w-28 shrink-0 rounded-lg border border-border object-contain" /><div className="min-w-0 flex-1 space-y-1 text-xs"><div className="font-medium">Page element</div><p className="truncate font-mono text-[11px] text-muted-foreground" title={captured.capture.selector}>{captured.capture.selector}</p><p className="line-clamp-3 whitespace-pre-wrap break-words">{captured.capture.text || 'No text on this element'}</p></div><button type="button" onClick={cancel} aria-label="Cancel picked element" className="rounded p-0.5 text-muted-foreground hover:bg-muted"><X className="h-3.5 w-3.5" /></button></div>
      <p className="mt-2 truncate text-[10px] text-muted-foreground" title={captured.capture.url}>{captured.capture.url}</p>
      <div className="mt-3 flex justify-end gap-2"><button type="button" onClick={cancel} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted">Cancel</button><button type="button" onClick={attach} className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground">Add to chat</button></div>
    </div> : <div role="status" className="pointer-events-none absolute bottom-3 left-1/2 flex -translate-x-1/2 items-center gap-2 rounded-lg border border-border bg-background px-3 py-2 text-xs text-foreground shadow-lg">{pick.phase !== 'armed' && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{pick.phase === 'armed' ? 'Click an element. Esc cancels; nothing on the page will be clicked.' : pick.phase === 'loading' ? 'Preparing element pick…' : 'Capturing selected element…'}</div>}
  </div>
  return { active: Boolean(pick), button, overlay }
}
