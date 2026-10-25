import { useCallback, useEffect, useState } from 'react'
import { agentApi } from '../../services/api'
import type { GoalMeasurements } from '../../services/api-types'
import { sendWorkspacePaneMessageToChat } from '../../utils/workspacePaneChat'
import { useChatStore } from '../../stores/useChatStore'
import { GoalProgress } from './GoalProgress'
import { SoulViewer } from './SoulViewer'

export default function GoalOverview({ workspacePath }: { workspacePath: string }) {
  const [measurements, setMeasurements] = useState<GoalMeasurements | null>(null)
  const [error, setError] = useState<string | null>(null)
  const refresh = useCallback(async (signal: AbortSignal) => {
    try {
      const result = await agentApi.getGoalMetrics(workspacePath, signal)
      if (!signal.aborted) { setMeasurements(result); setError(null) }
    } catch (cause) {
      if (!signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load measurements')
    }
  }, [workspacePath])
  useEffect(() => {
    setMeasurements(null)
    const controller = new AbortController()
    void refresh(controller.signal)
    return () => controller.abort()
  }, [refresh])
  const askBuilder = (message: string) => {
    void sendWorkspacePaneMessageToChat({ workspacePath, message }).catch(cause =>
      useChatStore.getState().addToast(cause instanceof Error ? cause.message : 'Could not open Builder chat', 'error'))
  }
  return <div className="h-full space-y-5 overflow-y-auto p-4 sm:p-5">
    <SoulViewer workspacePath={workspacePath} embedded goalSummary />
    {error ? <p role="alert" className="text-sm text-destructive">{error}</p>
      : measurements ? <GoalProgress impact={measurements} workspacePath={workspacePath} onAskBuilder={askBuilder} />
        : <p className="text-sm text-muted-foreground">Loading measurements…</p>}
  </div>
}
