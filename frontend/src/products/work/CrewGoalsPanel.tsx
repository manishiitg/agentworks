import { useCallback, useEffect, useState } from 'react'
import { isAxiosError } from 'axios'
import api from '../../services/api'
import type { GoalMeasurements } from '../../services/api-types'
import { GoalOverviewContent } from '../../components/workflow/GoalOverview'
import { GoalSummary } from '../../components/workflow/SoulViewer'

type Goal = { id: string; name: string; outcome: string; status: string; workflow_ids: string[]; metric_ids: string[] }
type GoalData = { goals: Goal[]; measurements: GoalMeasurements; can_edit: boolean; workspace_path: string }

export function CrewGoalsPanel({ projectId, onAsk }: { projectId: string; onAsk: (message: string) => void }) {
  const [data, setData] = useState<GoalData | null>(null)
  const [error, setError] = useState('')
  const refresh = useCallback(async (signal: AbortSignal) => {
    const response = await api.get<GoalData>('/api/crew-goals', { params: { crew_id: projectId }, signal })
    if (!signal.aborted) setData(response.data)
  }, [projectId])
  useEffect(() => {
    const controller = new AbortController()
    setData(null); setError('')
    void refresh(controller.signal).catch(e => { if (!controller.signal.aborted) setError(isAxiosError(e) && typeof e.response?.data === 'string' ? e.response.data : e instanceof Error ? e.message : 'Could not load goals. Try again.') })
    return () => controller.abort()
  }, [refresh])
  const summary = data?.goals.map(goal => `### ${goal.name}\n\n${goal.outcome}`).join('\n\n')
  return <GoalOverviewContent workspacePath={data?.workspace_path ?? ''} measurements={data?.measurements ?? null} error={error} onAsk={onAsk} summary={<>
    {summary && <GoalSummary content={`## Objective\n\n${summary}`} />}
    {data?.goals.length === 0 && <p className="text-sm text-muted-foreground">No goals yet. Describe what you want this Crew to achieve in chat.</p>}
    {data?.can_edit && <button className="text-sm text-primary" onClick={() => onAsk('Help me set or update the goals and tracking for this Crew. Read its existing goals and measurements first.')}>Edit in chat</button>}
  </>} />
}
