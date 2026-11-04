import { lazy, Suspense, useEffect, useState } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { Fingerprint, FolderOpen, Loader2, Lock, Settings, Tag, Target, Trash2, X } from 'lucide-react'
import { FolderGrantList } from '../../components/folders/FolderGrantList'
import { WorkflowReferenceAccess } from '../../components/folders/WorkflowReferenceAccess'
import { Button } from '../../components/ui/Button'
import { IconUploadField } from '../../components/ui/IconUploadField'
import { SettingsCard } from '../../components/ui/SettingsCard'
import { ToggleRow } from '../../components/ui/ToggleRow'
import { Input } from '../../components/ui/Input'
import { Label } from '../../components/ui/label'
import { Textarea } from '../../components/ui/Textarea'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { StatusBanner } from '../../components/workflow/bots/StatusBanner'
import { productMode, type ProductIdentity } from '../../platform/chat/productProjects'
import type { ProductIdentityPatch } from '../../platform/chat/productProjects'
import api, { workFolderApi } from '../../services/api'
import type { PresetLLMConfig, WorkFolderGrant } from '../../services/api-types'
import { loadWorkSessions } from './workSessions'
import { useProjectProduct } from './projectProduct'
import { isWorkIdentityTabEnabled } from './workViewGating'
import { WorkModelsPanel } from './WorkModelsPanel'
import { ChatInstructionsCard } from '../../components/workflow/ChatInstructionsCard'
import { ProjectInstructionsCard } from './ProjectInstructionsCard'
import type { WorkRuntimeSelection } from './workTabs'
import { useChatStore } from '../../stores/useChatStore'

import { localFileError, useProjectMode } from './codeLocalFiles'
import { isLocalDeployment } from '../productSurfaceConfig'
import { CODE_MODE_CHOICES } from './CreateCodeWorkspaceDialog'

const CodeLocalFilesSettings = lazy(() => import('./CodeFilesPanel').then(module => ({ default: module.CodeLocalFilesSettings })))

export type WorkIdentityTab = 'general' | 'models' | 'location'

const IDENTITY_TABS: Array<{ value: WorkIdentityTab; label: string }> = [
  { value: 'general', label: 'General' },
  { value: 'models', label: 'Models' },
  // Code only: where this chat's files and commands live (the server, or your own computer through the CLI).
  { value: 'location', label: 'Laptop access' },
]

// Code has a name only: rename and delete, no identity or purpose.
/** The mode this project was created in. It cannot change, so it is shown as plain text. */
function ModeRow({ sessionId }: { sessionId: string }) {
  const mode = useProjectMode(sessionId)
  const choice = CODE_MODE_CHOICES.find(item => item.mode === mode) ?? CODE_MODE_CHOICES[0]
  return <SettingsCard icon={<choice.icon aria-hidden="true" className="h-4 w-4 text-primary" />} title={`Mode: ${choice.title}`} description={choice.text}>
    {null}
  </SettingsCard>
}


