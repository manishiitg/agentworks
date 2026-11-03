import { memo } from 'react'
import { Handle, Position } from '@xyflow/react'
import { Variable } from 'lucide-react'
import type { VariablesManifest } from '../../../services/api-types'
import { useWorkflowStore } from '../../../stores/useWorkflowStore'

export interface VariablesNodeData extends Record<string, unknown> {
  manifest: VariablesManifest | null
  relayMode?: boolean
  onOpenSidebar?: () => void
  isLoading?: boolean
}
interface VariablesNodeProps { data: VariablesNodeData; selected?: boolean }

export const VariablesNode = memo(({ data, selected }: VariablesNodeProps) => {
  const chosen = useWorkflowStore(state => state.selectedGroupIds)
  const select = useWorkflowStore(state => state.setSelectedGroupIds)
  const { manifest, relayMode, onOpenSidebar, isLoading } = data
  return <div className={`min-w-[220px] max-w-[300px] rounded-lg border bg-background shadow-sm ${selected ? 'ring-2 ring-primary' : ''}`}>
    <Handle type="target" position={Position.Left} />
    <button className="flex w-full items-center gap-2 border-b px-3 py-2 text-left text-sm font-medium" onClick={event => { event.stopPropagation(); onOpenSidebar?.() }}>
      <Variable className="h-4 w-4" /> {relayMode ? 'Inputs & configuration' : 'Inputs'}
    </button>
    <div className="space-y-2 p-3 text-xs">
      {isLoading ? <p>Loading inputs…</p> : <>
        <p className="text-muted-foreground">{manifest?.variables?.length ? `${manifest.variables.length} input fields` : 'No inputs needed'}</p>
        {!relayMode && !!manifest?.groups?.length && <select aria-label="Saved inputs for the next run" className="nodrag w-full rounded border bg-background p-2" value={chosen.length === 1 ? chosen[0] : ''} onChange={event => select(event.target.value ? [event.target.value] : [])}>
          <option value="">Workflow defaults</option>
          {manifest.groups.map(group => <option key={group.name} value={group.name}>{group.name}</option>)}
        </select>}
        <button className="text-muted-foreground underline" onClick={event => { event.stopPropagation(); onOpenSidebar?.() }}>Edit inputs</button>
      </>}
    </div>
    <Handle type="source" position={Position.Right} />
  </div>
})
VariablesNode.displayName = 'VariablesNode'
export default VariablesNode
