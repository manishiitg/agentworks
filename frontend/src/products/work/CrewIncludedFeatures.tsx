import { useEffect, useId, useState } from 'react'
import { ChevronDown, Plug } from 'lucide-react'
import api, { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import type { AgentProfileFeature } from '../../utils/agentProfileCapabilities'
import type { ProductMode } from '../../platform/chat/productProjects'
import type { CrewStarterType } from './crewStarterTypes'
import { crewFeaturePresentation } from './crewFeaturePresentation'

export function CrewIncludedFeatures({ type, mode, profileVersion }: { type?: CrewStarterType; mode: ProductMode; profileVersion: number }) {
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([server, getApiBaseUrl(), user, profileVersion])
  const [result, setResult] = useState<{ scope: string; features?: AgentProfileFeature[]; error?: boolean } | null>(null)
  const [retry, setRetry] = useState(0)
  const [expanded, setExpanded] = useState(false)
  const [picked, setPicked] = useState<string | null>(null)
  const hintID = useId()
  useEffect(() => {
    const controller = new AbortController()
    void api.get<{ resolved_features?: AgentProfileFeature[] }>('/api/agent-profiles/work', { params: { version: profileVersion }, signal: controller.signal, timeout: 10000 })
      .then(({ data }) => {
        if (!Array.isArray(data.resolved_features) || data.resolved_features.some(item => !item || typeof item.id !== 'string' || !item.id.trim())) throw new Error('Default features unavailable')
        if (!controller.signal.aborted) setResult({ scope, features: data.resolved_features })
      }).catch(() => { if (!controller.signal.aborted) setResult({ scope, error: true }) })
    return () => controller.abort()
  }, [scope, retry, profileVersion])
  useEffect(() => { setExpanded(false); setPicked(null) }, [scope, type])
  const current = result?.scope === scope ? result : null
  const features = crewFeaturePresentation(current?.features || [], type, mode)
  const selected = features.find(item => item.id === picked)
  return <details open={expanded} className="rounded-lg border border-border">
    <summary onClick={event => { event.preventDefault(); setExpanded(value => !value); setPicked(null) }} className="flex min-h-10 cursor-pointer list-none items-center gap-2 px-3 text-xs text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
      <span className="flex-1 font-medium">What’s included</span>{!!features.length && <span>{features.length} features</span>}<ChevronDown aria-hidden="true" className={`h-3.5 w-3.5 transition-transform ${expanded ? 'rotate-180' : ''}`} />
    </summary>
    {expanded && <section aria-label="Included features" className="space-y-2 border-t border-border p-3">
      {current?.error ? <p className="text-xs text-muted-foreground">Could not load default features. <button type="button" onClick={() => setRetry(value => value + 1)} className="font-medium text-primary hover:underline">Retry</button></p>
      : !current?.features ? <p role="status" className="text-xs text-muted-foreground">Checking included features…</p>
      : !features.length ? <p className="text-xs text-muted-foreground">No default features reported.</p>
      : <>
        <div className="grid max-h-48 gap-1 overflow-y-auto sm:grid-cols-2">
          {features.map(item => <button key={item.id} type="button" aria-label={`${item.label}: how to use`} aria-expanded={picked === item.id} aria-controls={hintID}
            onClick={() => setPicked(picked === item.id ? null : item.id)}
            className={`flex min-h-8 items-center gap-2 rounded-md px-2 py-1 text-left text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${picked === item.id ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-muted hover:text-foreground'}`}>
            <item.icon aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />{item.label}
          </button>)}
        </div>
        {selected && <div id={hintID} aria-live="polite" className="rounded-md bg-muted/40 px-2 py-2 text-xs leading-5">
          <p><span className="font-medium">{['voice', 'models', 'costs'].includes(selected.id) ? 'How to use: ' : 'Try in chat: '}</span>{selected.example}</p>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 text-[10px] text-muted-foreground"><span>{selected.location}</span>{selected.setup && <span className="inline-flex items-center gap-1"><Plug aria-hidden="true" className="h-2.5 w-2.5" />Set up first</span>}</div>
        </div>}
      </>}
    </section>}
  </details>
}
