import { useCallback, useEffect, useState } from 'react'
import { workflowManifestApi, type WorkflowGoalSetupCheck, type WorkflowGoalSetupStatus } from '../../services/api'
import { useChatStore } from '../../stores/useChatStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useWorkflowManifestStore } from '../../stores/useWorkflowManifestStore'
import { goalSetupChatMessage } from '../../utils/goalSetupChat'
import { GoalSetupCard } from './GoalSetupCard'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'

const REFRESH_MS = 30_000

// Initial setup for an automation, like a Crew template's setup bar: goal,
// then plan, metrics and dashboard, each done in the Builder chat. Goals are
// optional, so it can be dismissed; the server stops showing it once the
// automation has run. GoalSetupCard draws it.
export function WorkflowGoalSetupBar({ workspacePath, canEdit }: { workspacePath: string | null | undefined; canEdit: boolean }) {
  const [status, setStatus] = useState<WorkflowGoalSetupStatus | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)

  const refresh = useCallback(async () => {
    if (!workspacePath) {
      setStatus(null)
      return
    }
    setLoading(true)
    try {
      setStatus(await workflowManifestApi.getGoalSetup(workspacePath))
    } catch {
      setStatus(null)
    } finally {
      setLoading(false)
    }
  }, [workspacePath])

  useEffect(() => {
    setStatus(null)
    void refresh()
  }, [refresh])

  useEffect(() => {
    if (!status?.show) return
    const timer = window.setInterval(() => { void refresh() }, REFRESH_MS)
    return () => window.clearInterval(timer)
  }, [refresh, status?.show])

  if (!canEdit || !workspacePath || !status?.show) return null
  const playbooks = status.playbooks ?? []

  const startStep = async (step: WorkflowGoalSetupCheck | undefined) => {
    if (!step || busy) return
    setBusy(true)
    try {
      if (step.id === 'pulse') {
        // Pulse is one click, not a chat: the same update the Pulse tab makes.
        await useWorkflowManifestStore.getState().updateWorkflow(workspacePath, { pulse_enabled: true })
        useChatStore.getState().addToast('Pulse turned on', 'success')
        await refresh()
        return
      }
      await sendWorkspacePaneMessageToChat({ workspacePath, message: goalSetupChatMessage(step.command || 'setup-goals', playbooks) })
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : step.id === 'pulse' ? 'Could not turn on Pulse.' : 'Could not start goal setup in chat.', 'error')
    } finally {
      setBusy(false)
    }
  }

  const dismiss = async () => {
    if (busy) return
    setBusy(true)
    try {
      setStatus(await workflowManifestApi.dismissGoalSetup(workspacePath))
    } catch (cause) {
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not dismiss goal setup.', 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <GoalSetupCard
      status={status}
      busy={busy}
      loading={loading}
      onStart={step => { void startStep(step) }}
      onOpenPlaybooks={() => useWorkflowStore.getState().openWorkspaceView('playbooks')}
      onDismiss={() => { void dismiss() }}
      onRefresh={() => { void refresh() }}
    />
  )
}
