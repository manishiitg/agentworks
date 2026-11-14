import { useCallback, useEffect, useRef, useState, useId } from 'react'
import { ChevronDown, SquareArrowOutUpRight, X } from 'lucide-react'
import api from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import ModalPortal from '../ui/ModalPortal'

type Editor = { id: string; label: string }
type Capabilities = { available: boolean; preferred_editor: string; editors: Editor[] }
type Target = { workspacePath?: string | null; repo?: string; file?: string }
type Resolved = Capabilities & { launch?: { url?: string; jetbrains?: { path: string; line: number } }; request_id?: string }

function failureText(cause: unknown): string {
  const data = (cause as { response?: { data?: { error?: unknown } } })?.response?.data
  if (typeof data?.error === 'string') return data.error
  if (data?.error && typeof data.error === 'object' && 'message' in data.error && typeof data.error.message === 'string') return data.error.message
  return 'The editor could not open. Try again or choose another editor.'
}

/** One capability read per visible control/file, never one request per diff line.
 * The browser does not construct local paths or editor URLs. */
export function useOpenInEditor({ workspacePath, repo = '', file = '' }: Target) {
  const actor = useAuthStore(state => state.user?.id)
  const network = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const [capability, setCapability] = useState<Capabilities | null>(null)
  const [picker, setPicker] = useState(false)
  const [busy, setBusy] = useState(false)
  const [pendingLine, setPendingLine] = useState(0)
  const titleId = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const pending = useRef<AbortController | null>(null)
  const toast = useChatStore(state => state.addToast)
  const scope = `${network}:${actor || ''}:${workspacePath || ''}:${repo}:${file}`
  const currentScope = useRef(scope)
  currentScope.current = scope
  const native = typeof window.electronAPI?.openEditor === 'function'
  useEffect(() => {
    setCapability(null); setPicker(false); setBusy(false)
    if (!native || !workspacePath || network !== 'local') return
    const controller = new AbortController()
    const identity = captureChatIdentity()
    const load = () => {
      void api.get<Capabilities>('/api/workspace-editor', { params: { workspace_path: workspacePath, repo, file }, signal: controller.signal, skipSessionContext: true })
        .then(({ data }) => { if (!controller.signal.aborted && isChatIdentityCurrent(identity)) setCapability(data.available ? data : null) })
        .catch(() => { if (!controller.signal.aborted) setCapability(null) })
    }
    load()
    window.addEventListener('agentworks:editor-preference', load)
    return () => { controller.abort(); pending.current?.abort(); window.removeEventListener('agentworks:editor-preference', load) }
  }, [scope, native, workspacePath, repo, file, network])
  useEffect(() => { if (picker && dialog.current && !dialog.current.open) dialog.current.showModal() }, [picker])
  const resolve = useCallback(async (editor?: string, line = 0) => {
    if (!workspacePath || !native) return
    const capturedScope = scope, identity = captureChatIdentity()
    pending.current?.abort()
    const controller = new AbortController(); pending.current = controller
    setBusy(true)
    try {
      const { data } = await api.post<Resolved>('/api/workspace-editor', { workspace_path: workspacePath, op: 'resolve', repo, file, line, ...(editor ? { editor, remember: true } : {}) }, { signal: controller.signal, skipSessionContext: true })
      if (controller.signal.aborted || currentScope.current !== capturedScope || !isChatIdentityCurrent(identity)) return
      if (!data.launch || !data.request_id) { setCapability(data); setPendingLine(line); setPicker(true); return }
      const result = await window.electronAPI!.openEditor!({ ...data.launch, requestId: data.request_id })
      if (currentScope.current !== capturedScope || !isChatIdentityCurrent(identity)) return
      if (!result.ok) throw new Error(result.error)
      setPicker(false)
      if (editor) window.dispatchEvent(new Event('agentworks:editor-preference'))
    } catch (cause) {
      if (!controller.signal.aborted && currentScope.current === capturedScope && isChatIdentityCurrent(identity)) toast(cause instanceof Error && !('response' in cause) ? cause.message : failureText(cause), 'error')
    } finally { if (currentScope.current === capturedScope && pending.current === controller) setBusy(false) }
  }, [workspacePath, repo, file, native, scope, toast])
  const open = useCallback((line = 0) => {
    setPendingLine(line)
    if (!capability?.preferred_editor) setPicker(true)
    else void resolve(undefined, line)
  }, [capability?.preferred_editor, resolve])
  const label = capability?.editors.find(editor => editor.id === capability.preferred_editor)?.label
  const chooser = picker && capability && <ModalPortal><dialog ref={dialog} onClick={event => event.stopPropagation()} onMouseDown={event => event.stopPropagation()} onTouchStart={event => event.stopPropagation()} onKeyDown={event => { if (event.key === 'Escape') event.stopPropagation() }} onCancel={() => setPicker(false)} aria-labelledby={titleId} className="fixed inset-0 m-auto w-[calc(100%-2rem)] max-w-sm rounded-lg border border-border bg-popover p-4 text-popover-foreground shadow-xl backdrop:bg-black/50">
    <div className="flex items-center justify-between gap-3"><h2 id={titleId} className="text-sm font-semibold">Open in editor</h2><button type="button" aria-label="Close editor choice" className="rounded p-1 text-muted-foreground hover:bg-muted" onClick={() => setPicker(false)}><X className="h-4 w-4" /></button></div>
    <p className="mb-3 mt-1 text-xs text-muted-foreground">Choose once. Use the arrow beside the action to change it.</p>
    <div className="space-y-1">{capability.editors.map(editor => <button key={editor.id} type="button" disabled={busy} className="block w-full rounded-md px-3 py-2 text-left text-sm hover:bg-muted disabled:opacity-50" onClick={() => { void resolve(editor.id, pendingLine) }}>{editor.label}</button>)}</div>
  </dialog></ModalPortal>
  return { available: Boolean(capability?.available), busy, label: label ? `Open in ${label}` : 'Open in editor…', open, choose: () => { setPendingLine(0); setPicker(true) }, chooser }
}

export function OpenInEditorAction({ workspacePath, repo, file, menu = false }: Target & { menu?: boolean }) {
  const editor = useOpenInEditor({ workspacePath, repo, file })
  if (!editor.available) return null
  const classes = menu ? 'flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted' : 'flex items-center rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground'
  return <>{menu ? <div className="flex items-center"><button type="button" role="menuitem" disabled={editor.busy} className={classes} onClick={() => editor.open()}><SquareArrowOutUpRight className="h-3.5 w-3.5" />{editor.label}</button><button type="button" title="Choose editor" aria-label="Choose editor" onClick={editor.choose} className="rounded p-1 text-muted-foreground hover:bg-muted"><ChevronDown className="h-3 w-3" /></button></div> : <div className="flex items-center"><button type="button" disabled={editor.busy} className={classes} title={editor.label} aria-label={editor.label} onClick={() => editor.open()}><SquareArrowOutUpRight className="h-4 w-4" /></button><button type="button" title="Choose editor" aria-label="Choose editor" className="rounded p-0.5 text-muted-foreground hover:bg-muted" onClick={editor.choose}><ChevronDown className="h-3 w-3" /></button></div>}{editor.chooser}</>
}
