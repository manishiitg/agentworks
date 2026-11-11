import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { create } from 'zustand'
import { agentApi } from '../../services/api'
import type { ChatTab } from '../../stores/useChatStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'

type Decision = { confirmation: string; choices: Record<string, string> }
type Pending = { tab: ChatTab; identity: number; finish: (value: Decision | null) => void }
const useRemoval = create<{ pending: Pending | null }>(() => ({ pending: null }))
export function requestAgentRemoval(tab: ChatTab): Promise<Decision | null> {
  useRemoval.getState().pending?.finish(null)
  return new Promise(resolve => {
    const finish = (decision: Decision | null) => { useRemoval.setState({ pending: null }); resolve(decision) }
    useRemoval.setState({ pending: { tab, identity: captureChatIdentity(), finish } })
  })
}

export function AgentRemovalDialog({ projectId }: { projectId: string }) {
  const pending = useRemoval(state => state.pending)
  const [preview, setPreview] = useState<Awaited<ReturnType<typeof agentApi.previewAgentRemoval>> | null>(null)
  const [choices, setChoices] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const mine = pending?.tab.metadata?.agentProfileProjectId === projectId ? pending : null
  useEffect(() => {
    if (!mine) return
    let active = true
    setPreview(null); setChoices({}); setError('')
    const meta = mine.tab.metadata!
    void agentApi.previewAgentRemoval(meta.agentProfileId!, meta.agentProfileConversationKey!).then(result => {
      if (active && isChatIdentityCurrent(mine.identity)) setPreview(result)
    }).catch(cause => { if (active) setError(cause?.response?.data?.error || 'Could not load linked work. Try again.') })
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') mine.finish(null) }
    window.addEventListener('keydown', escape)
    return () => { active = false; window.removeEventListener('keydown', escape) }
  }, [mine])
  useEffect(() => () => {
    const current = useRemoval.getState().pending
    if (current?.tab.metadata?.agentProfileProjectId === projectId) current.finish(null)
  }, [projectId])
  if (!mine) return null
  const ready = preview && preview.dependencies.every(item => choices[item.id]) && isChatIdentityCurrent(mine.identity)
  return createPortal(<div className="fixed inset-0 z-[120] flex items-center justify-center bg-black/40 p-4" onClick={() => mine.finish(null)}>
    <div role="dialog" aria-modal="true" aria-label="Remove agent" className="max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-xl border border-border bg-popover p-5 shadow-xl" onClick={event => event.stopPropagation()}>
      <h2 className="text-base font-semibold">Remove {mine.tab.name}?</h2>
      <p className="mt-2 text-sm text-muted-foreground">This permanently deletes the agent and its saved chats. To keep them, cancel and hide the agent instead.</p>
      {!preview && !error && <p className="mt-4 text-sm">Checking linked work…</p>}
      {error && <p role="alert" className="mt-4 text-sm text-destructive">{error}</p>}
      {preview && <>
        <ul className="mt-3 list-inside list-disc text-sm text-muted-foreground">{preview.effects.map(effect => <li key={effect}>{effect}</li>)}</ul>
        {preview.dependencies.length === 0 ? <p className="mt-4 text-sm">No linked automations or channels.</p> : <div className="mt-4 space-y-3">
          <p className="text-sm font-medium">Choose what happens to each linked item:</p>
          {preview.dependencies.map(item => <label key={item.id} className="block rounded-lg border border-border p-3 text-sm">
            <span className="block font-medium">{item.name}</span><span className="text-xs text-muted-foreground">{item.kind}</span>
            <select aria-label={`What happens to ${item.name}`} value={choices[item.id] || ''} onChange={event => setChoices(current => ({ ...current, [item.id]: event.target.value }))} className="mt-2 block w-full rounded-md border border-input bg-background p-2">
              <option value="">Choose…</option><option value="remove">Remove this item</option>
              {item.can_reassign && preview.destinations.map(target => <option key={target.key} value={target.key}>Move to {target.name}</option>)}
            </select>
          </label>)}
        </div>}
      </>}
      <div className="mt-5 flex justify-end gap-2">
        <button autoFocus type="button" onClick={() => mine.finish(null)} className="rounded-md px-3 py-2 text-sm hover:bg-muted">Cancel</button>
        <button type="button" disabled={!ready} onClick={() => { if (preview && ready) mine.finish({ confirmation: preview.confirmation, choices }) }} className="rounded-md bg-destructive px-3 py-2 text-sm text-destructive-foreground disabled:opacity-50">Remove agent</button>
      </div>
    </div>
  </div>, document.body)
}
