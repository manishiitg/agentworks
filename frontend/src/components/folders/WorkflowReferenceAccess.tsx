import { useEffect, useMemo } from 'react'
import { Link2, X } from 'lucide-react'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { normalizeWorkspacePath } from '../../utils/workspacePathUtils'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export type AdditionalReadOnlyReference = {
  path: string
  label: string
  icon?: string
}

type WorkflowReferenceAccessProps = {
  selectedPaths: string[]
  onChange: (paths: string[]) => void | Promise<unknown>
  excludeWorkspacePath?: string | null
  disabled?: boolean
  additionalReferences?: AdditionalReadOnlyReference[]
  additionalLabel?: string
  showAdditionalGroup?: boolean
  /** Hide the attach dropdowns: the list stays visible with remove, adding goes through Ask AI. */
  hideAdd?: boolean
  /** More groups of attachable projects (a Code project's other Code projects), each with its own heading and picker. */
  extraGroups?: Array<{ label: string; description: string; placeholder: string; references: AdditionalReadOnlyReference[] }>
}

export function WorkflowReferenceAccess({ selectedPaths, onChange, excludeWorkspacePath, disabled = false, additionalReferences = [], additionalLabel = 'Crew', showAdditionalGroup = false, extraGroups = [], hideAdd = false }: WorkflowReferenceAccessProps) {
  const workflows = useWorkflowManifestStore(state => state.workflows)
  const refreshWorkflows = useWorkflowManifestStore(state => state.refreshWorkflows)
  const normalizedSelected = useMemo(
    () => [...new Set(selectedPaths.map(normalizeWorkspacePath).filter(Boolean))],
    [selectedPaths],
  )
  const excluded = normalizeWorkspacePath(excludeWorkspacePath || '')
  const workflowReferences = workflows.map(workflow => ({
    path: normalizeWorkspacePath(workflow.workspace_path),
    label: workflow.manifest.label,
    icon: workflow.manifest.icon,
  })).filter(reference => reference.path && reference.path !== excluded)
  const crewReferences = additionalReferences.map(reference => ({
    ...reference,
    path: normalizeWorkspacePath(reference.path),
  })).filter(reference => reference.path && reference.path !== excluded)
  const availableWorkflows = workflowReferences.filter(reference => {
    return !normalizedSelected.includes(reference.path)
  })
  const availableCrew = crewReferences.filter(reference => {
    return !normalizedSelected.includes(reference.path)
  })
  const extraReferences = extraGroups.map(group => group.references.map(reference => ({ ...reference, path: normalizeWorkspacePath(reference.path) })).filter(reference => reference.path && reference.path !== excluded))
  const knownReferences = [...workflowReferences, ...crewReferences, ...extraReferences.flat()]
  const unknownSelected = normalizedSelected.filter(path => !knownReferences.some(reference => reference.path === path))

  const add = (path: string) => {
    const normalized = normalizeWorkspacePath(path)
    if (!normalized || normalized === excluded || normalizedSelected.includes(normalized)) return
    void onChange([...normalizedSelected, normalized])
  }

  const remove = (path: string) => void onChange(normalizedSelected.filter(candidate => candidate !== path))
  const renderSelected = (references: AdditionalReadOnlyReference[]) => references
    .filter(reference => normalizedSelected.includes(reference.path))
    .map(reference => (
      <div key={reference.path} className="flex items-center gap-2 rounded-md border border-border bg-muted/30 px-3 py-2">
        <EntityIdentityIcon icon={reference.icon} label={reference.label} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium text-foreground">{reference.label}</div>
          <div className="truncate font-mono text-[10px] text-muted-foreground">{reference.path} · read only</div>
        </div>
        <button type="button" disabled={disabled} onClick={() => remove(reference.path)} aria-label={`Remove ${reference.path}`} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"><X className="h-3.5 w-3.5" /></button>
      </div>
    ))

  const renderGroup = (label: string, description: string, references: AdditionalReadOnlyReference[], available: AdditionalReadOnlyReference[], placeholder: string, canAdd = !hideAdd) => (
    <div className="rounded-md border border-border/80 bg-background/50 p-3">
      <div className="text-xs font-medium text-foreground">{label}</div>
      <p className="mt-0.5 text-[11px] text-muted-foreground">{description}</p>
      {references.some(reference => normalizedSelected.includes(reference.path)) && (
        <div className="mt-2 space-y-2">{renderSelected(references)}</div>
      )}
      {!references.some(reference => normalizedSelected.includes(reference.path)) && !canAdd && (
        <p className="mt-2 text-[11px] text-muted-foreground">Nothing attached.</p>
      )}
      {canAdd && (
      <select disabled={disabled || available.length === 0} value="" onChange={event => add(event.target.value)} className="mt-2 w-full rounded-md border border-border bg-background px-2.5 py-2 text-xs text-foreground disabled:opacity-50">
        <option value="">{available.length > 0 ? placeholder : `No other accessible ${label.toLowerCase()}`}</option>
        {available.map(reference => <option key={reference.path} value={reference.path}>{reference.icon ? `${reference.icon} ` : ''}{reference.label} — {reference.path}</option>)}
      </select>
      )}
    </div>
  )

  useEffect(() => {
    if (workflows.length === 0) void refreshWorkflows()
  }, [refreshWorkflows, workflows.length])

  return (
    <section className="rounded-lg border border-border p-4">
      <div className="flex items-start gap-2">
        <Link2 className="mt-0.5 h-4 w-4 text-muted-foreground" />
        <div>
          <h3 className="text-sm font-medium text-foreground">Read-only context</h3>
          <p className="mt-1 text-xs text-muted-foreground">Attach workflows, Crews or your other projects as durable context. Access is checked again on every run.</p>
        </div>
      </div>
      <div className="mt-3 grid gap-3">
        {renderGroup('Workflows', 'Reusable AgentWorks automations.', workflowReferences, availableWorkflows, 'Attach a workflow…')}
        {showAdditionalGroup && renderGroup(additionalLabel, 'Other persistent Crew projects.', crewReferences, availableCrew, `Attach ${additionalLabel}…`)}
        {extraGroups.map((group, index) => <div key={group.label}>{renderGroup(group.label, group.description, extraReferences[index], extraReferences[index].filter(reference => !normalizedSelected.includes(reference.path)), group.placeholder, true)}</div>)}
        {unknownSelected.length > 0 && <div className="rounded-md border border-border/80 p-3">
          <div className="text-xs font-medium text-foreground">Other linked folders</div>
          <div className="mt-2 space-y-2">{renderSelected(unknownSelected.map(path => ({ path, label: path.split('/').pop() || path })))}</div>
        </div>}
      </div>
    </section>
  )
}
