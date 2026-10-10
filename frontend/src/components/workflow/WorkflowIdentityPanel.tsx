import { useEffect, useState } from 'react'
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
import { AskAIButton } from './AskAIButton'
import { SoulViewer } from './SoulViewer'
import { StatusBanner } from './bots/StatusBanner'
import { IconUploadField } from '../ui/IconUploadField'
import { RelayDurabilitySetting } from './RelayDurabilitySetting'

const SOUL_EDIT_MESSAGE = 'Review this workflow\'s purpose (soul/soul.md: Objective and Success Criteria) and help me update it. Read the file first, explain what it says in plain words, then ask what I want to change before changing anything.'

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
    if (!canWriteWorkflow) return
    setDeleting(true)
    setError(null)
    try {
      await useWorkflowManifestStore.getState().deleteWorkflow(workspacePath)
      useGlobalPresetStore.getState().clearActivePreset('workflow')
      await Promise.all([
        useGlobalPresetStore.getState().refreshPresets(),
        useWorkflowManifestStore.getState().refreshWorkflows(),
      ])
      useWorkflowStore.getState().setShowWorkspacePane(false)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : `Failed to delete ${relayMode ? 'Relay' : 'automation'}. Please try again.`)
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
        description={relayMode ? 'How this Relay appears in the app.' : 'How this workflow appears across AgentWorks.'}
      >
        <div>
          <Label className="mb-2 block">{relayMode ? 'Relay name' : 'Workflow name'}</Label>
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
        actions={(
          <AskAIButton
            workspacePath={canWriteWorkflow ? workspacePath : null}
            label="Ask AI to edit"
            message={SOUL_EDIT_MESSAGE}
          />
        )}
      >
        <SoulViewer workspacePath={workspacePath} embedded />
      </SettingsCard>}

      <SettingsCard
        icon={<Trash2 aria-hidden="true" className="h-4 w-4 text-primary" />}
        title={relayMode ? 'Delete Relay' : 'Delete workflow'}
        description={relayMode ? 'Removes this Relay and everything in it. This cannot be undone.' : 'Removes the workflow folder and everything in it. This cannot be undone.'}
      >
        <div>
          <Button
            variant="destructive"
            onClick={() => setConfirmingDelete(true)}
            disabled={!canWriteWorkflow || deleting}
            title={!canWriteWorkflow ? READ_ONLY_TITLE : undefined}
          >
            <Trash2 className="h-4 w-4" /> Delete {relayMode ? 'Relay' : 'workflow'}
          </Button>
        </div>
      </SettingsCard>

      <ConfirmationDialog
        isOpen={confirmingDelete}
        onClose={() => { if (!deleting) setConfirmingDelete(false) }}
        onConfirm={() => void remove()}
        title={`Delete ${workflow?.manifest.label || (relayMode ? 'Relay' : 'workflow')}?`}
        message={relayMode ? 'This removes the Relay and everything in it. This cannot be undone.' : 'This removes the workflow folder and everything in it. This cannot be undone.'}
        confirmText={`Delete ${relayMode ? 'Relay' : 'workflow'}`}
        type="danger"
        isLoading={deleting}
        loadingText="Deleting…"
        requireText={workflow?.manifest.label || undefined}
      />
    </div>
  )
}
