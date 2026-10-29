import { lazy, Suspense, useEffect, useState } from 'react'
import { useWorkspaceViewTarget } from '../../hooks/useWorkspaceViewTarget'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { Fingerprint, FolderOpen, Loader2, Lock, Settings, Tag, Target, Trash2 } from 'lucide-react'
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
import { workFolderApi } from '../../services/api'
import type { PresetLLMConfig, WorkFolderGrant } from '../../services/api-types'
import { loadWorkSessions } from './workSessions'
import { useProjectProduct } from './projectProduct'
import { isWorkIdentityTabEnabled } from './workViewGating'
import { WorkModelsPanel } from './WorkModelsPanel'
import { IdleCompactionSetting } from '../../components/workflow/IdleCompactionSetting'
import { ChatInstructionsCard } from '../../components/workflow/ChatInstructionsCard'
import { ProjectInstructionsCard } from './ProjectInstructionsCard'
import type { WorkRuntimeSelection } from './workTabs'
import { useChatStore } from '../../stores/useChatStore'

import { useProjectMode } from './codeLocalFiles'
import { CODE_MODE_CHOICES } from './CreateCodeWorkspaceDialog'

const CodeLocalFilesSettings = lazy(() => import('./CodeFilesPanel').then(module => ({ default: module.CodeLocalFilesSettings })))

export type WorkIdentityTab = 'general' | 'models' | 'location'

const IDENTITY_TABS: Array<{ value: WorkIdentityTab; label: string }> = [
  { value: 'general', label: 'General' },
  { value: 'models', label: 'Models' },
  // Code only: where this chat's files and commands live (the server, or your own computer through the CLI).
  { value: 'location', label: 'Laptop access' },
]

function identityTabAskAIMessage(noun: string, hasIdentity: boolean): Record<WorkIdentityTab, string> {
  return {
    general: hasIdentity
      ? `Help me with this ${noun} project's name, icon, and purpose. Explain what's set and ask what I want to change.`
      : `Help me with this ${noun} project's name. Explain what's set and ask what I want to change.`,
    models: 'Help me choose between the coding agents available for this project. Explain the practical differences before changing anything.',
    location: 'Explain how this server Crew can also run commands on my connected computer and copy its files into the Crew.',
  }
}

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

export function WorkFoldersSection({ workspacePath, workflowContextPaths, onWorkflowContextPathsChange }: {
  workspacePath: string
  workflowContextPaths: string[]
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
      if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load attached folders.')
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
      setError(cause instanceof Error ? cause.message : 'Could not load attached folders.')
    } finally {
      setLoading(false)
    }
  }

  const remove = async (folder: WorkFolderGrant) => {
    setError('')
    try {
      await workFolderApi.deleteWorkFolder(folder.id)
      await refresh()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not remove folder.')
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
        hideAdd
      />
      <SettingsCard
        icon={<FolderOpen aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Attached folders"
        count={`${folders.length} attached`}
      >
        {loading ? <p className="text-xs text-muted-foreground">Loading…</p> : <FolderGrantList grants={folders} onRemove={remove} />}
        {error && <p className="text-xs text-destructive">{error}</p>}
      </SettingsCard>
    </div>
  )
}

export function WorkIdentityPanel({ workspacePath, projectId, shared, projectTitle, projectDescription, projectIdentity, tabId, openLocalFilesSettings, onLocalFilesSettingsOpened, selectedSecrets, selectedGlobalSecrets, projectLLMConfig, enabledPanels, onAsk, onRuntimeChange, nativeAgentTools, onNativeAgentToolsChange, idleCompaction = true, onIdleCompactionChange, onSelectedSecretsChange, onSelectedGlobalSecretsChange, onUpdateIdentity, onDeleteRequest, modelsOnly = false }: {
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
  onAsk: (message: string) => Promise<void>
  onRuntimeChange: (selection: WorkRuntimeSelection) => void | Promise<void>
  nativeAgentTools?: boolean
  idleCompaction?: boolean
  onIdleCompactionChange?: (enabled: boolean) => Promise<unknown>
  /** Omitted for readers: only the Crew owner can change it. */
  onNativeAgentToolsChange?: (enabled: boolean) => Promise<unknown>
  onSelectedSecretsChange: (secrets: string[]) => Promise<unknown>
  onSelectedGlobalSecretsChange: (secrets: string[]) => Promise<unknown>
  onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>
  onDeleteRequest: () => void
}) {
  const product = useProjectProduct()
  const sessionId = useChatStore(state => state.chatTabs[tabId]?.sessionId || '')
  const askMessages = identityTabAskAIMessage(product.noun, product.hasIdentity)
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
            workspacePath={workspacePath}
            message={askMessages[activeTab]}
            onAsk={onAsk}
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
          <IdleCompactionSetting enabled={idleCompaction} onChange={onIdleCompactionChange} disabledTitle={shared ? 'Only the owner can change this.' : 'Choose a model first, then change this setting.'} />
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

