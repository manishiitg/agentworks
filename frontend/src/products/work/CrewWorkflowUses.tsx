import { useEffect, useState } from 'react'
import { crewWorkflowUsesApi, type CrewWorkflowUses, type CrewWorkflowUsesItem } from '../../api/crewWorkflowUses'

/** Reads what a workflow uses. Undefined while loading or when it could not be read (the page then shows no Uses rows). */
export function useCrewWorkflowUses(workspacePath: string): CrewWorkflowUses | undefined {
  const [state, setState] = useState<{ workspacePath: string; uses?: CrewWorkflowUses }>()
  useEffect(() => {
    let current = true
    crewWorkflowUsesApi.get(workspacePath)
      .then(uses => { if (current) setState({ workspacePath, uses }) })
      .catch(() => { if (current) setState({ workspacePath }) })
    return () => { current = false }
  }, [workspacePath])
  return state?.workspacePath === workspacePath ? state.uses : undefined
}

function Chip({ item, dot }: { item: CrewWorkflowUsesItem; dot?: boolean }) {
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border border-border px-2.5 py-0.5 text-xs text-foreground">
    {dot && <span aria-hidden="true" className={`h-2 w-2 rounded-full ${item.ready ? 'bg-emerald-500' : 'bg-red-500'}`} />}
    {item.value !== undefined ? <><span className="text-muted-foreground">{item.name}</span> {item.value}</> : item.name}
    {dot && !item.ready && <span className="text-muted-foreground">· missing</span>}
  </span>
}

/**
 * What the workflow uses: its inputs, the keys its script needs (names only), the apps and skills a run gets. The server
 * says whether each key and app is there; a missing key gets one line and a way to add it through the Crew chat.
 */
export function CrewWorkflowUsesBlock({ uses, onAsk }: { uses: CrewWorkflowUses; onAsk: (task: string) => void | Promise<void> }) {
  const rows: Array<[string, CrewWorkflowUsesItem[], boolean]> = [
    ['Inputs', [...uses.inputs, ...uses.presets.map(name => ({ name: 'Preset', value: name, ready: true }))], false],
    ['Keys', uses.keys, true],
    ['Apps', uses.apps, true],
    ['Skills', uses.skills.map(name => ({ name, ready: true })), false],
  ].filter(([, items]) => (items as CrewWorkflowUsesItem[]).length > 0) as Array<[string, CrewWorkflowUsesItem[], boolean]>
  const missing = uses.keys.filter(key => !key.ready)
  if (!rows.length) return null
  return <div data-testid="crew-workflow-uses">
    <div className="grid grid-cols-[4.5rem_1fr] items-center gap-x-3 gap-y-2 px-4 py-3">
      {rows.map(([label, items, dot]) => <div key={label} className="contents">
        <span className="text-xs text-muted-foreground">{label}</span>
        <span className="flex flex-wrap gap-1.5">{items.map((item, index) => <Chip key={`${item.name}:${index}`} item={item} dot={dot} />)}</span>
      </div>)}
    </div>
    {missing.map(key => <div key={key.name} role="status" className="mx-4 mb-3 flex items-center gap-3 rounded-lg border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs">
      <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-full bg-red-500" />
      <span className="min-w-0 flex-1">This workflow needs <b>{key.name}</b>, which isn't in your Vault yet.</span>
      <button type="button" onClick={() => void onAsk(`Help me add the key ${key.name} to my Vault for this workflow.`)}
        className="shrink-0 rounded-full border border-border px-3 py-0.5 text-xs hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Add key</button>
    </div>)}
  </div>
}
