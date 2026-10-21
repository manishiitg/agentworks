import { useState, type ReactNode } from 'react'
import { Check, ChevronRight, CircleDot, Plug, Sparkles } from 'lucide-react'

export type HostedAiApp = 'chatgpt' | 'cowork' | 'grok' | 'dots' | 'muse-personal'
export type AiApp = HostedAiApp | 'claude-code' | 'codex' | 'cursor' | 'muse' | 'json-client'

export const AI_APPS: { id: AiApp; name: string; icon?: string; description: string; hosted?: boolean }[] = [
  { id: 'chatgpt', name: 'ChatGPT', icon: '/provider-icons/codex.png', description: 'ChatGPT.com', hosted: true },
  { id: 'cowork', name: 'Claude Cowork', icon: '/provider-icons/claude.ico', description: 'Your Claude desktop assistant', hosted: true },
  { id: 'grok', name: 'Grok', description: 'Grok.com', hosted: true },
  { id: 'dots', name: 'OpenAI dots', description: 'Through supported ChatGPT plugins', hosted: true },
  { id: 'muse-personal', name: 'Muse personal agent', icon: '/provider-icons/muse.ico', description: 'Setup not yet verified', hosted: true },
  { id: 'claude-code', name: 'Claude Code', icon: '/provider-icons/claude.ico', description: 'In your terminal' },
  { id: 'codex', name: 'Codex', icon: '/provider-icons/codex.png', description: 'Desktop app or terminal' },
  { id: 'cursor', name: 'Cursor', icon: '/provider-icons/cursor.svg', description: 'In your editor' },
  { id: 'muse', name: 'Muse Code', icon: '/provider-icons/muse.ico', description: 'In your terminal' },
  { id: 'json-client', name: 'Other app', description: 'Any compatible MCP app' },
]

export function isHostedAiApp(app: AiApp | null): app is HostedAiApp {
  return AI_APPS.some(item => item.id === app && item.hosted)
}

/** The same app chooser for local keys and browser-approved connections. */
export function AiAppPicker({ value, onChange, localOnly = false, extraCodingChoices }: { extraCodingChoices?: ReactNode; value: AiApp | null; onChange: (app: AiApp) => void; localOnly?: boolean }) {
  const [changing, setChanging] = useState(false)
  const selected = AI_APPS.find(app => app.id === value)
  const select = (app: AiApp) => { setChanging(false); onChange(app) }
  const choices = (hosted: boolean) => <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {AI_APPS.filter(app => !!app.hosted === hosted).map(app => <button key={app.id} type="button" aria-label={app.name} aria-pressed={value === app.id} onClick={() => select(app.id)} className={`flex min-w-0 items-center gap-3 rounded-lg border p-3 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${value === app.id ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'}`}>
        <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-muted">{app.icon ? <img src={app.icon} alt="" className="h-6 w-6 object-contain" /> : app.id === 'dots' ? <CircleDot className="h-5 w-5" aria-hidden="true" /> : app.id === 'grok' ? <Sparkles className="h-5 w-5" aria-hidden="true" /> : <Plug className="h-5 w-5 text-muted-foreground" aria-hidden="true" />}</span>
        <span className="min-w-0 flex-1"><span className="block text-sm font-medium">{app.name}</span><span className="block text-xs text-muted-foreground">{app.description}</span></span>
        {value === app.id ? <Check className="h-4 w-4 shrink-0 text-primary" aria-hidden="true" /> : <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden="true" />}
      </button>)}
    </div>
  if (selected && !changing) return <section aria-label="Choose your AI app" className="flex min-w-0 items-center gap-3 rounded-lg border border-border p-3">
    {selected.icon ? <img src={selected.icon} alt="" className="h-7 w-7 shrink-0 object-contain" /> : <Plug className="h-6 w-6 shrink-0 text-muted-foreground" aria-hidden="true" />}
    <div className="min-w-0 flex-1"><h2 className="text-sm font-medium">{selected.name}</h2><p className="text-xs text-muted-foreground">{selected.description}</p></div>
    <button type="button" onClick={() => setChanging(true)} className="min-h-8 rounded-md px-2 text-xs text-primary hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Change app</button>
  </section>
  return <section className="space-y-3" aria-label="Choose your AI app">
    <div><h2 className="text-base font-semibold">Which AI app do you use?</h2><p className="mt-1 text-sm text-muted-foreground">Choose an app to see its setup steps.</p></div>
    {choices(true)}
    {localOnly && <p className="text-xs text-muted-foreground">Online assistants need a hosted AgentWorks connection. This copy runs only on your computer.</p>}
    <details className="pt-1" open={value !== null && !isHostedAiApp(value) ? true : undefined}><summary className="w-fit cursor-pointer text-sm text-muted-foreground">Coding apps & other tools</summary><div className="mt-3">{choices(false)}{extraCodingChoices}</div></details>
  </section>
}
