import { useEffect, useState } from 'react'
import { usePersistentTab } from '../../hooks/usePersistentTab'
import { Fingerprint, FolderOpen, Loader2, Lock, Tag, Target, Trash2 } from 'lucide-react'
import { FolderGrantList } from '../../components/folders/FolderGrantList'
import { WorkflowReferenceAccess } from '../../components/folders/WorkflowReferenceAccess'
import { AskAIButton } from '../../components/workflow/AskAIButton'
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
import type { ProductIdentity } from '../../platform/chat/productProjects'
import type { ProductIdentityPatch } from '../../platform/chat/productProjects'
import { workFolderApi } from '../../services/api'
import type { PresetLLMConfig, WorkFolderGrant } from '../../services/api-types'
import { loadWorkSessions } from './workSessions'
import { useProjectProduct } from './projectProduct'
import { isWorkIdentityTabEnabled } from './workViewGating'
import { WorkModelsPanel } from './WorkModelsPanel'
import type { CrewTemplateId } from './crewTemplates'
import { CrewTemplatePicker } from './CrewTemplatePicker'
import type { WorkRuntimeSelection } from './workTabs'

export type WorkIdentityTab = 'general' | 'models'

const IDENTITY_TABS: Array<{ value: WorkIdentityTab; label: string }> = [
  { value: 'general', label: 'General' },
  { value: 'models', label: 'Models' },
]

function identityTabAskAIMessage(noun: string, hasIdentity: boolean): Record<WorkIdentityTab, string> {
  return {
    general: hasIdentity
      ? `Help me with this ${noun} project's name, icon, and purpose. Explain what's set and ask what I want to change.`
      : `Help me with this ${noun} project's name. Explain what's set and ask what I want to change.`,
    models: 'Help me choose between the coding agents available for this project. Explain the practical differences before changing anything.',
  }
}

