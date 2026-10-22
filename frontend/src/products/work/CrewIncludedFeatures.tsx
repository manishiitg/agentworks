import { useEffect, useId, useState } from 'react'
import { ArrowLeft, ArrowRight, Plug } from 'lucide-react'
import api, { getApiBaseUrl } from '../../services/api'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import type { AgentProfileFeature } from '../../utils/agentProfileCapabilities'
import type { ProductMode } from '../../platform/chat/productProjects'
import { CREW_STARTER_TYPES, type CrewStarterType } from './crewStarterTypes'
import { crewFeaturePresentation } from './crewFeaturePresentation'

export function CrewIncludedFeatures({ type, mode, profileVersion }: { type?: CrewStarterType; mode: ProductMode; profileVersion: number }) {
  const user = useAuthStore(state => state.user)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = JSON.stringify([server, getApiBaseUrl(), user, profileVersion])
  const [result, setResult] = useState<{ scope: string; features?: AgentProfileFeature[]; error?: boolean } | null>(null)
  const [retry, setRetry] = useState(0)
  const [expanded, setExpanded] = useState(false)
  const [picked, setPicked] = useState<{ id: string; key: string } | null>(null)
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
  const starter = CREW_STARTER_TYPES.find(item => item.id === type)
  const features = crewFeaturePresentation(current?.features || [], type, mode)
  const selected = features.find(item => item.id === picked?.id)
  const choice = (item: typeof features[number], key: string) => <button key={key} type="button" aria-label={`${item.label}: how to use`} aria-expanded={picked?.key === key} aria-controls={hintID} onClick={() => setPicked(picked?.key === key ? null : { id: item.id, key })} className={`inline-flex min-h-8 max-w-full items-center gap-1.5 rounded-full border px-3 py-1.5 text-left text-xs font-medium leading-4 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${picked?.key === key ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border bg-background text-foreground/80 hover:border-primary/30 hover:bg-muted'}`}>
    <item.icon aria-hidden="true" className={`h-3.5 w-3.5 shrink-0 ${picked?.key === key ? 'text-primary' : 'text-muted-foreground'}`} /><span className="min-w-0 break-words">{item.label}</span>
  </button>
  return <section aria-label="Included features" className="space-y-2.5">
    <div className="flex items-center justify-between gap-2">
      <h3 className="text-xs font-medium">{starter && !expanded ? 'How it works' : 'Included by default'}</h3>
      {!!features.length && starter && <button type="button" aria-expanded={expanded} onClick={() => { setExpanded(value => !value); setPicked(null) }} className="inline-flex items-center gap-1 rounded py-1 text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">{expanded ? <><ArrowLeft aria-hidden="true" className="h-3 w-3" />Back to guide</> : <>All {features.length} features<ArrowRight aria-hidden="true" className="h-3 w-3" /></>}</button>}
    </div>
    {current?.error ? <p className="text-xs text-muted-foreground">Could not load default features. <button type="button" onClick={() => setRetry(value => value + 1)} className="font-medium text-primary hover:underline">Retry</button></p>
    : !current?.features ? <p role="status" className="text-xs text-muted-foreground">Checking included features…</p>
    : !features.length ? <p className="text-xs text-muted-foreground">No default features reported.</p>
    : <>
    {starter && !expanded ? <div className="rounded-xl border border-border/70 bg-muted/15 p-4">
      <ol aria-label={`${starter.label}: how it works`} className="space-y-5">
        {starter.flow.map((stage, index) => {
          const related = stage.features.flatMap(id => features.filter(feature => feature.id === id))
          return <li key={stage.label} aria-label={stage.label} className="relative grid min-w-0 grid-cols-[2.25rem_minmax(0,1fr)] gap-x-3">
            {index < starter.flow.length - 1 && <div aria-hidden="true" className="absolute bottom-[-1.25rem] left-[1.0625rem] top-10 border-l border-border" />}
            <span className="relative grid h-9 w-9 place-items-center rounded-xl border border-primary/20 bg-primary/10 text-primary"><stage.icon aria-hidden="true" className="h-[18px] w-[18px]" /></span>
            <div className="min-w-0 pt-0.5">
              <div className="mb-2 flex items-center gap-2"><span aria-hidden="true" className="text-[10px] font-medium tabular-nums text-muted-foreground">0{index + 1}</span><h4 className="text-sm font-semibold leading-5">{stage.label}</h4></div>
              <div className="flex flex-wrap gap-1.5">{related.length ? related.map(item => choice(item, `${index}:${item.id}`)) : <p className="text-xs text-muted-foreground">No matching defaults</p>}</div>
            </div>
          </li>
        })}
      </ol>
    </div> : <div className="flex flex-wrap gap-2 rounded-xl border border-border/70 bg-muted/15 p-4">{features.map(item => choice(item, `all:${item.id}`))}</div>}
    <div id={hintID} aria-live="polite" className={selected ? 'rounded-lg border border-primary/20 bg-primary/5 px-3 py-2.5 text-xs leading-5' : 'px-1 text-xs leading-5'}>
    {selected ? <>
      <p><span className="font-medium">{['voice', 'models', 'costs'].includes(selected.id) ? 'How to use: ' : 'Try in chat: '}</span>{selected.example}</p>
      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 text-[10px] text-muted-foreground"><span>{selected.location}</span>{selected.setup && <span className="inline-flex items-center gap-1"><Plug aria-hidden="true" className="h-2.5 w-2.5" />Set up first</span>}</div>
    </> : <p className="text-xs text-muted-foreground">Included features. Select one for a quick example.</p>}
    </div>
    </>}
  </section>
}
