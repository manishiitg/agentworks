import { useState, useEffect, useLayoutEffect, useRef, useMemo, useImperativeHandle, type Ref } from 'react'
import { Layers } from 'lucide-react'
import { workflowManifestApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { loadProductProjects } from '../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../products/work/workData'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { AGENTWORKS_SUBTITLE, AgentWorksIcon, agentWorksFirst, isOwnAgentWorks } from './ui/AgentWorksIdentity'
import { isPlainPickerKey } from '../utils/composerReferences'
import { WorkMark } from '../products/work/WorkMark'
import { RunloopMark } from './branding/RunloopLogo'

interface WorkflowItem {
  presetId: string
  label: string
  workspacePath: string
  kind: 'workflow' | 'crew'
  icon?: string
  secondaryLabel?: string
  shared?: boolean
  /** The person's own AgentWorks (server role "agentworks"): own icon, listed first. */
  agentWorks?: boolean
}

const referenceRowKey = (item: WorkflowItem) => item.presetId

export interface WorkflowSelectionHandle {
  handleKeyDown: (event: KeyboardEvent) => boolean
}

interface WorkflowSelectionDialogProps {
  ref: Ref<WorkflowSelectionHandle>
  isOpen: boolean
  onClose: () => void
  onSelectWorkflow: (workflow: WorkflowItem) => void
  listId: string
  onActiveOptionChange: (id?: string) => void
  searchQuery: string
  position: { bottom: number; left: number }
}

let lastReferences: { userID?: string; workflows: WorkflowItem[]; crews: WorkflowItem[] } | null = null

export function WorkflowSelectionDialog({
  ref,
  isOpen,
  onClose,
  onSelectWorkflow,
  listId,
  onActiveOptionChange,
  searchQuery,
  position,
}: WorkflowSelectionDialogProps) {
  const rowID = (item: WorkflowItem) => `${listId}-${encodeURIComponent(referenceRowKey(item))}`
  const [selectedPresetId, setSelectedPresetId] = useState<string | null>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const userID = useAuthStore(state => state.user?.id)

  useLayoutEffect(() => { setSelectedPresetId(null) }, [isOpen, searchQuery, userID])

  const [referenceResult, setReferenceResult] = useState<{ userID?: string; items: WorkflowItem[]; pending: number } | null>(null)
  const [loadError, setLoadError] = useState(false)
  // Show this account's last lists at once, so typing after # finds a project without waiting, and fetch fresh
  // permission-filtered lists on every opening; each fresh list replaces its cached half as it arrives. The cache
  // is per account and lives only in this page.
  useEffect(() => {
    let cancelled = false
    const cached = lastReferences?.userID === userID ? lastReferences : null
    setReferenceResult(cached ? { userID, items: [...cached.workflows, ...cached.crews], pending: 2 } : null)
    setLoadError(false)
    if (!isOpen) return
    let workflows: WorkflowItem[] = cached?.workflows ?? []
    let crewProjects: WorkflowItem[] = cached?.crews ?? []
    let pending = 2
    const finish = () => {
      pending -= 1
      if (cancelled) return
      lastReferences = { userID, workflows, crews: crewProjects }
      setReferenceResult({ userID, items: [...workflows, ...crewProjects], pending })
    }
    const failed = () => { if (!cancelled) setLoadError(true) }
    // Each fresh, server-authorized list can be used immediately. A slow
    // discovery service must not hold the other product's results hostage.
    void workflowManifestApi.listWorkflowManifests().then(response => {
      workflows = (response.workflows || []).map(workflow => ({
        presetId: `workflow:${workflow.manifest.id || workflow.workspace_path}`,
        label: workflow.manifest.label,
        workspacePath: workflow.workspace_path,
        kind: 'workflow',
        shared: workflow.my_access === 'read',
        icon: workflow.manifest.icon,
      }))
    }).catch(() => { workflows = []; failed() }).finally(finish)
    void loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true }).then(crews => {
      crewProjects = crews.map(crew => {
        const identityName = crew.identity?.name?.trim() || crew.title
        const agentWorks = isOwnAgentWorks(crew)
        return {
          presetId: `crew:${crew.id}`,
          label: identityName,
          secondaryLabel: agentWorks ? AGENTWORKS_SUBTITLE : identityName === crew.title ? undefined : crew.title,
          agentWorks,
          workspacePath: crew.workspacePath,
          kind: 'crew',
          shared: !!crew.shared && crew.shared.canBuild !== true,
          icon: crew.identity?.icon,
        }
      })
    }).catch(() => { crewProjects = []; failed() }).finally(finish)
    return () => { cancelled = true }
  }, [isOpen, userID])
  const allReferences = useMemo(() => {
    const items = isOpen && referenceResult?.userID === userID ? referenceResult?.items || [] : []
    return agentWorksFirst(items, item => !!item.agentWorks)
  }, [isOpen, referenceResult, userID])

  const query = searchQuery.toLowerCase().trim()
  const projectMatches = (project: WorkflowItem) => !query ||
    project.label.toLowerCase().includes(query) ||
    !!project.secondaryLabel?.toLowerCase().includes(query) ||
    project.kind.includes(query) ||
    project.workspacePath.toLowerCase().includes(query)

  // Internal paths stay searchable but are displayed only on hover.
  const filteredWorkflows = (() => {
    if (!query) return allReferences

    const filtered = allReferences.filter(projectMatches)

    filtered.sort((a, b) => {
      const aExact = a.label.toLowerCase() === query
      const bExact = b.label.toLowerCase() === query
      if (aExact && !bExact) return -1
      if (!aExact && bExact) return 1
      const aStarts = a.label.toLowerCase().startsWith(query)
      const bStarts = b.label.toLowerCase().startsWith(query)
      if (aStarts && !bStarts) return -1
      if (!aStarts && bStarts) return 1
      return a.label.localeCompare(b.label)
    })

    return filtered
  })()

  // Preserve the explicitly selected row when the other list finishes loading.
  const selectedIndex = filteredWorkflows.findIndex(item => referenceRowKey(item) === selectedPresetId)
  const selectedWorkflow = filteredWorkflows[selectedIndex]
  const activeOption = isOpen && selectedWorkflow ? rowID(selectedWorkflow) : undefined
  useLayoutEffect(() => { onActiveOptionChange(activeOption) }, [activeOption, onActiveOptionChange])
  useEffect(() => {
    if (selectedIndex >= 0) listRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[selectedIndex]?.scrollIntoView({ block: 'nearest' })
  }, [activeOption, selectedIndex])

  const loadingReferences = !referenceResult || referenceResult.pending > 0
  const nothingMatches = isOpen && !loadError && !loadingReferences && !!query && filteredWorkflows.length === 0
  // Only consume a key from the composer when it does something in the picker.
  // Enter with no explicit selection still sends the person's draft as written.
  useImperativeHandle(ref, () => ({
    handleKeyDown(event) {
      if (!isOpen || nothingMatches || !isPlainPickerKey(event)) return false
      if (event.key === 'Escape') {
        onClose()
        return true
      }
      if (event.key === 'Enter' && selectedWorkflow) {
        onSelectWorkflow(selectedWorkflow)
        return true
      }
      if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && filteredWorkflows.length) {
        const down = event.key === 'ArrowDown'
        const last = filteredWorkflows.length - 1
        const next = selectedIndex < 0 ? (down ? 0 : last) : Math.max(0, Math.min(last, selectedIndex + (down ? 1 : -1)))
        setSelectedPresetId(referenceRowKey(filteredWorkflows[next]))
        return true
      }
      return false
    },
  }))

  useEffect(() => {
    if (nothingMatches) onClose()
  }, [nothingMatches, onClose])

  if (!isOpen || nothingMatches) return null

  return (
    <div
      className="fixed z-50 w-[min(400px,calc(100vw-16px))] rounded-lg border border-border bg-popover text-popover-foreground shadow-lg"
      style={{ bottom: `${position.bottom}px`, left: `${position.left}px` }}
    >
      {/* Header */}
      <div className="px-3 py-2 border-b border-border bg-secondary">
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-muted-foreground" />
          <span className="text-sm font-medium">References</span>
        </div>
      </div>

      {/* Workflow list */}
      <div ref={listRef} id={listId} role="listbox" aria-label="References" className="overflow-y-auto max-h-64">
        {filteredWorkflows.length === 0 ? (
          <div className="px-3 py-4 text-center text-muted-foreground text-sm">
            {loadingReferences ? 'Loading references… You can still send.' : loadError ? 'Could not load references. You can still send.' : 'No available projects. You can still send.'}
          </div>
        ) : (
          filteredWorkflows.map(workflow => (
            <div
              key={workflow.presetId}
              id={rowID(workflow)}
              role="option"
              aria-label={workflow.label}
              aria-selected={selectedWorkflow === workflow}
              title={[workflow.secondaryLabel, workflow.shared ? 'Shared with you · Open from Artifacts' : '', workflow.workspacePath].filter(Boolean).join('\n') || workflow.label}
              className={`px-3 py-2 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                selectedWorkflow === workflow
                  ? 'bg-primary/10 text-primary border-l-2 border-primary'
                  : 'hover:bg-secondary'
              }`}
              onMouseDown={e => e.preventDefault()}
              onClick={() => onSelectWorkflow(workflow)}
            >
              {workflow.agentWorks ? <AgentWorksIcon /> : <EntityIdentityIcon icon={workflow.icon} label={workflow.label} />}
              <div className="flex-1 min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <div className="truncate font-medium">{workflow.label}</div>
                  {workflow.kind === 'crew'
                    ? <WorkMark title="Crew" className="h-4 w-4 shrink-0" />
                    : <RunloopMark title="Workflow" className="h-4 w-4 shrink-0" />}
                  {workflow.shared && <span className="shrink-0 text-[10px] text-muted-foreground">Shared</span>}
                </div>
                {workflow.agentWorks && <div className="truncate text-xs text-muted-foreground">{AGENTWORKS_SUBTITLE}</div>}
              </div>
            </div>
          ))
        )}
      </div>
      {filteredWorkflows.length > 0 && (loadingReferences || loadError) && (
        <div className="border-t border-border px-3 py-1.5 text-xs text-muted-foreground" role="status">
          {loadingReferences ? 'Loading more references…' : 'Some references could not be loaded. Reopen to retry.'}
        </div>
      )}

      {/* Footer */}
      <div className="px-3 py-2 border-t border-border bg-secondary text-xs text-muted-foreground">
        ↑↓ choose · Enter to {selectedWorkflow ? 'select' : 'send'} · Esc to close
      </div>
    </div>
  )
}

export default WorkflowSelectionDialog
