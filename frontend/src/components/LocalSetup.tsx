import { useEffect, useState } from 'react'
import { ArrowRight, CheckCircle2, FolderOpen, Loader2, Plus, X } from 'lucide-react'
import ModalPortal from './ui/ModalPortal'
import { Button } from './ui/Button'
import { llmConfigService } from '../services/llm-config-api'
import type { LLMDiscoveryCandidate } from '../services/api-types'
import { openWorkspaceSettings } from '../utils/workspaceSettingsNavigation'

export const LOCAL_SETUP_SEEN_KEY = 'agentworks_local_setup_v1_seen'

/** The new-user setup on the Mac app (PLAT-856-441 onboarding): the AI programs already on this Mac, then where to
 *  work. Opened automatically on the first visit and from Home at any time; it only reads and opens existing flows,
 *  so running it again never resets anything. */
export function LocalSetup({ onClose, onStartProject }: { onClose: () => void; onStartProject: () => void }) {
  const [step, setStep] = useState<1 | 2>(1)
  const [programs, setPrograms] = useState<LLMDiscoveryCandidate[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    llmConfigService.discoverLLMSetup()
      .then(result => { if (!cancelled) setPrograms(result.candidates.filter(item => item.kind === 'local_cli' && !item.deprecated)) })
      .catch(() => { if (!cancelled) setError('Could not check the AI programs on this Mac.') })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [onClose])
  const openAccounts = () => { onClose(); openWorkspaceSettings('providers') }
  const start = () => { onClose(); onStartProject() }
  const stepPill = (n: 1 | 2, label: string) => (
    <span className={`rounded-full border px-2.5 py-1 text-xs ${step === n ? 'border-primary text-foreground' : 'border-border text-muted-foreground'}`}>{n} · {label}</span>
  )
  return (
    <ModalPortal>
      <div className="fixed inset-0 z-[60] flex items-start justify-center overflow-y-auto bg-black/50 p-4 sm:p-8" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
        <section role="dialog" aria-modal="true" aria-labelledby="local-setup-title" className="w-full max-w-2xl rounded-2xl border border-border bg-background p-5 shadow-xl sm:p-6">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 id="local-setup-title" className="text-lg font-semibold">Set up AgentWorks on this Mac</h2>
              <p className="mt-1 text-xs text-muted-foreground">Two quick steps. Nothing you already have is changed.</p>
            </div>
            <button type="button" aria-label="Close setup" onClick={onClose} className="rounded-md p-1.5 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
          </div>
          <div className="mt-4 flex flex-wrap gap-2">{stepPill(1, 'AI programs')}{stepPill(2, 'Where to work')}</div>

          {step === 1 && <div className="mt-5">
            <h3 className="text-sm font-semibold">AI programs on this Mac</h3>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">We looked for coding programs you already use. Signed-in ones work right away.</p>
            {error && <p role="alert" className="mt-3 text-xs text-red-600 dark:text-red-400">{error}</p>}
            {!programs && !error && <p className="mt-3 flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Checking…</p>}
            {programs && <ul className="mt-3 space-y-2">
              {programs.map(program => {
                const installed = program.runtime_available !== false
                const ready = installed && program.usable
                return <li key={program.provider} className="flex items-center gap-3 rounded-xl border border-border bg-card/50 px-3.5 py-2.5">
                  <span className={`h-2 w-2 shrink-0 rounded-full ${ready ? 'bg-emerald-500' : installed ? 'bg-amber-500' : 'bg-gray-400'}`} aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">{program.label}</span>
                    <span className="block text-xs text-muted-foreground">{ready ? 'Ready' : installed ? 'Installed, not signed in' : 'Not installed'}{!installed && program.setup_hint ? ` · ${program.setup_hint}` : ''}</span>
                  </span>
                  {ready ? <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-500" aria-label="Ready" /> : installed && <Button type="button" size="xs" variant="outline" onClick={openAccounts}>Sign in</Button>}
                </li>
              })}
            </ul>}
            <p className="mt-3 text-xs leading-5 text-muted-foreground">To use them on your company's server as well, open <button type="button" className="font-medium text-primary hover:underline" onClick={openAccounts}>AI accounts</button>: each account shows "Use it on your servers too".</p>
            <div className="mt-5 flex justify-between gap-2">
              <Button type="button" variant="ghost" size="sm" onClick={() => setStep(2)}>Skip</Button>
              <Button type="button" size="sm" onClick={() => setStep(2)}>Next<ArrowRight className="ml-1.5 h-4 w-4" /></Button>
            </div>
          </div>}

          {step === 2 && <div className="mt-5">
            <h3 className="text-sm font-semibold">Where do you want to work?</h3>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">A project holds your files, agents and chats. It starts private to you.</p>
            <div className="mt-3 grid gap-3 sm:grid-cols-2">
              <button type="button" onClick={start} className="rounded-xl border border-border bg-card/50 p-4 text-left transition-colors hover:border-primary/60 hover:bg-muted/40">
                <Plus className="h-5 w-5 text-primary" aria-hidden="true" />
                <span className="mt-2 block text-sm font-semibold">Start a new project</span>
                <span className="mt-1 block text-xs leading-5 text-muted-foreground">An empty folder AgentWorks makes for you.</span>
              </button>
              <button type="button" onClick={start} className="rounded-xl border border-border bg-card/50 p-4 text-left transition-colors hover:border-primary/60 hover:bg-muted/40">
                <FolderOpen className="h-5 w-5 text-primary" aria-hidden="true" />
                <span className="mt-2 block text-sm font-semibold">Open a project folder</span>
                <span className="mt-1 block text-xs leading-5 text-muted-foreground">Work in a folder you already have, such as one you use with Claude Code or Codex. Its files and Git history stay where they are; choose it under Folder on the next screen.</span>
              </button>
            </div>
            <div className="mt-5 flex justify-between gap-2">
              <Button type="button" variant="ghost" size="sm" onClick={() => setStep(1)}>Back</Button>
              <Button type="button" variant="ghost" size="sm" onClick={onClose}>Done for now</Button>
            </div>
          </div>}
        </section>
      </div>
    </ModalPortal>
  )
}
