import { useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertCircle, FolderOpen, Loader2, X } from 'lucide-react'
import type { ProductMode } from '../../platform/chat/productProjects'
import { agentApi } from '../../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { RunsOnPicker, type RunsOnSelection } from './RunsOnPicker'
import { CREW_STARTER_TYPES, type CrewOnboarding, type CrewStarterType } from './crewStarterTypes'

export function CreateCrewWizard({ onClose, onCreate, submitting, error, initialMode = 'dev', initialType, studio = false, allowExistingFolder = false }: {
  onClose: () => void
  onCreate: (title: string, purpose: string, runsOn: RunsOnSelection | undefined, mode: ProductMode, onboarding: CrewOnboarding, rootPath?: string) => void | Promise<void>
  submitting: boolean
  error: string | null
  initialType?: CrewStarterType
  initialMode?: ProductMode
  studio?: boolean
  allowExistingFolder?: boolean
}) {
  const [type, setType] = useState<CrewStarterType | undefined>(initialType ?? (initialMode === 'cowork' ? 'cowork' : undefined))
  const [title, setTitle] = useState('')
  const [rootPath, setRootPath] = useState('')
  const [editingFolder, setEditingFolder] = useState(false)
  const [folderError, setFolderError] = useState<string | null>(null)
  const [location, setLocation] = useState<{ folder: string; can_choose_folder: boolean } | null>(null)
  const [locationLoading, setLocationLoading] = useState(true)
  const [locationError, setLocationError] = useState(false)
  const [locationRevision, setLocationRevision] = useState(0)
  const [mode, setMode] = useState(initialMode)
  const [runsOn, setRunsOn] = useState<RunsOnSelection | undefined>()
  const formRef = useRef<HTMLFormElement>(null)
  const nameRef = useRef<HTMLInputElement>(null)
  const selected = CREW_STARTER_TYPES.find(item => item.id === type)
  const starters = studio ? CREW_STARTER_TYPES.filter(item => item.id === 'code' || item.id === 'cowork') : CREW_STARTER_TYPES
  const canChangeFolder = allowExistingFolder && location?.can_choose_folder === true
  const closingRef = useRef({ onClose, submitting })
  closingRef.current = { onClose, submitting }
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const key = (event: KeyboardEvent) => {
      // A provider setup dialog can open above this wizard from Runs on.
      if (!formRef.current?.contains(event.target as Node)) return
      if (event.key === 'Escape') {
        event.preventDefault(); event.stopPropagation()
        if (!closingRef.current.submitting) closingRef.current.onClose()
      }
      if (event.key !== 'Tab') return
      const elements = Array.from(formRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), summary') || []).filter(item => item.getClientRects().length > 0)
      const first = elements[0], last = elements[elements.length - 1]
      if (!first) { event.preventDefault(); formRef.current?.focus(); return }
      if (event.shiftKey && (document.activeElement === first || !elements.includes(document.activeElement as HTMLElement))) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && (document.activeElement === last || !elements.includes(document.activeElement as HTMLElement))) { event.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', key, true)
    return () => { document.removeEventListener('keydown', key, true); previous?.focus() }
  }, [])
  useEffect(() => { nameRef.current?.focus() }, [])
  useEffect(() => {
    let active = true
    const identity = captureChatIdentity()
    setLocationLoading(true)
    setLocationError(false)
    const timer = window.setTimeout(() => {
      void agentApi.getProjectCreationLocation('work', title.trim()).then(next => {
        if (active && isChatIdentityCurrent(identity)) { setLocation(next); setLocationLoading(false) }
      }).catch(() => { if (active && isChatIdentityCurrent(identity)) { setLocationError(true); setLocationLoading(false) } })
    }, 200)
    return () => { active = false; window.clearTimeout(timer) }
  }, [title, locationRevision])
  const choose = (next?: CrewStarterType) => {
    setType(next)
    if (studio && next) setMode(next === 'code' ? 'dev' : 'cowork')
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (submitting || !title.trim()) return
    void onCreate(title.trim(), selected?.purpose || '', runsOn, mode, { type }, canChangeFolder ? rootPath.trim() || undefined : undefined)
  }
  return <div className="fixed inset-0 z-50 grid place-items-center bg-black/65 p-3 backdrop-blur-sm sm:p-6">
    <form ref={formRef} tabIndex={-1} onSubmit={submit} role="dialog" aria-modal="true" aria-labelledby="crew-wizard-title" aria-busy={submitting} className="flex max-h-[90dvh] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-border bg-background shadow-2xl">
      <header className="flex shrink-0 items-center justify-between gap-4 border-b border-border px-5 py-4 sm:px-6">
        <h2 id="crew-wizard-title" className="text-lg font-semibold">New project</h2>
        <button type="button" onClick={onClose} disabled={submitting} aria-label="Close" className="rounded-lg p-2 text-muted-foreground hover:bg-muted disabled:opacity-50"><X className="h-4 w-4" /></button>
      </header>
      <div className="min-h-0 space-y-4 overflow-y-auto px-5 py-5 sm:px-6">
        <label className="block text-sm font-medium" htmlFor="crew-wizard-name">Name
          <input ref={nameRef} id="crew-wizard-name" value={title} onChange={event => setTitle(event.target.value)} maxLength={60} disabled={submitting} placeholder="e.g. My project" className="mt-1.5 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm font-normal outline-none focus:ring-2 focus:ring-ring" />
        </label>
        <section aria-label="Project folder">
          <div className="flex items-center justify-between gap-3">
            <h3 className="text-sm font-medium">Folder</h3>
            {canChangeFolder && <button type="button" disabled={submitting} onClick={() => { setEditingFolder(value => !value); setFolderError(null) }} className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted">{editingFolder ? 'Done' : 'Change'}</button>}
          </div>
          {editingFolder && canChangeFolder ? <>
            <div className="mt-1.5 flex gap-2">
              <input id="crew-wizard-folder" aria-label="Folder path" value={rootPath} onChange={event => { setRootPath(event.target.value); setFolderError(null) }} disabled={submitting} placeholder="Paste an existing folder path" className="min-w-0 flex-1 rounded-lg border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring" />
              {window.electronAPI?.pickWorkflowFolder && <button type="button" disabled={submitting} onClick={async () => {
                setFolderError(null)
                try { const folder = await window.electronAPI!.pickWorkflowFolder!(); if (folder) setRootPath(folder) }
                catch { setFolderError('Could not open the folder picker. You can paste the folder path instead.') }
              }} className="inline-flex items-center gap-2 rounded-lg border border-border px-3 py-2 text-sm hover:bg-muted disabled:opacity-50"><FolderOpen aria-hidden="true" className="h-4 w-4" />Browse</button>}
            </div>
            <button type="button" disabled={submitting} onClick={() => { setRootPath(''); setEditingFolder(false); setFolderError(null) }} className="mt-1.5 text-xs text-muted-foreground hover:text-foreground">Use default folder</button>
          </> : <p className="mt-1.5 break-all text-sm text-muted-foreground" title={rootPath || location?.folder}>{rootPath || (locationLoading ? 'Checking folder…' : locationError ? 'Folder location unavailable' : location?.folder)}</p>}
          {!rootPath && location && !locationLoading && !locationError && <p className="mt-1 text-xs text-muted-foreground">A unique suffix is added when created.</p>}
          {locationError && <button type="button" onClick={() => setLocationRevision(value => value + 1)} className="mt-1 text-xs text-primary">Retry folder lookup</button>}
          {folderError && <p role="alert" className="mt-1 text-xs text-destructive">{folderError}</p>}
        </section>
        <label className="block text-sm font-medium" htmlFor="crew-wizard-mode">Workspace mode
          <select id="crew-wizard-mode" value={mode} onChange={event => setMode(event.target.value as ProductMode)} disabled={submitting} className="mt-1.5 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm font-normal">
            <option value="dev">Code — files and terminal</option>
            <option value="cowork">Cowork — research, write and organize</option>
          </select>
        </label>
        <RunsOnPicker profileId="work" onChange={setRunsOn} disabled={submitting} saveAccount="when-needed" />
        <label className="block border-t border-border pt-4 text-sm font-medium" htmlFor="crew-wizard-type">Starting point <span className="font-normal text-muted-foreground">(optional)</span>
          <select id="crew-wizard-type" value={type || ''} onChange={event => choose(event.target.value as CrewStarterType || undefined)} disabled={submitting} className="mt-1.5 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm font-normal">
            <option value="">Blank project</option>
            {starters.map(item => <option key={item.id} value={item.id}>{item.label}</option>)}
          </select>
        </label>
        {error && <p role="alert" className="flex items-start gap-2 text-sm text-destructive"><AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />{error}</p>}
      </div>
      <footer className="flex shrink-0 items-center justify-end gap-3 border-t border-border px-5 py-3.5 sm:px-6">
        <button type="button" onClick={onClose} disabled={submitting} className="rounded-lg px-3 py-2 text-sm text-muted-foreground hover:bg-muted disabled:opacity-50">Cancel</button>
        <button type="submit" disabled={submitting || !title.trim()} className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50">
          {submitting ? <><Loader2 className="h-4 w-4 animate-spin" />Creating…</> : 'Create project'}
        </button>
      </footer>
    </form>
  </div>
}