function WorkGeneralPanel({ projectTitle, projectPurpose, projectIdentity, onUpdateIdentity, onDeleteRequest }: {
  projectTitle: string
  projectPurpose: string
  projectIdentity?: ProductIdentity
  onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>
  onDeleteRequest: () => void
}) {
  const [nameDraft, setNameDraft] = useState('')
  const [iconDraft, setIconDraft] = useState('')
  const [purposeDraft, setPurposeDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setNameDraft(projectIdentity?.name ?? '')
    setIconDraft(projectIdentity?.icon ?? '')
    setPurposeDraft(projectPurpose)
    setError(null)
  }, [projectIdentity?.name, projectIdentity?.icon, projectPurpose])

  const displayName = nameDraft.trim() || projectTitle
  const dirty = nameDraft.trim() !== (projectIdentity?.name ?? '')
    || iconDraft.trim() !== (projectIdentity?.icon ?? '')
    || purposeDraft.trim() !== projectPurpose

  const save = async () => {
    if (!dirty || saving) return
    setSaving(true)
    setError(null)
    try {
      await onUpdateIdentity({
        name: nameDraft.trim(),
        icon: iconDraft.trim(),
        purpose: purposeDraft.trim(),
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Unable to save.')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="space-y-4">
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      <SettingsCard
        icon={<Tag aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Name and icon"
      >
        <div>
          <Label className="mb-2 block">Project name</Label>
          <Input
            value={nameDraft}
            onChange={event => setNameDraft(event.target.value)}
            disabled={saving}
            placeholder={projectTitle}
          />
        </div>
        <div>
          <Label className="mb-2 block">Icon</Label>
          <IconUploadField
            value={iconDraft}
            onChange={setIconDraft}
            label={displayName}
            disabled={saving}
            inputAriaLabel="Project icon"
          />
        </div>
        <div className="flex justify-end">
          <Button onClick={() => void save()} disabled={!dirty || saving}>
            {saving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving…</> : 'Save'}
          </Button>
        </div>
      </SettingsCard>

      <SettingsCard
        icon={<Target aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="What this Crew is for"
      >
        <div>
          <Textarea
            value={purposeDraft}
            onChange={event => setPurposeDraft(event.target.value)}
            disabled={saving}
            aria-label="What this Crew is for"
            placeholder="e.g. Plan and ship the Q3 launch."
            rows={3}
          />
        </div>
        <div className="flex justify-end">
          <Button onClick={() => void save()} disabled={!dirty || saving}>
            {saving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving…</> : 'Save'}
          </Button>
        </div>
      </SettingsCard>


      <SettingsCard
        icon={<Trash2 aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Delete project"
        description="Removes the project folder and everything in it. This cannot be undone."
      >
        <div>
          <Button variant="destructive" onClick={onDeleteRequest} disabled={saving}>
            <Trash2 className="h-4 w-4" /> Delete project
          </Button>
        </div>
      </SettingsCard>
    </div>
  )
}

export function WorkFoldersSection({ workspacePath, workflowContextPaths, onWorkflowContextPathsChange, onOpenComputer }: {
  workspacePath: string
  workflowContextPaths: string[]
  onOpenComputer?: () => void
  onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
}) {
  const [folders, setFolders] = useState<WorkFolderGrant[]>([])
  const [roots, setRoots] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [crewReferences, setCrewReferences] = useState<Array<{ path: string; label: string; icon?: string }>>([])
  // A Code project can also attach the person's other Code and Cowork projects (their files, read-only). Not Local ones (their
  // files are on a computer), and not for a Crew, which others can open.
  const product = useProjectProduct()


  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    void workFolderApi.listWorkFolders().then(response => {
      if (cancelled) return
      setFolders(response.folders || [])
      setRoots(response.roots || [])
    }).catch((cause: unknown) => {
      if (!cancelled) setError(localFileError(cause))
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })
    return () => { cancelled = true }
  }, [workspacePath])

  useEffect(() => {
    let cancelled = false
    void loadWorkSessions().then(sessions => {
      if (cancelled) return
      setCrewReferences(sessions.map(session => ({
        path: session.workspacePath,
        label: session.identity?.name || session.title,
        icon: session.identity?.icon,
      })))
    }).catch(() => { if (!cancelled) setCrewReferences([]) })
    return () => { cancelled = true }
  }, [])



  const refresh = async () => {
    setLoading(true)
    setError('')
    try {
      const response = await workFolderApi.listWorkFolders()
      setFolders(response.folders || [])
      setRoots(response.roots || [])
    } catch (cause) {
      setError(localFileError(cause))
    } finally {
      setLoading(false)
    }
  }

  // In the Mac app, pick a folder on this computer and attach it (read only or read & write).
  const [addAccess, setAddAccess] = useState<'read_only' | 'read_write'>('read_only')
  const [adding, setAdding] = useState(false)
  const local = isLocalDeployment()
  const canPick = local && Boolean(window.electronAPI?.pickWorkflowFolder)
  // On the Mac, agents may use the Downloads folder by default; the person can remove it (every agent sandbox then blocks it).
  const [downloads, setDownloads] = useState<{ available: boolean; removed?: boolean; home?: string }>({ available: false })
  useEffect(() => { void api.get<{ available: boolean; removed?: boolean; home?: string }>('/api/work/mac-downloads').then(({ data }) => setDownloads(data)).catch(() => {}) }, [])
  // "Your whole Mac" (owner, 4 Nov): one click attaches the home folder read & write, as an ordinary attached folder, so it
  // is removed like any other. macOS system folders stay out.
  const home = downloads.available ? (downloads.home || '').replace(/\/+$/, '') : ''
  const wholeMac = home ? folders.find(folder => folder.path.replace(/\/+$/, '') === home) : undefined
  const attachWholeMac = async () => {
    if (!home) return
    setAdding(true); setError('')
    try { await workFolderApi.addWorkFolder({ path: home, alias: 'mac', access: 'read_write' }); await refresh() }
    catch (cause) { setError(localFileError(cause)) }
    finally { setAdding(false) }
  }
  const setDownloadsRemoved = async (removed: boolean) => {
    setError('')
    try { const { data } = await api.put<{ available: boolean; removed?: boolean }>('/api/work/mac-downloads', { removed }); setDownloads(data) }
    catch (cause) { setError(localFileError(cause)) }
  }
  const add = async () => {
    if (!canPick) return
    const path = await window.electronAPI?.pickWorkflowFolder?.()
    if (!path) return
    setAdding(true); setError('')
    try {
      const alias = path.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || 'folder'
      await workFolderApi.addWorkFolder({ path, alias, access: addAccess })
      await refresh()
    } catch (cause) {
      setError(localFileError(cause))
    } finally { setAdding(false) }
  }

  const remove = async (folder: WorkFolderGrant) => {
    setError('')
    try {
      await workFolderApi.deleteWorkFolder(folder.id)
      await refresh()
    } catch (cause) {
      setError(localFileError(cause))
    }
  }

  return (
    <div className="space-y-4">
      {roots.length > 0 && <p className="text-[11px] text-muted-foreground">Allowed roots: {roots.join(', ')}</p>}
      <WorkflowReferenceAccess
        selectedPaths={workflowContextPaths}
        onChange={onWorkflowContextPathsChange}
        excludeWorkspacePath={workspacePath}
        additionalReferences={crewReferences}
        additionalLabel="Crew"
        showAdditionalGroup
      />
      <SettingsCard
        icon={<FolderOpen aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Attached folders"
        description={local ? "Folders on your Mac that your Crews can open. They stay on your Mac." : "Folders available on this server. Use Your computer for folders on your laptop."}
        count={`${folders.length} attached`}
      >
        {home && !wholeMac && <div className="mb-2 flex items-center gap-3 rounded-lg border border-border bg-muted/20 p-3">
          <FolderOpen aria-hidden="true" className="h-4 w-4 shrink-0 text-primary" />
          <div className="min-w-0 flex-1"><p className="text-sm font-medium">Your whole Mac</p><p className="text-xs text-muted-foreground">Everything in your home folder ({home}), read &amp; write, for all your Crews. macOS system folders stay out.</p></div>
          <button type="button" disabled={adding} onClick={() => void attachWholeMac()} className="shrink-0 rounded-md border border-border px-2.5 py-1 text-xs font-medium hover:bg-muted disabled:opacity-50">Give access</button>
        </div>}
        {/* On a person's own Mac agents may use their Downloads folder by default; it can be removed and added back. */}
        {downloads.available && !downloads.removed && <div className="mb-2 flex items-center gap-3 rounded-lg border border-border bg-muted/20 p-3">
          <FolderOpen aria-hidden="true" className="h-4 w-4 shrink-0 text-primary" />
          <div className="min-w-0 flex-1"><p className="text-sm font-medium">Downloads</p><p className="text-xs text-muted-foreground">Your Mac's Downloads folder. On by default.</p></div>
          <span className="shrink-0 text-xs text-muted-foreground">Read &amp; write</span>
          <button type="button" aria-label="Remove Downloads" onClick={() => void setDownloadsRemoved(true)} className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"><X aria-hidden="true" className="h-3.5 w-3.5" /></button>
        </div>}
        {downloads.available && downloads.removed && <button type="button" onClick={() => void setDownloadsRemoved(false)} className="mb-2 text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline">Add Downloads back</button>}
        {loading ? <p className="text-xs text-muted-foreground">Loading…</p> : <FolderGrantList grants={folders} onRemove={remove} />}
        {canPick ? <div className="mt-3 flex flex-wrap items-center gap-2">
          <select aria-label="Access for the new folder" value={addAccess} disabled={adding} onChange={event => setAddAccess(event.target.value as 'read_only' | 'read_write')} className="h-8 rounded-md border border-border bg-background px-2 text-xs">
            <option value="read_only">Read only</option><option value="read_write">Read &amp; write</option>
          </select>
          <button type="button" disabled={adding} onClick={() => void add()} className="h-8 rounded-md border border-border px-3 text-xs hover:bg-muted disabled:opacity-50">{adding ? 'Adding…' : 'Add folder'}</button>
        </div> : <div className="mt-3 text-xs text-muted-foreground">{local ? 'Add folders from the AgentWorks Mac app.' : onOpenComputer && <button type="button" onClick={onOpenComputer} className="underline underline-offset-2 hover:text-foreground">Open Your computer</button>}</div>}
        {error && <p className="text-xs text-destructive">{error}</p>}
      </SettingsCard>
    </div>
  )
}

export function WorkIdentityPanel({ workspacePath, projectId, shared, projectTitle, projectDescription, projectIdentity, tabId, openLocalFilesSettings, onLocalFilesSettingsOpened, selectedSecrets, selectedGlobalSecrets, projectLLMConfig, enabledPanels, onRuntimeChange, nativeAgentTools, onNativeAgentToolsChange, onSelectedSecretsChange, onSelectedGlobalSecretsChange, onUpdateIdentity, onDeleteRequest, modelsOnly = false }: {
  workspacePath: string
  /** A Crew opened by a reader: the project instructions editor is the owner's. */
  shared?: boolean
  /** The project's id. */
  projectId?: string
  projectTitle: string
  projectDescription: string
  projectIdentity?: ProductIdentity
  tabId: string
  modelsOnly?: boolean
  openLocalFilesSettings?: boolean
  onLocalFilesSettingsOpened?: () => void
  selectedSecrets: string[]
  selectedGlobalSecrets: string[]
  projectLLMConfig?: PresetLLMConfig
  enabledPanels?: Set<string>
  onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>
  nativeAgentTools?: boolean
  /** Omitted for readers: only the Crew owner can change it. */
  onNativeAgentToolsChange?: (enabled: boolean) => Promise<unknown>
  onSelectedSecretsChange: (secrets: string[]) => Promise<unknown>
  onSelectedGlobalSecretsChange: (secrets: string[]) => Promise<unknown>
  onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>
  onDeleteRequest: () => void
}) {
  const product = useProjectProduct()
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId || '')
  const visibleTabs = IDENTITY_TABS.filter(option => (!modelsOnly || option.value === 'models') && isWorkIdentityTabEnabled(option.value, enabledPanels))
  const [tab, setTab] = usePersistentTab<WorkIdentityTab>('agentworks.tab.crew-identity', 'general', IDENTITY_TABS.map(option => option.value))
  useEffect(() => {
    if (!openLocalFilesSettings) return
    setTab('location')
    onLocalFilesSettingsOpened?.()
  }, [openLocalFilesSettings, onLocalFilesSettingsOpened, setTab])
  const activeTab = visibleTabs.some(option => option.value === tab) ? tab : visibleTabs[0].value
  useWorkspaceViewTarget('identity', target => { if (visibleTabs.some(option => option.value === target)) setTab(target as WorkIdentityTab) })
  // Every tab loads on mount, so Refresh always remounts.
  const [tabNonce, setTabNonce] = useState(0)

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={product.hasIdentity ? Fingerprint : Settings}
        title={modelsOnly ? 'Models' : 'Settings'}
        actions={(
          <WorkspaceViewActions
            onRefresh={() => setTabNonce(nonce => nonce + 1)}
            refreshLabel={`Refresh ${visibleTabs.find(option => option.value === activeTab)?.label ?? 'view'}`}
          />
        )}
        /* No sub-tabs: Settings, Models and Laptop access are separate items in the side list (owner, 2026-10-20). */
      />
      <div key={`${activeTab}:${tabNonce}`} className="min-h-0 flex-1 overflow-y-auto p-4">

        {activeTab === 'general' && product.hasIdentity && <WorkGeneralPanel
          projectTitle={projectTitle}
          projectPurpose={projectDescription}
          projectIdentity={projectIdentity}
          onUpdateIdentity={onUpdateIdentity}
          onDeleteRequest={onDeleteRequest}
        />}
        {activeTab === 'general' && product.hasIdentity && <ChatInstructionsCard workspacePath={workspacePath} />}
        {activeTab === 'general' &&  <ModeRow sessionId={sessionId} />}
        {activeTab === 'general' && !shared && <div className="mt-4"><ProjectInstructionsCard workspacePath={workspacePath} /></div>}
        {activeTab === 'location' && <Suspense fallback={<p className="text-sm text-muted-foreground">Loading connection settings…</p>}><CodeLocalFilesSettings sessionId={sessionId} workspaceName={projectIdentity?.name?.trim() || projectTitle} /></Suspense>}
        {activeTab === 'models' && <div className="space-y-4">
          <WorkModelsPanel
            tabId={tabId}
            workspacePath={workspacePath}
            projectId={projectId}
            projectLLMConfig={projectLLMConfig}
            onRuntimeChange={onRuntimeChange}
            hideHeader
          />
          {!modelsOnly && product.hasNativeAgentToolsSetting && <NativeAgentToolsSetting enabled={!!nativeAgentTools} onChange={onNativeAgentToolsChange} />}
        </div>}
      </div>
    </div>
  )
}

/**
 * Crew and Code "Native agent tools" switch (workflow.json capabilities.native_agent_tools).
 * On: supported coding agents may use native read and search tools. The
 * exact native set varies by CLI; file changes stay on AgentWorks tools.
 */
function NativeAgentToolsSetting({ enabled, onChange }: { enabled: boolean; onChange?: (enabled: boolean) => Promise<unknown> }) {
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  return (
    <SettingsCard title="Agent tools" ariaLabel="Native agent tools">
      <ToggleRow
        label="Native agent tools"
        description="Use the coding agent's native tools."
        checked={enabled}
        disabled={!onChange || saving}
        disabledTitle={onChange ? 'Saving…' : 'Only the owner can change this.'}
        onCheckedChange={async checked => {
          if (!onChange) return
          setSaving(true)
          setError('')
          try {
            await onChange(checked)
          } catch (err) {
            setError(err instanceof Error ? err.message : 'Could not save this setting.')
          } finally {
            setSaving(false)
          }
        }}
      />
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
    </SettingsCard>
  )
}

