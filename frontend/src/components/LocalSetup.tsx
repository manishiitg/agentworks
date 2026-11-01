import { useCallback, useEffect, useState } from 'react'
import { ArrowRight, Bot, Loader2, RefreshCw } from 'lucide-react'
import ModalPortal from './ui/ModalPortal'
import { Button } from './ui/Button'
import { llmConfigService } from '../services/llm-config-api'
import type { LLMDiscoveryCandidate } from '../services/api-types'
import { ConnectServerForm } from './providers/ProvidersOverview'

export const LOCAL_SETUP_SEEN_KEY = 'agentworks_local_setup_v1_seen'

const providerIcons: Record<string, string> = {
  'claude-code': '/provider-icons/claude.ico',
  'codex-cli': '/provider-icons/codex.png',
  'cursor-cli': '/provider-icons/cursor.svg',
  'pi-cli': '/provider-icons/pi.svg',
  'muse-cli': '/provider-icons/muse.ico',
  'agy-cli': '/provider-icons/antigravity.svg',
}

/** Local AI discovery followed by an optional server connection. */
export function LocalSetup({ onClose }: { onClose: () => void }) {
  const [step, setStep] = useState<1 | 2>(1)
  const [programs, setPrograms] = useState<LLMDiscoveryCandidate[] | null>(null)
  const [error, setError] = useState('')
  const [check, setCheck] = useState(0)
  const [serverConnected, setServerConnected] = useState(false)
  const setServerConnectedTrue = useCallback(() => setServerConnected(true), [])
  useEffect(() => {
    let cancelled = false
    setError('')
    setPrograms(null)
    llmConfigService.discoverLLMSetup()
      .then(result => { if (!cancelled) setPrograms(result.candidates.filter(item => item.kind === 'local_cli' && !item.deprecated)) })
      .catch(() => { if (!cancelled) setError('Could not check the AI programs on this Mac.') })
    return () => { cancelled = true }
  }, [check])
  const stepPill = (n: 1 | 2, label: string) => (
    <span className={`rounded-full border px-2.5 py-1 text-xs ${step === n ? 'border-primary text-foreground' : 'border-border text-muted-foreground'}`}>{n} · {label}</span>
  )
  return (
    <ModalPortal>
      <div className="fixed inset-0 z-[60] flex items-start justify-center overflow-y-auto bg-background p-4 sm:p-8">
        <section aria-labelledby="local-setup-title" className="w-full max-w-4xl p-5 sm:p-6">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 id="local-setup-title" className="text-lg font-semibold">Set up AgentWorks on this Mac</h2>
              <p className="mt-1 text-xs text-muted-foreground">Two quick steps. Nothing you already have is changed.</p>
            </div>
          </div>
          <div className="mt-4 flex flex-wrap gap-2">{stepPill(1, 'AI programs')}{stepPill(2, 'Server')}</div>

          {step === 1 && <div className="mt-5">
            <h3 className="text-sm font-semibold">AI programs on this Mac</h3>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">We found the coding programs on this Mac. To use another, install it and sign in on your Mac, then check again.</p>
            {error && <p role="alert" className="mt-3 text-xs text-red-600 dark:text-red-400">{error}</p>}
            {!programs && !error && <p className="mt-3 flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Checking…</p>}
            {programs && <ul className="mt-3 space-y-2">
              {programs.map(program => {
                const installed = program.runtime_available !== false
                const ready = installed && program.usable
                return <li key={program.provider} className="flex items-center gap-3 rounded-xl border border-border bg-card/50 px-3.5 py-2.5">
                  <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-muted/50">
                    {providerIcons[program.provider] ? <img src={providerIcons[program.provider]} alt="" className="h-6 w-6 object-contain" /> : <Bot aria-hidden="true" className="h-5 w-5 text-muted-foreground" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">{program.label}</span>
                    <span className="block text-xs text-muted-foreground">{ready ? 'Ready' : installed ? 'Installed, not signed in' : 'Not installed'}</span>
                  </span>
                </li>
              })}
            </ul>}
            <div className="mt-5 flex justify-between gap-2">
              <Button type="button" variant="ghost" size="sm" onClick={() => setCheck(value => value + 1)}><RefreshCw className="mr-1.5 h-4 w-4" />Check again</Button>
              <Button type="button" size="sm" onClick={() => setStep(2)}>Next<ArrowRight className="ml-1.5 h-4 w-4" /></Button>
            </div>
          </div>}

          {step === 2 && <div className="mt-5">
            <h3 className="text-sm font-semibold">Connect a server <span className="font-normal text-muted-foreground">(optional)</span></h3>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">Connect your team's AgentWorks server, or continue on this Mac. You can connect later.</p>
            <div className="mt-4"><ConnectServerForm onConnected={setServerConnectedTrue} /></div>
            {serverConnected && <p role="status" className="mt-3 text-sm text-emerald-600 dark:text-emerald-400">Server connected.</p>}
            <div className="mt-5 flex justify-between gap-2">
              <Button type="button" variant="ghost" size="sm" onClick={() => setStep(1)}>Back</Button>
              <Button type="button" size="sm" onClick={onClose}>Continue to Home<ArrowRight className="ml-1.5 h-4 w-4" /></Button>
            </div>
          </div>}
        </section>
      </div>
    </ModalPortal>
  )
}