// Code has a name only: rename and delete, no identity, purpose or templates.
function CodeGeneralPanel({ projectTitle, projectIdentity, onUpdateIdentity, onDeleteRequest }: {
  projectTitle: string
  projectIdentity?: ProductIdentity
  onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>
  onDeleteRequest: () => void
}) {
  const current = projectIdentity?.name ?? ''
  const [nameDraft, setNameDraft] = useState(current)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { setNameDraft(current); setError(null) }, [current])
  const dirty = nameDraft.trim() !== current
  const save = async () => {
    if (!dirty || saving) return
    setSaving(true)
    setError(null)
    try {
      await onUpdateIdentity({ name: nameDraft.trim() })
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
        title="Name"
      >
        <div>
          <Label className="mb-2 block">Workspace name</Label>
          <Input value={nameDraft} onChange={event => setNameDraft(event.target.value)} disabled={saving} placeholder={projectTitle} maxLength={60} />
        </div>
        <div className="flex justify-end">
          <Button onClick={() => void save()} disabled={!dirty || saving}>
            {saving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving…</> : 'Save'}
          </Button>
        </div>
      </SettingsCard>
      <SettingsCard
        icon={<Lock aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Access"
        description="Private to you. Files, chats, credentials and runtime cannot be shared with other users. Admins and Code reviewers on this server can view it, read-only, and every view is logged."
      >
        {null}
      </SettingsCard>
      <SettingsCard
        icon={<Trash2 aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Delete workspace"
        description="Removes the workspace folder and everything in it. This cannot be undone."
      >
        <div>
          <Button variant="destructive" onClick={onDeleteRequest} disabled={saving}>
            <Trash2 className="h-4 w-4" /> Delete workspace
          </Button>
        </div>
      </SettingsCard>
    </div>
  )
}

function WorkGeneralPanel({ projectTitle, projectPurpose, projectIdentity, projectTemplates, onInstallTemplate, onUpdateIdentity, onDeleteRequest }: {
  projectTitle: string
  projectPurpose: string
  projectIdentity?: ProductIdentity
  projectTemplates: Array<{ id: string; version: number }>
  onInstallTemplate: (id: CrewTemplateId) => Promise<void>
  onUpdateIdentity: (patch: ProductIdentityPatch) => Promise<unknown>
  onDeleteRequest: () => void
}) {
  const [nameDraft, setNameDraft] = useState('')
  const [iconDraft, setIconDraft] = useState('')
  const [roleDraft, setRoleDraft] = useState('')
  const [purposeDraft, setPurposeDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setNameDraft(projectIdentity?.name ?? '')
    setIconDraft(projectIdentity?.icon ?? '')
    setRoleDraft(projectIdentity?.role ?? '')
    setPurposeDraft(projectPurpose)
    setError(null)
  }, [projectIdentity?.name, projectIdentity?.icon, projectIdentity?.role, projectPurpose])

  const displayName = nameDraft.trim() || projectTitle
  const dirty = nameDraft.trim() !== (projectIdentity?.name ?? '')
    || iconDraft.trim() !== (projectIdentity?.icon ?? '')
    || roleDraft.trim() !== (projectIdentity?.role ?? '')
    || purposeDraft.trim() !== projectPurpose

  const save = async () => {
    if (!dirty || saving) return
    if (!roleDraft.trim() || !purposeDraft.trim()) {
      setError('Role and purpose are both required.')
      return
    }
    setSaving(true)
    setError(null)
    try {
      await onUpdateIdentity({
        name: nameDraft.trim(),
        icon: iconDraft.trim(),
        role: roleDraft.trim(),
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
      <CrewTemplatePicker projectTemplates={projectTemplates} onInstallTemplate={onInstallTemplate} onError={setError} />
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
        title="Purpose"
      >
        <div>
          <Label className="mb-2 block">Role (required)</Label>
          <Input
            value={roleDraft}
            onChange={event => setRoleDraft(event.target.value)}
            disabled={saving}
            placeholder="e.g. Launch partner"
          />
        </div>
        <div>
          <Label className="mb-2 block">Purpose (required)</Label>
          <Textarea
            value={purposeDraft}
            onChange={event => setPurposeDraft(event.target.value)}
            disabled={saving}
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

function WorkFoldersBody({ workspacePath, workflowContextPaths, onWorkflowContextPathsChange }: {
  workspacePath: string
  workflowContextPaths: string[]
  onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
}) {
  const [folders, setFolders] = useState<WorkFolderGrant[]>([])
  const [roots, setRoots] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [crewReferences, setCrewReferences] = useState<Array<{ path: string; label: string; icon?: string }>>([])

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

/** Folders on this server and other work this Crew or Code project can use (Integrations, "Folders & workflows"). */
export function WorkFoldersSection({ workspacePath, workflowContextPaths, onWorkflowContextPathsChange, onAsk, askMessage }: {
  workspacePath: string
  workflowContextPaths: string[]
  onWorkflowContextPathsChange: (paths: string[]) => Promise<unknown>
  onAsk: (message: string) => Promise<void>
  askMessage: string
}) {
  return (
    <div className="space-y-4">
      <div className="flex shrink-0 flex-wrap items-center gap-3 rounded-lg border border-border bg-muted/40 p-3">
            <div className="min-w-0 flex-1 basis-48">
              <p className="text-sm font-semibold text-foreground">Need to attach something?</p>
              <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
                Ask the agent to attach folders or link other work for context — no manual setup needed.
              </p>
            </div>
            <AskAIButton
              workspacePath={workspacePath}
              onAsk={onAsk}
              label="Ask AI to add"
              message={askMessage}
              className="inline-flex shrink-0 items-center justify-center gap-2 rounded-lg border border-border px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
            />
          </div>
          <WorkFoldersBody
            workspacePath={workspacePath}
            workflowContextPaths={workflowContextPaths}
            onWorkflowContextPathsChange={onWorkflowContextPathsChange}
          />
    </div>
  )
}

export function WorkIdentityPanel({ workspacePath, projectTitle, projectDescription, projectIdentity, projectTemplates, onInstallTemplate, tabId, selectedSecrets, selectedGlobalSecrets, projectLLMConfig, enabledPanels, onAsk, onRuntimeChange, nativeAgentTools, onNativeAgentToolsChange, onSelectedSecretsChange, onSelectedGlobalSecretsChange, onUpdateIdentity, onDeleteRequest }: {
  workspacePath: string
  /** The project's id. */
  projectId?: string
  projectTitle: string
  projectDescription: string
  projectIdentity?: ProductIdentity
  projectTemplates: Array<{ id: string; version: number }>
  onInstallTemplate: (id: CrewTemplateId) => Promise<void>
  tabId: string
  selectedSecrets: string[]
  selectedGlobalSecrets: string[]
  projectLLMConfig?: PresetLLMConfig
  enabledPanels?: Set<string>
  onAsk: (message: string) => Promise<void>
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
  const askMessages = identityTabAskAIMessage(product.noun, product.hasIdentity)
  const visibleTabs = IDENTITY_TABS.filter(option => isWorkIdentityTabEnabled(option.value, enabledPanels))
  const [tab, setTab] = usePersistentTab<WorkIdentityTab>('agentworks.tab.crew-identity', 'general', IDENTITY_TABS.map(option => option.value))
  const activeTab = visibleTabs.some(option => option.value === tab) ? tab : visibleTabs[0].value
  // Every tab loads on mount, so Refresh always remounts.
  const [tabNonce, setTabNonce] = useState(0)

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={Fingerprint}
        title={product.hasIdentity ? 'Identity' : 'Setup'}
        helpTopic={`Identity · ${visibleTabs.find(option => option.value === activeTab)?.label ?? 'General'}`}
        actions={(
          <WorkspaceViewActions
            workspacePath={workspacePath}
            message={askMessages[activeTab]}
            onAsk={onAsk}
            onRefresh={() => setTabNonce(nonce => nonce + 1)}
            refreshLabel={`Refresh ${visibleTabs.find(option => option.value === activeTab)?.label ?? 'view'}`}
          />
        )}
        tabs={{ value: activeTab, onChange: (value: string) => setTab(value as WorkIdentityTab), options: visibleTabs, ariaLabel: 'Identity' }}
      />
      <div key={`${activeTab}:${tabNonce}`} className="min-h-0 flex-1 overflow-y-auto p-4">
        {activeTab === 'general' && !product.hasIdentity && <CodeGeneralPanel
          projectTitle={projectTitle}
          projectIdentity={projectIdentity}
          onUpdateIdentity={onUpdateIdentity}
          onDeleteRequest={onDeleteRequest}
        />}
        {activeTab === 'general' && product.hasIdentity && <WorkGeneralPanel
          projectTitle={projectTitle}
          projectPurpose={projectDescription}
          projectIdentity={projectIdentity}
          projectTemplates={projectTemplates}
          onInstallTemplate={onInstallTemplate}
          onUpdateIdentity={onUpdateIdentity}
          onDeleteRequest={onDeleteRequest}
        />}
        {activeTab === 'models' && <div className="space-y-4">
          <WorkModelsPanel
            tabId={tabId}
            workspacePath={workspacePath}
            projectLLMConfig={projectLLMConfig}
            onRuntimeChange={onRuntimeChange}
            hideHeader
          />
          {product.hasNativeAgentToolsSetting && <NativeAgentToolsSetting enabled={!!nativeAgentTools} onChange={onNativeAgentToolsChange} />}
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
