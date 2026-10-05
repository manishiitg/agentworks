import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { FolderOpen, LoaderCircle, Plus } from 'lucide-react'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { SettingsCard } from '../ui/SettingsCard'
import { workflowManifestApi } from '../../services/api'
import type { WorkflowFolderAccessRequest, WorkflowFolderGrant } from '../../services/api-types'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { KnowledgebaseSources } from './KnowledgebaseSources'
import { ProjectKnowledgebasePanel } from './ProjectKnowledgebasePanel'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { aliasFromPath } from '../../utils/folderAlias'
import { READ_ONLY_TITLE, useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { FolderGrantList } from '../folders/FolderGrantList'
import { WorkflowReferenceAccess } from '../folders/WorkflowReferenceAccess'
import { AskAIButton } from './AskAIButton'
import { getIdentityTabAskAIMessage } from './workspaceAskAI'

interface WorkflowFolderAccessViewProps {
  workspacePath: string | null
  headerAction?: React.ReactNode
  /** Embedded under the Identity tab: no header, joins the pane scroll. */
  hideHeader?: boolean
  manageOwnScroll?: boolean
}

function grantID(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  return `folder-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function legacyFolderRequest(request: WorkflowFolderAccessRequest): { path: string; reason: string } | null {
  const match = request.reason.match(/^Folder:\s*(.+?)\s+(?:—|–|-)\s+(.+)$/s)
  if (!match) return null
  const path = match[1].trim()
  const isAbsolute = path.startsWith('/') || /^[a-zA-Z]:[\\/]/.test(path) || path.startsWith('\\\\')
  return isAbsolute ? { path, reason: match[2].trim() } : null
}

function requestedPathFor(request: WorkflowFolderAccessRequest): string {
  return request.requested_path?.trim() || legacyFolderRequest(request)?.path || ''
}

function requestReasonFor(request: WorkflowFolderAccessRequest): string {
  return legacyFolderRequest(request)?.reason || request.reason
}

export default function WorkflowFolderAccessView({ workspacePath, headerAction, hideHeader = false, manageOwnScroll = true }: WorkflowFolderAccessViewProps) {
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  // Host folder picking needs the desktop app's native dialog; the whole
  // external-folders box stays hidden on server deployments.
  const canPickFolders = typeof window !== 'undefined' && !!window.electronAPI?.pickWorkflowFolder
  const [grants, setGrants] = useState<WorkflowFolderGrant[]>([])
  const [requests, setRequests] = useState<WorkflowFolderAccessRequest[]>([])
  const [workflowContextPaths, setWorkflowContextPaths] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pendingPath, setPendingPath] = useState('')
  const [pendingAlias, setPendingAlias] = useState('')
  const [pendingAccess, setPendingAccess] = useState<'read_only' | 'read_write'>('read_only')
  const [pendingReason, setPendingReason] = useState('')
  const [activeRequestID, setActiveRequestID] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!workspacePath) return
    setLoading(true)
    setError(null)
    try {
      const response = await workflowManifestApi.getWorkflowManifest(workspacePath)
      setGrants(response.manifest.folder_access || [])
      setRequests(response.manifest.folder_access_requests || [])
      setWorkflowContextPaths(response.manifest.workflow_context_paths || [])
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Unable to load attached folders')
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => {
    void load()
  }, [load])

  const canAdd = useMemo(() => {
    const alias = pendingAlias.trim().toLowerCase()
    return Boolean(pendingPath && alias && !grants.some(grant => grant.alias.toLowerCase() === alias))
  }, [grants, pendingAlias, pendingPath])

  const chooseFolder = useCallback(async (request?: WorkflowFolderAccessRequest) => {
    if (!canWriteWorkflow) return
    setError(null)
    if (!window.electronAPI?.pickWorkflowFolder) {
      setError('Folder attachment requires the AgentWorks desktop app so the host folder can be selected safely.')
      return
    }
    const selected = await window.electronAPI.pickWorkflowFolder()
    if (!selected) return
    setPendingPath(selected)
    setPendingAlias(request?.alias || aliasFromPath(selected))
    setPendingAccess(request?.access || 'read_only')
    setPendingReason(request?.reason || '')
    setActiveRequestID(request?.id || null)
  }, [canWriteWorkflow])

  const persist = useCallback(async (next: WorkflowFolderGrant[], nextRequests = requests) => {
    if (!canWriteWorkflow) return false
    if (!workspacePath) return false
    setSaving(true)
    setError(null)
    try {
      await workflowManifestApi.updateWorkflowManifest({ workspace_path: workspacePath, folder_access: next, folder_access_requests: nextRequests })
      setGrants(next)
      setRequests(nextRequests)
      await useWorkflowManifestStore.getState().refreshWorkflows()
      return true
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Unable to update attached folders')
      return false
    } finally {
      setSaving(false)
    }
  }, [canWriteWorkflow, requests, workspacePath])

  const addGrant = useCallback(async () => {
    if (!canAdd) return
    if (pendingAccess === 'read_write' && !window.confirm(`Allow this workflow to modify files in ${pendingPath}?`)) return
    const now = new Date().toISOString()
    const next = [...grants, {
      id: grantID(),
      alias: pendingAlias.trim(),
      path: pendingPath,
      access: pendingAccess,
      reason: pendingReason.trim() || undefined,
      created_at: now,
      updated_at: now,
    }]
    const nextRequests = activeRequestID ? requests.filter(request => request.id !== activeRequestID) : requests
    if (await persist(next, nextRequests)) {
      setPendingPath('')
      setPendingAlias('')
      setPendingAccess('read_only')
      setPendingReason('')
      setActiveRequestID(null)
    }
  }, [activeRequestID, canAdd, grants, pendingAccess, pendingAlias, pendingPath, pendingReason, persist, requests])

  const dismissRequest = useCallback(async (request: WorkflowFolderAccessRequest) => {
    await persist(grants, requests.filter(candidate => candidate.id !== request.id))
  }, [grants, persist, requests])

  const approveRequest = useCallback(async (request: WorkflowFolderAccessRequest) => {
    const requestedPath = requestedPathFor(request)
    if (!requestedPath) return
    const now = new Date().toISOString()
    const next = [...grants, {
      id: grantID(),
      alias: request.alias,
      path: requestedPath,
      access: request.access,
      reason: requestReasonFor(request) || undefined,
      created_at: now,
      updated_at: now,
    }]
    await persist(next, requests.filter(candidate => candidate.id !== request.id))
  }, [grants, persist, requests])

  const removeGrant = useCallback(async (grant: WorkflowFolderGrant) => {
    if (!window.confirm(`Remove access to ${grant.alias}? Open workflow sessions will lose this folder immediately.`)) return
    await persist(grants.filter(candidate => candidate.id !== grant.id))
  }, [grants, persist])

  const changeAccess = useCallback(async (grant: WorkflowFolderGrant, access: 'read_only' | 'read_write') => {
    if (access === grant.access) return
    if (access === 'read_write' && !window.confirm(`Allow this workflow to modify files in ${grant.path}?`)) return
    const now = new Date().toISOString()
    await persist(grants.map(candidate => candidate.id === grant.id ? { ...candidate, access, updated_at: now } : candidate))
  }, [grants, persist])

  const persistWorkflowReferences = useCallback(async (paths: string[]) => {
    if (!canWriteWorkflow || !workspacePath) return
    setSaving(true)
    setError(null)
    try {
      const response = await workflowManifestApi.updateWorkflowManifest({ workspace_path: workspacePath, workflow_context_paths: paths })
      setWorkflowContextPaths(response.manifest.workflow_context_paths || paths)
      await useWorkflowManifestStore.getState().refreshWorkflows()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Unable to update linked workflows')
    } finally {
      setSaving(false)
    }
  }, [canWriteWorkflow, workspacePath])

  return (
        <div className={`flex w-full max-w-none flex-col bg-background ${manageOwnScroll ? 'h-full min-h-0' : ''}`}>
          {!hideHeader && (
            <WorkspaceViewHeader
              icon={FolderOpen}
              title="Attached folders"
              subtitle="Folders and shared knowledge bases this workflow can access."
              actions={<>
                {!canWriteWorkflow && <span className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-[11px] font-medium text-amber-700 dark:text-amber-300">Read-only</span>}
                {headerAction}
              </>}
            />
          )}

          <div className={`space-y-5 ${manageOwnScroll ? 'min-h-0 flex-1 overflow-y-auto p-5' : ''}`}>
            <div className="flex shrink-0 flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 p-3">
              <div className="min-w-0 flex-1 basis-48">
                <p className="text-sm font-semibold text-foreground">Need to attach something?</p>
                <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
                  Ask the builder to attach folders, shared knowledge, or other workflows — no manual setup needed.
                </p>
              </div>
              <AskAIButton
                workspacePath={!canWriteWorkflow ? null : workspacePath ?? null}
                label="Ask AI to add"
                message={getIdentityTabAskAIMessage('folders')}
                className="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg border border-border px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
              />
            </div>
            {workspacePath && <ProjectKnowledgebasePanel key={`shared-${workspacePath}`} workspacePath={workspacePath} disabled={!canWriteWorkflow} />}
            {workspacePath && <KnowledgebaseSources key={workspacePath} workspacePath={workspacePath} variant="folders" />}
            <WorkflowReferenceAccess selectedPaths={workflowContextPaths} onChange={persistWorkflowReferences} excludeWorkspacePath={workspacePath} disabled={!canWriteWorkflow || saving} hideAdd />
            {canPickFolders && (<>
            <SettingsCard
              icon={<FolderOpen aria-hidden="true" className="h-4 w-4 text-primary" />}
              title="External folders"
              count={`${grants.length} attached`}
            >
              {error && <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{error}</div>}
              {loading ? (
                <div className="flex justify-center py-8"><LoaderCircle className="h-5 w-5 animate-spin text-muted-foreground" /></div>
              ) : (
                <FolderGrantList grants={grants} disabled={!canWriteWorkflow || saving} onAccessChange={changeAccess} onRemove={removeGrant} />
              )}
              <div className="mt-3 border-t border-border/60 pt-3">
                <Button type="button" variant="outline" size="sm" disabled={!canWriteWorkflow} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onClick={() => void chooseFolder()}><FolderOpen className="h-4 w-4" />Choose folder…</Button>
                {pendingPath && (
                  <div className="mt-3 grid gap-3 sm:grid-cols-2">
                    <label className="text-xs text-muted-foreground">Alias<Input value={pendingAlias} disabled={!canWriteWorkflow} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onChange={event => setPendingAlias(event.target.value)} className="mt-1" /></label>
                    <label className="text-xs text-muted-foreground">Access<select value={pendingAccess} disabled={!canWriteWorkflow} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onChange={event => setPendingAccess(event.target.value as 'read_only' | 'read_write')} className="mt-1 w-full rounded-md border border-border bg-background px-2.5 py-2 text-sm text-foreground disabled:cursor-not-allowed disabled:opacity-50"><option value="read_only">Read only</option><option value="read_write">Read & write</option></select></label>
                    <label className="text-xs text-muted-foreground sm:col-span-2">Reason (optional)<Input value={pendingReason} disabled={!canWriteWorkflow} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onChange={event => setPendingReason(event.target.value)} className="mt-1" placeholder="Why this workflow needs the folder" /></label>
                    <div className="truncate text-xs text-muted-foreground sm:col-span-2" title={pendingPath}>{pendingPath}</div>
                    <Button type="button" size="sm" disabled={!canWriteWorkflow || !canAdd || saving} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onClick={() => void addGrant()} className="w-fit"><Plus className="h-3.5 w-3.5" />Attach folder</Button>
                  </div>
                )}
              </div>
            </SettingsCard>

            {!loading && requests.length > 0 && (
              <div className="space-y-2">
                <div className="text-sm font-medium text-foreground">Pending requests</div>
                {requests.map(request => {
                  const requestedPath = requestedPathFor(request)
                  return <div key={request.id} className="rounded-lg border border-primary/30 bg-primary/5 p-3">
                    <div className="flex items-start gap-3">
                      <FolderOpen className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
                      <div className="min-w-0 flex-1">
                        <div className="text-sm font-medium text-foreground">{request.alias}</div>
                        <div className="mt-1 text-xs text-muted-foreground">{request.access === 'read_write' ? 'Read & write' : 'Read only'} · {requestReasonFor(request)}</div>
                        {requestedPath && <div className="mt-1 truncate text-xs text-muted-foreground" title={requestedPath}>{requestedPath}</div>}
                      </div>
                      {requestedPath ? (
                        <Button type="button" size="sm" disabled={!canWriteWorkflow || saving} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onClick={() => void approveRequest(request)}>Approve</Button>
                      ) : (
                        <Button type="button" size="sm" disabled={!canWriteWorkflow || saving} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onClick={() => void chooseFolder(request)}>Choose folder</Button>
                      )}
                      <Button type="button" variant="ghost" size="sm" disabled={!canWriteWorkflow || saving} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined} onClick={() => void dismissRequest(request)}>Deny</Button>
                    </div>
                  </div>
                } )}
              </div>
            )}

            <p className="text-[11px] leading-relaxed text-muted-foreground">Attached folders are host-local, exposed to shells as WORKFLOW_FOLDER_* variables; safe patches can use the linked:// alias. Existing tools are not duplicated or restored.</p>
            </>)}
          </div>
        </div>
  )
}
