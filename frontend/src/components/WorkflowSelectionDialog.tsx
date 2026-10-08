import React, { useState, useEffect, useRef, useMemo } from 'react'
import { Layers, Search } from 'lucide-react'
import { workflowManifestApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { loadProductProjects } from '../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from '../products/work/workData'
import { EntityIdentityIcon } from './ui/EntityIdentityIcon'
import { PulseIcon } from './workflow/pulseIcon'

interface WorkflowItem {
  presetId: string
  label: string
  workspacePath: string
  kind: 'workflow' | 'crew' | 'pulse'
  icon?: string
  secondaryLabel?: string
}

interface WorkflowSelectionDialogProps {
  isOpen: boolean
  onClose: () => void
  onSelectWorkflow: (workflow: WorkflowItem) => void
  searchQuery: string
  position: { bottom: number; left: number }
  // In a Builder chat, the workflow's Pulse is the first entry: #pulse asks it.
  includePulse?: boolean
}

export const WorkflowSelectionDialog: React.FC<WorkflowSelectionDialogProps> = ({
  isOpen,
  onClose,
  onSelectWorkflow,
  searchQuery: externalSearchQuery,
  position,
  includePulse = false
}) => {
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [localQuery, setLocalQuery] = useState('')
  const searchInputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)

  // Refs so keyboard handler always has fresh values
  const selectedIndexRef = useRef(selectedIndex)
  const onCloseRef = useRef(onClose)
  const onSelectWorkflowRef = useRef(onSelectWorkflow)
  useEffect(() => { selectedIndexRef.current = selectedIndex }, [selectedIndex])
  useEffect(() => { onCloseRef.current = onClose }, [onClose])
  useEffect(() => { onSelectWorkflowRef.current = onSelectWorkflow }, [onSelectWorkflow])

  // Sync external search query into local input
  useEffect(() => {
    if (isOpen) setLocalQuery(externalSearchQuery)
  }, [externalSearchQuery, isOpen])

  // Auto-focus on open, reset on close
  useEffect(() => {
    if (isOpen) {
      setSelectedIndex(0)
      setTimeout(() => searchInputRef.current?.focus(), 50)
    } else {
      setLocalQuery('')
      setSelectedIndex(0)
    }
  }, [isOpen])

  const userID = useAuthStore(state => state.user?.id)
  const [referenceResult, setReferenceResult] = useState<{ userID?: string; items: WorkflowItem[] } | null>(null)
  const [loadError, setLoadError] = useState(false)
  // Fetch fresh permission-filtered workflows and user-scoped Crew projects
  // for every opening. Never reuse stale entries after an account change,
  // access revocation, or failed authorization check.
  useEffect(() => {
    let cancelled = false
    setReferenceResult(null)
    setLoadError(false)
    if (!isOpen) return
    void Promise.all([
      workflowManifestApi.listWorkflowManifests(),
      loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true }),
    ]).then(([response, crews]) => {
      if (cancelled) return
      const workflows: WorkflowItem[] = (response.workflows || []).map(workflow => ({
        presetId: `workflow:${workflow.manifest.id || workflow.workspace_path}`,
        label: workflow.manifest.label,
        workspacePath: workflow.workspace_path,
        kind: 'workflow',
        icon: workflow.manifest.icon,
      }))
      const crewProjects: WorkflowItem[] = crews.map(crew => {
        const identityName = crew.identity?.name?.trim() || crew.title
        return {
          presetId: `crew:${crew.id}`,
          label: identityName,
          secondaryLabel: identityName === crew.title ? undefined : crew.title,
          workspacePath: crew.workspacePath,
          kind: 'crew',
          icon: crew.identity?.icon,
        }
      })
      setReferenceResult({ userID, items: [...workflows, ...crewProjects] })
    }).catch(() => { if (!cancelled) setLoadError(true) })
    return () => { cancelled = true }
  }, [isOpen, userID])
  const allReferences = useMemo(() => {
    const items = isOpen && referenceResult?.userID === userID ? referenceResult?.items || [] : []
    if (!isOpen || !includePulse) return items
    return [{ presetId: 'pulse', label: 'pulse', secondaryLabel: 'Ask this workflow\'s Pulse', workspacePath: '', kind: 'pulse' as const }, ...items]
  }, [includePulse, isOpen, referenceResult, userID])

  // Filter synchronously
  const filteredWorkflows = useMemo<WorkflowItem[]>(() => {
    if (!localQuery.trim()) return allReferences

    const query = localQuery.toLowerCase().trim()
    const filtered = allReferences.filter(w =>
      w.label.toLowerCase().includes(query) ||
      w.secondaryLabel?.toLowerCase().includes(query) ||
      w.kind.includes(query) ||
      w.workspacePath.toLowerCase().includes(query)
    )

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
  }, [localQuery, allReferences])

  const filteredWorkflowsRef = useRef(filteredWorkflows)
  useEffect(() => { filteredWorkflowsRef.current = filteredWorkflows }, [filteredWorkflows])

  // Reset selected index when results change
  useEffect(() => { setSelectedIndex(0) }, [localQuery, allReferences])

  // Scroll selected item into view
  useEffect(() => {
    if (listRef.current && selectedIndex >= 0) {
      const el = listRef.current.children[selectedIndex] as HTMLElement
      el?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    }
  }, [selectedIndex])

  // Stable document-level keydown listener using refs
  useEffect(() => {
    if (!isOpen) return

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCloseRef.current()
      } else if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedIndex(prev => Math.min(prev + 1, filteredWorkflowsRef.current.length - 1))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedIndex(prev => Math.max(prev - 1, 0))
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isOpen])

  // Nothing matches the typed reference (e.g. a ticket number like #1764):
  // close instead of holding the composer's Enter behind an empty picker.
  const nothingMatches = isOpen && !loadError && !!referenceResult && !!localQuery.trim() && filteredWorkflows.length === 0
  useEffect(() => {
    if (nothingMatches) onCloseRef.current()
  }, [nothingMatches])

  if (!isOpen || nothingMatches) return null

  const handleEnter = () => {
    const items = filteredWorkflowsRef.current
    const idx = selectedIndexRef.current
    if (items.length > 0 && idx >= 0 && idx < items.length) {
      onSelectWorkflowRef.current(items[idx])
    }
  }

  return (
    <div
      className="fixed z-50 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg min-w-[300px] max-w-[400px]"
      style={{ bottom: `${position.bottom}px`, left: `${position.left}px` }}
    >
      {/* Header */}
      <div className="px-3 py-2 border-b border-border bg-secondary">
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-muted-foreground" />
          <span className="text-sm font-medium">References</span>
        </div>
      </div>

      {/* Search input */}
      <div className="px-3 py-2 border-b border-border">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted-foreground pointer-events-none" />
          <input
            ref={searchInputRef}
            type="text"
            placeholder="Search workflows and Crews..."
            value={localQuery}
            onChange={e => setLocalQuery(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); handleEnter() }
              else if (e.key === 'ArrowDown') { e.preventDefault(); e.stopPropagation(); setSelectedIndex(prev => Math.min(prev + 1, filteredWorkflowsRef.current.length - 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); e.stopPropagation(); setSelectedIndex(prev => Math.max(prev - 1, 0)) }
              else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onCloseRef.current() }
            }}
            className="w-full pl-7 pr-2 py-1.5 text-xs rounded-md border border-border bg-background text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary"
          />
        </div>
      </div>

      {/* Workflow list */}
      <div ref={listRef} className="overflow-y-auto max-h-64">
        {filteredWorkflows.length === 0 ? (
          <div className="px-3 py-4 text-center text-muted-foreground text-sm">
            {loadError ? 'Unable to load accessible references. Close and reopen to retry.' : !referenceResult ? 'Loading accessible references…' : localQuery ? 'No references found' : 'No accessible workflows or Crews available'}
          </div>
        ) : (
          filteredWorkflows.map((workflow, index) => (
            <div
              key={`${workflow.presetId}-${index}`}
              className={`px-3 py-2 cursor-pointer flex items-center gap-2 text-sm transition-colors ${
                index === selectedIndex
                  ? 'bg-primary/10 text-primary border-l-2 border-primary'
                  : 'hover:bg-secondary'
              }`}
              onMouseDown={e => { e.preventDefault(); onSelectWorkflow(workflow) }}
            >
              {workflow.kind === 'pulse' ? <PulseIcon className="h-5 w-5 shrink-0 text-primary" aria-hidden="true" /> : <EntityIdentityIcon icon={workflow.icon} label={workflow.label} />}
              <div className="flex-1 min-w-0">
                <div className="flex min-w-0 items-center gap-2">
                  <div className="truncate font-medium">{workflow.label}</div>
                  <span className="shrink-0 rounded border border-border px-1 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{workflow.kind}</span>
                </div>
                {workflow.secondaryLabel && <div className="truncate text-xs text-muted-foreground">{workflow.secondaryLabel}</div>}
                {workflow.workspacePath && <div className="text-xs text-muted-foreground truncate">{workflow.workspacePath}</div>}
              </div>
            </div>
          ))
        )}
      </div>

      {/* Footer */}
      <div className="px-3 py-2 border-t border-border bg-secondary text-xs text-muted-foreground">
        <div className="flex items-center justify-between">
          <span>↑↓ navigate</span>
          <span>Enter to select • Esc to close</span>
        </div>
      </div>
    </div>
  )
}

export default WorkflowSelectionDialog
