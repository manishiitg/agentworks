import { useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertCircle, ArrowLeft, ArrowRight, Check, Loader2, LockKeyhole, X } from 'lucide-react'
import type { ProductMode } from '../../platform/chat/productProjects'
import { RunsOnPicker, type RunsOnSelection } from './RunsOnPicker'
import { CREW_STARTER_TYPES, type CrewOnboarding, type CrewStarterType } from './crewStarterTypes'
import { CrewIncludedFeatures } from './CrewIncludedFeatures'

export function CreateCrewWizard({ onClose, onCreate, submitting, error, initialMode = 'dev', initialPurpose = '', initialType, profileVersion = 3 }: {
  onClose: () => void
  onCreate: (title: string, purpose: string, runsOn: RunsOnSelection | undefined, mode: ProductMode, onboarding: CrewOnboarding) => void | Promise<void>
  submitting: boolean
  error: string | null
  initialMode?: ProductMode
  initialPurpose?: string
  initialType?: CrewStarterType
  profileVersion?: number
}) {
  const [step, setStep] = useState(initialPurpose || initialType ? 2 : 1)
  const [type, setType] = useState<CrewStarterType | undefined>(initialType ?? (initialMode === 'cowork' ? 'cowork' : undefined))
  const [title, setTitle] = useState('')
  const [purpose, setPurpose] = useState(initialPurpose || CREW_STARTER_TYPES.find(item => item.id === initialType)?.purpose || (initialMode === 'cowork' ? CREW_STARTER_TYPES.find(item => item.id === 'cowork')!.purpose : ''))
  const [mode, setMode] = useState(initialMode)
  const [runsOn, setRunsOn] = useState<RunsOnSelection | undefined>()
  const [openSharing, setOpenSharing] = useState(false)
  const formRef = useRef<HTMLFormElement>(null)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const nameRef = useRef<HTMLInputElement>(null)
  const selected = CREW_STARTER_TYPES.find(item => item.id === type)
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
  useEffect(() => { if (step === 2) nameRef.current?.focus(); else headingRef.current?.focus() }, [step])
  const choose = (next?: CrewStarterType) => {
    const previous = CREW_STARTER_TYPES.find(item => item.id === type)
    if (!purpose || purpose === previous?.purpose) setPurpose(CREW_STARTER_TYPES.find(item => item.id === next)?.purpose || '')
    setType(next)
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (submitting) return
    if (step === 1) { if (type) setStep(2); return }
    if (title.trim()) void onCreate(title.trim(), purpose.trim(), runsOn, mode, { type, openSharing })
  }
  return <div className="fixed inset-0 z-50 grid place-items-center bg-black/65 p-3 backdrop-blur-sm sm:p-6">
    <form ref={formRef} tabIndex={-1} onSubmit={submit} role="dialog" aria-modal="true" aria-labelledby="crew-wizard-title" aria-describedby="crew-wizard-description" aria-busy={submitting} className="flex max-h-[90vh] w-full max-w-xl flex-col overflow-hidden rounded-2xl border border-border bg-background shadow-2xl">
      <header className="flex shrink-0 items-start justify-between gap-4 border-b border-border px-5 py-4 sm:px-6">
        <div>
          <p className="mb-1 text-xs text-muted-foreground">Step {step} of 2</p>
          <h2 ref={headingRef} tabIndex={-1} id="crew-wizard-title" className="text-lg font-semibold outline-none">{step === 1 ? 'Create a Crew' : selected?.label || 'Your Crew'}</h2>
          <p id="crew-wizard-description" className="mt-1 text-sm text-muted-foreground">{step === 1 ? 'Pick a starting point.' : 'Give it a name to get started.'}</p>
        </div>
        <button type="button" onClick={onClose} disabled={submitting} aria-label="Close" className="rounded-lg p-2 text-muted-foreground hover:bg-muted disabled:opacity-50"><X className="h-4 w-4" /></button>
      </header>
      <div className="min-h-0 overflow-y-auto px-5 py-5 sm:px-6">
        {step === 1 ? <fieldset disabled={submitting}>
          <legend className="sr-only">Crew type</legend>
          <div className="grid gap-2.5 sm:grid-cols-2">
            {CREW_STARTER_TYPES.map(item => <label key={item.id} className={`relative flex cursor-pointer gap-3 rounded-xl border p-3.5 focus-within:ring-2 focus-within:ring-ring ${item.id === 'chief-of-staff' ? 'sm:col-span-2' : ''} ${type === item.id ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'}`}>
              <input type="radio" name="crew-type" value={item.id} checked={type === item.id} onChange={() => choose(item.id)} className="sr-only" />
              <span className={`grid h-10 w-10 shrink-0 place-items-center rounded-xl ${type === item.id ? 'bg-primary/15 text-primary' : 'bg-muted text-muted-foreground'}`}><item.icon aria-hidden="true" className="h-5 w-5" /></span>
              <span className="min-w-0"><span className="block pr-3 text-sm font-semibold">{item.label}</span><span className="mt-1 block text-xs leading-5 text-muted-foreground">{item.summary}</span></span>
              {type === item.id && <Check aria-hidden="true" className="absolute right-2 top-3 h-3.5 w-3.5 text-primary" />}
            </label>)}
          </div>
        </fieldset> : <div className="space-y-3">
          <label className="block text-sm font-medium" htmlFor="crew-wizard-name">Crew name
            <input ref={nameRef} id="crew-wizard-name" value={title} onChange={event => setTitle(event.target.value)} maxLength={60} disabled={submitting} placeholder="e.g. Operations helper" className="mt-1.5 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm font-normal outline-none focus:ring-2 focus:ring-ring" />
          </label>
          <CrewIncludedFeatures type={type} mode={mode} profileVersion={profileVersion} />
          <details open={Boolean(initialPurpose) || undefined}>
            <summary className="cursor-pointer text-xs font-medium text-muted-foreground">Add instructions <span className="font-normal">(optional)</span></summary>
            <label className="sr-only" htmlFor="crew-wizard-purpose">What should it help with?</label>
            <textarea id="crew-wizard-purpose" value={purpose} onChange={event => setPurpose(event.target.value)} rows={2} maxLength={1000} disabled={submitting} className="mt-2 w-full resize-y rounded-lg border border-border bg-background px-3 py-2 text-sm font-normal outline-none focus:ring-2 focus:ring-ring" />
          </details>
          <div className="flex items-center justify-between gap-3 border-t border-border pt-4">
            <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={openSharing} onChange={event => setOpenSharing(event.target.checked)} disabled={submitting} />Set up sharing next</label>
            <span className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground" title="Choose people and permissions after creation."><LockKeyhole aria-hidden="true" className="h-3 w-3" />Only me</span>
          </div>
          <details className="border-t border-border pt-3">
            <summary className="cursor-pointer text-xs font-medium text-muted-foreground">Advanced setup</summary>
            <label className="mt-3 block text-xs font-medium" htmlFor="crew-wizard-mode">Workspace mode</label>
            <select id="crew-wizard-mode" value={mode} onChange={event => setMode(event.target.value as ProductMode)} disabled={submitting} className="mt-1.5 w-full rounded-lg border border-border bg-background px-3 py-2 text-sm">
              <option value="cowork">Cowork — work, dashboards and automations</option>
              <option value="dev">Dev — code, files and terminal</option>
            </select>
            <RunsOnPicker profileId="work" onChange={setRunsOn} disabled={submitting} saveAccount="when-needed" />
          </details>
        </div>}
        {error && <p role="alert" className="mt-3 flex items-start gap-2 text-sm text-destructive"><AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />{error}</p>}
      </div>
      <footer className="flex shrink-0 items-center justify-between gap-3 border-t border-border px-5 py-3.5 sm:px-6">
        {step === 1 ? <button type="button" onClick={() => { choose(); setStep(2) }} disabled={submitting} className="rounded-lg px-2 py-2 text-sm text-muted-foreground hover:bg-muted">Start blank</button> : <button type="button" onClick={() => setStep(1)} disabled={submitting} className="flex items-center gap-1.5 rounded-lg px-2 py-2 text-sm text-muted-foreground hover:bg-muted disabled:opacity-50"><ArrowLeft className="h-4 w-4" />Back</button>}
        <button type="submit" disabled={submitting || (step === 1 ? !type : !title.trim())} className="inline-flex items-center gap-2 rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50">
          {submitting ? <><Loader2 className="h-4 w-4 animate-spin" />Creating…</> : step === 1 ? <>Continue<ArrowRight className="h-4 w-4" /></> : 'Create Crew'}
        </button>
      </footer>
    </form>
  </div>
}
