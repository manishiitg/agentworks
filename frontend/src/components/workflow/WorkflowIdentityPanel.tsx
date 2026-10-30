import { useEffect, useRef, useState } from 'react'
import { Loader2, Tag, Target, Trash2 } from 'lucide-react'
import { Button } from '../ui/Button'
import ConfirmationDialog from '../ui/ConfirmationDialog'
import { SettingsCard } from '../ui/SettingsCard'
import { Input } from '../ui/Input'
import { Label } from '../ui/label'
import { READ_ONLY_TITLE, useCanWriteWorkflow } from '../../hooks/useCanWriteWorkflow'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useChatStore } from '../../stores/useChatStore'
import { ChatInstructionsCard } from './ChatInstructionsCard'
import { SoulViewer } from './SoulViewer'
import { StatusBanner } from './bots/StatusBanner'
import { IconUploadField } from '../ui/IconUploadField'
import { RelayDurabilitySetting } from './RelayDurabilitySetting'

// The Identity view's General tab: workflow name, icon, purpose (soul),
// and deletion. Joins the pane scroll; never owns a scroll container.
export default function WorkflowIdentityPanel({ workspacePath, relayMode = false }: { workspacePath: string | null; relayMode?: boolean }) {
  const canWriteWorkflow = useCanWriteWorkflow(workspacePath)
  const workflow = useWorkflowManifestStore(state =>
    workspacePath ? state.workflows.find(entry => entry.workspace_path === workspacePath) : undefined,
  )
  const [labelDraft, setLabelDraft] = useState('')
  const [iconDraft, setIconDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const currentWorkspace = useRef(workspacePath)
  currentWorkspace.current = workspacePath

  useEffect(() => {
    setLabelDraft(workflow?.manifest.label ?? '')
    setIconDraft(workflow?.manifest.icon ?? '')
    setError(null)
  }, [workflow?.manifest.label, workflow?.manifest.icon, workspacePath])

  if (!workspacePath) {
    return <p className="text-xs text-muted-foreground">Select a workflow to manage its identity.</p>
  }

  const labelDirty = labelDraft.trim() !== (workflow?.manifest.label ?? '')
  const iconDirty = !relayMode && iconDraft.trim() !== (workflow?.manifest.icon ?? '')
  const dirty = (labelDirty || iconDirty) && labelDraft.trim().length > 0

  const save = async () => {
    if (!dirty || !canWriteWorkflow) return
    setSaving(true)
    setError(null)
    try {
      await useWorkflowManifestStore.getState().updateWorkflow(workspacePath, {
        ...(labelDirty ? { label: labelDraft.trim() } : {}),
        ...(iconDirty ? { icon: iconDraft.trim() } : {}),
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Unable to save.')
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    if (!canWriteWorkflow || deleting) return
    setDeleting(true)
    // Confirmation is complete. Keep navigation usable while the server
    // finishes deletion; only this workflow's Delete button stays disabled.
    setConfirmingDelete(false)
    setError(null)
    try {
      await useGlobalPresetStore.getState().deleteWorkflow(workspacePath)
      if (currentWorkspace.current === workspacePath && !useGlobalPresetStore.getState().activePresetIds.workflow) {
        useWorkflowStore.getState().setShowWorkspacePane(false)
      }
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : `Failed to delete ${relayMode ? 'Flow' : 'automation'}. Please try again.`
      useChatStore.getState().addToast(message, 'error')
      if (currentWorkspace.current === workspacePath) setError(message)
    } finally {
      setDeleting(false)
      setConfirmingDelete(false)
    }
  }

  return (
    <div className="space-y-4">
      {error && <StatusBanner tone="error">{error}</StatusBanner>}
      <SettingsCard
        icon={<Tag aria-hidden="true" className="h-4 w-4 text-primary" />}
        title={relayMode ? 'Name' : 'Name and icon'}
        description={relayMode ? 'How this Flow appears in the app.' : 'How this workflow appears across AgentWorks.'}
      >
        <div>
          <Label className="mb-2 block">{relayMode ? 'Flow name' : 'Workflow name'}</Label>
          <Input
            value={labelDraft}
            onChange={event => setLabelDraft(event.target.value)}
            disabled={!canWriteWorkflow || saving}
            title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined}
            placeholder="e.g. Support helper"
          />
        </div>
        {!relayMode && <div title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined}>
          <Label className="mb-2 block">Icon</Label>
          <IconUploadField
            value={iconDraft}
            onChange={setIconDraft}
            label={labelDraft.trim() || 'Automation'}
            disabled={!canWriteWorkflow || saving}
            inputAriaLabel="Workflow icon"
          />
        </div>}
        <div className="flex justify-end">
          <Button onClick={() => void save()} disabled={!canWriteWorkflow || !dirty || saving} title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined}>
            {saving ? <><Loader2 className="h-4 w-4 animate-spin" />Saving…</> : 'Save'}
          </Button>
        </div>
      </SettingsCard>

      {relayMode && workflow?.manifest.relay_runtime === 'python' && <RelayDurabilitySetting workspacePath={workspacePath} />}
      {!relayMode && <SettingsCard
        icon={<Target aria-hidden="true" className="h-4 w-4 text-primary" />}
        title="Purpose"
        description="What this workflow is for — its Objective and Success Criteria."
      >
        <SoulViewer workspacePath={workspacePath} embedded />
      </SettingsCard>}

      <ChatInstructionsCard workspacePath={workspacePath} />

      <SettingsCard
        icon={<Trash2 aria-hidden="true" className="h-4 w-4 text-primary" />}
        title={relayMode ? 'Delete Flow' : 'Delete workflow'}
        description={relayMode ? 'Removes this Flow and everything in it. This cannot be undone.' : 'Removes the workflow folder and everything in it. This cannot be undone.'}
      >
        <div>
          <Button
            variant="destructive"
            onClick={() => setConfirmingDelete(true)}
            disabled={!canWriteWorkflow || deleting}
            title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined}
          >
            {deleting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}
            {deleting ? 'Deleting…' : `Delete ${relayMode ? 'Flow' : 'workflow'}`}
          </Button>
        </div>
      </SettingsCard>

      <ConfirmationDialog
        isOpen={confirmingDelete}
        onClose={() => { if (!deleting) setConfirmingDelete(false) }}
        onConfirm={() => void remove()}
        title={`Delete ${workflow?.manifest.label || (relayMode ? 'Flow' : 'workflow')}?`}
        message={relayMode ? 'This removes the Flow and everything in it. This cannot be undone.' : 'This removes the workflow folder and everything in it. This cannot be undone.'}
        confirmText={`Delete ${relayMode ? 'Flow' : 'workflow'}`}
        type="danger"
        isLoading={deleting}
        loadingText="Deleting…"
        requireText={workflow?.manifest.label || undefined}
      />
    </div>
  )
}
