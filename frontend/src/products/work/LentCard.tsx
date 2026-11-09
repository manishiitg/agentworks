import { useEffect, useState, type ReactNode } from 'react'
import { desktopRequest } from '../../services/desktopConnections'
import type { TeamLent, TeamTarget } from './teamApi'

// One thing of this Mac's Vault the Crew uses that teammates' agents can't use yet, as the Mac's server lists it.
type MacOnlyItem = { key: string; name: string; kind: string; label: string; action: 'copy' | 'move' | ''; note?: string }
type ItemResult = { done?: string; line?: string; vault_url?: string }

const card = 'space-y-2.5 rounded-xl border border-border px-4 py-3 text-sm'
const pill = 'rounded-full bg-sky-500/10 px-2 py-0.5 text-[11.5px] text-sky-700 dark:text-sky-300'
const button = 'ml-auto shrink-0 rounded-full border border-primary bg-primary px-2.5 py-0.5 text-xs font-medium text-primary-foreground disabled:opacity-60'

/**
 * What teammates' agents use from the Crew's owner (owner, 5 Nov, approved mockup "Team tab: what teammates' agents use
 * from you"). The server writes the card: its title and line, and what is ready for the team. In the owner's Mac app,
 * the Mac's own server adds what only this Mac has, with Copy (a secret) or Move (a signed-in app) to the server.
 */
export function LentCard({ lent, target, ask }: { lent: TeamLent; target: TeamTarget; ask?: ReactNode }) {
  const [macOnly, setMacOnly] = useState<MacOnlyItem[]>([])
  const [results, setResults] = useState<Record<string, ItemResult>>({})
  const [busy, setBusy] = useState('')
  // Just copied or moved here: shown as ready until the server's own list has it.
  const [recent, setRecent] = useState<{ name: string; kind: string; done: string }[]>([])
  const endpoint = target.connection && target.localProject ? `/${encodeURIComponent(target.connection)}/crew-sync/${encodeURIComponent(target.localProject)}` : ''

  const load = () => {
    if (!endpoint || !lent.yours) return
    void desktopRequest<{ items?: MacOnlyItem[] }>(endpoint, 'POST', { action: 'lent' }).then(data => setMacOnly(data.items || [])).catch(() => setMacOnly([]))
  }
  useEffect(load, [endpoint, lent.yours])

  const act = async (item: MacOnlyItem) => {
    setBusy(item.key)
    try {
      const result = await desktopRequest<ItemResult>(endpoint, 'POST', { action: item.action === 'copy' ? 'lent_copy' : 'lent_move', key: item.key })
      setResults(current => ({ ...current, [item.key]: result }))
      if (result.done) { setRecent(current => [...current, { name: item.name, kind: item.kind, done: result.done || '' }]); load() }
    } catch (error) {
      setResults(current => ({ ...current, [item.key]: { line: error instanceof Error ? error.message : 'That did not work. Try again.' } }))
    } finally { setBusy('') }
  }

  return <section aria-label={lent.title} className={card}>
    <div>
      <div className="flex items-center gap-2"><h3 className="flex-1 font-semibold">{lent.title}</h3>{ask}</div>
      <p className="text-[12.5px] text-muted-foreground">{lent.line}</p>
    </div>
    <div className={lent.yours ? 'border-t border-border pt-2' : ''}>
      {lent.yours && <p className="mb-1 text-xs text-muted-foreground">Ready for the team</p>}
      {lent.items.length === 0 && recent.length === 0 && macOnly.length === 0 && <p className="text-[12.5px] text-muted-foreground">{lent.empty || 'Nothing yet'}</p>}
      {lent.groups.map(group => <div key={group.title} className="py-1.5">
        <p className="text-[13px]"><span className="font-semibold">{group.title}</span> <span className="text-xs text-muted-foreground">{group.line}</span></p>
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {group.names.map(name => <span key={name} className={`rounded-full border border-border px-2.5 py-px text-[13px] ${group.key ? 'font-mono text-xs' : ''}`}>
            {name}{recent.find(done => done.name === name) && <span className="ml-1.5 text-xs text-emerald-600 dark:text-emerald-400">{recent.find(done => done.name === name)?.done}</span>}
          </span>)}
        </div>
      </div>)}
      {recent.filter(done => !lent.items.some(item => item.name === done.name)).map(done => <div key={`recent:${done.name}`} className="flex items-center gap-2 py-1 text-[13px]">
        <span>{done.name}</span><span className={pill}>On the server</span>
        <span className="text-xs text-emerald-600 dark:text-emerald-400">{done.done}</span>
      </div>)}
    </div>
    {macOnly.length > 0 && <div className="border-t border-border pt-2">
      <p className="mb-1 text-xs text-muted-foreground">Only on your Mac: teammates' agents can't use these yet</p>
      {macOnly.map(item => {
        const result = results[item.key]
        return <div key={item.key} className="py-1 text-[13px]">
          <div className="flex items-center gap-2">
            <span>{item.name}</span><span className="text-xs text-muted-foreground">· {item.label}</span>
            {item.action
              ? result?.vault_url
                ? <button type="button" className={button} onClick={() => window.electronAPI?.openExternal?.(result.vault_url || '')}>Open the server's Vault</button>
                : <button type="button" className={button} disabled={busy === item.key} onClick={() => void act(item)}>{item.action === 'copy' ? 'Copy to the server' : 'Move to the server'}</button>
              : <span className="ml-auto max-w-[220px] text-right text-xs text-muted-foreground">{item.note}</span>}
          </div>
          {result?.line && <p className="text-xs text-amber-600 dark:text-amber-400">{result.line}</p>}
        </div>
      })}
    </div>}
    {lent.foot && <p className="text-xs text-muted-foreground">{lent.foot}</p>}
  </section>
}
