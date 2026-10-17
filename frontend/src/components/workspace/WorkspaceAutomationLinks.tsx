import { useEffect, useState } from 'react'
import { Braces, Webhook, Zap } from 'lucide-react'
import { productWebhooksApi } from '../../api/productWebhooks'
import { workflowWebhooksApi } from '../../api/workflowWebhooks'
import { conditionTriggersApi } from '../../api/conditionTriggers'
import { crewFunctionsApi } from '../../api/crewFunctions'
import { useLiveRefetch } from '../../hooks/useLiveRefetch'
import { WorkspaceContextRow, WorkspaceContextSection } from './WorkspaceContextCard'

type Section = 'triggers' | 'conditions' | 'functions'
/** Summaries of the existing automation catalogs; no trigger creation or execution. */
export function WorkspaceAutomationLinks({
  workspacePath,
  profileId,
  projectId,
  onOpen,
  triggersEnabled = true,
  scheduleCount,
}: {
  workspacePath: string
  profileId: 'work' | 'code' | 'agentworks'
  projectId: string
  onOpen: (section: Section | 'chats') => void
  triggersEnabled?: boolean
  scheduleCount?: number | null
}) {
  const key = `${profileId}:${projectId}:${workspacePath}`
  const [tick, setTick] = useState(0)
  const [snapshot, setSnapshot] = useState<{ key: string; counts: Record<Section, number | null> } | null>(
    null
  )
  useLiveRefetch(() => setTick((value) => value + 1), {
    kinds: ['schedules'],
    workflow: workspacePath,
    fallbackMs: 30000,
  })
  useEffect(() => {
    let disposed = false
    if (!projectId || !triggersEnabled) return
    const scope = { profileId, projectId }
    const workflow = profileId === 'agentworks' ? workflowWebhooksApi.list(workspacePath) : null
    const webhooks = workflow
      ? workflow.then((result) => result.triggers.filter((item) => !item.kind).length)
      : productWebhooksApi
          .list(scope)
          .then(
            (result) =>
              result.triggers.filter((item) => item.kind !== 'internal' && item.kind !== 'gmail').length
          )
    const functions = workflow
      ? workflow.then(
          (result) => result.triggers.filter((item) => item.kind === 'function' && item.function).length
        )
      : crewFunctionsApi
          .list(scope)
          .then((result) => result.functions.filter((item) => !item.implicit).length)
    void Promise.allSettled([
      webhooks,
      conditionTriggersApi.list(projectId, profileId).then((result) => result.triggers.length),
      functions,
    ]).then((results) => {
      if (!disposed)
        setSnapshot({
          key,
          counts: Object.fromEntries(
            ['triggers', 'conditions', 'functions'].map((id, index) => [
              id,
              results[index].status === 'fulfilled' ? results[index].value : null,
            ])
          ) as Record<Section, number | null>,
        })
    })
    return () => {
      disposed = true
    }
  }, [key, profileId, projectId, workspacePath, tick, triggersEnabled])
  const data = snapshot?.key === key ? snapshot.counts : null
  const rows = [
    { id: 'triggers', label: 'API triggers', hint: 'Run when another app sends data (webhooks)', icon: Webhook },
    { id: 'conditions', label: 'Condition triggers', hint: 'Run when saved conditions match (watchers)', icon: Zap },
  ] as const
  if (!triggersEnabled)
    return scheduleCount === 0 ? (
      <p className="py-1 text-xs text-muted-foreground">No schedules yet.</p>
    ) : null
  if (!projectId) return null
  return (
    <>
      {!data && <p className="py-1 text-xs text-muted-foreground">Loading triggers…</p>}
      {data && scheduleCount === 0 && data.triggers === 0 && data.conditions === 0 && (
        <p className="py-1 text-xs text-muted-foreground">No schedules or triggers yet.</p>
      )}
      {data &&
        rows
          .filter((item) => data[item.id] == null || data[item.id]! > 0)
          .map((item) => (
            <WorkspaceContextRow
              key={item.id}
              icon={item.icon}
              hint={item.hint}
              detail={data[item.id] == null ? 'Unavailable' : String(data[item.id])}
              onClick={() => onOpen(item.id)}
            >
              {item.label}
            </WorkspaceContextRow>
          ))}
      {data && (data.functions == null || data.functions > 0) && (
        <div className="mt-3 border-t border-border pt-3">
          <WorkspaceContextSection title="Functions">
            <WorkspaceContextRow
              icon={Braces}
              detail={data.functions == null ? 'Unavailable' : String(data.functions)}
              onClick={() => onOpen('functions')}
            >
              Callable functions
            </WorkspaceContextRow>
          </WorkspaceContextSection>
        </div>
      )}
    </>
  )
}
