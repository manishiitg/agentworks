import { useCallback, useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { productWebhooksApi, type ProductAPITrigger, type ProductTriggerScope } from '../../api/productWebhooks'
import type { ScheduledJob, ScheduledJobRun } from '../../services/api-types'
import { useResumePreviousChat } from '../../hooks/useResumePreviousChat'
import { PreviousChatHistoryPanel } from '../PreviousChatHistoryPanel'
import { ScheduleRunCard } from '../ScheduleRunCard'

function productTriggerJob(trigger: ProductAPITrigger): ScheduledJob {
  return {
    id: trigger.id,
    name: trigger.name,
    description: trigger.message,
    entity_type: 'product',
    messages: [trigger.message],
    schedule_type: 'webhook',
    cron_expression: '',
    timezone: 'UTC',
    enabled: trigger.enabled,
    run_count: 0,
    consecutive_failures: 0,
    run_destination: trigger.run_destination,
  }
}

function ProductTriggerDeliveryHistory({ scope }: { scope: ProductTriggerScope }) {
  const [items, setItems] = useState<Array<{ trigger: ProductAPITrigger; run: ScheduledJobRun }>>([])
  const [loading, setLoading] = useState(true)
  const [failed, setFailed] = useState(false)
  const openChat = useResumePreviousChat()
  const refresh = useCallback(async () => {
    try {
      const triggers = (await productWebhooksApi.list(scope)).triggers
      const histories = await Promise.all(triggers.map(async trigger => ({
        trigger,
        runs: (await productWebhooksApi.runs(scope, trigger.id, 30)).runs || [],
      })))
      setItems(histories.flatMap(({ trigger, runs }) => runs.map(run => ({ trigger, run })))
        .sort((a, b) => Date.parse(b.run.started_at || '') - Date.parse(a.run.started_at || '')))
      setFailed(false)
    } catch {
      setFailed(true)
    } finally {
      setLoading(false)
    }
  }, [scope])
  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => { if (!document.hidden) void refresh() }, 10000)
    return () => window.clearInterval(timer)
  }, [refresh])

  return <section className="min-w-0 border-b border-border bg-background">
    <div className="flex items-center justify-between border-b border-border px-3 py-2">
      <h3 className="text-sm font-medium text-foreground">Delivery history</h3>
      <span className="text-xs text-muted-foreground">{loading ? 'Loading…' : items.length}</span>
    </div>
    {loading ? <div className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading deliveries…</div>
      : failed ? <p className="px-3 py-4 text-xs text-destructive">Could not load delivery history.</p>
        : items.length === 0 ? <p className="px-3 py-4 text-xs text-muted-foreground">No webhook deliveries recorded yet.</p>
          : <div className="divide-y divide-border">{items.map(({ trigger, run }) => {
            const job = productTriggerJob(trigger)
            return <div key={run.id} className="px-3 py-3 transition-colors hover:bg-muted/20">
              <ScheduleRunCard
                job={job}
                run={run}
                deletingRunIds={new Set()}
                showScheduleName
                openLabel="View chat"
                loadWebhookPayload={(_, selectedRun) => productWebhooksApi.getPayload(scope, trigger.id, selectedRun.id)}
                onOpen={selectedRun => selectedRun.session_id && void openChat({
                  session_id: selectedRun.session_id,
                  title: trigger.name,
                  query: trigger.message,
                  workshop_mode: 'run',
                  created_at: selectedRun.started_at,
                  updated_at: selectedRun.completed_at || selectedRun.started_at,
                })}
              />
            </div>
          })}</div>}
  </section>
}

export function TriggerDeliveryHistoryPanel({
  workspacePath,
  entityType,
  productTriggerScope,
}: {
  workspacePath: string
  entityType: 'workflow' | 'product'
  productTriggerScope?: ProductTriggerScope
}) {
  const openChat = useResumePreviousChat()
  if (entityType === 'product' && productTriggerScope) {
    return <ProductTriggerDeliveryHistory scope={productTriggerScope} />
  }
  return (
    <PreviousChatHistoryPanel
      workspacePath={workspacePath}
      title="Delivery history"
      emptyText="No webhook deliveries recorded yet."
      runOnly="webhook"
      runEntityType={entityType}
      readOnly
      allowOpen
      actionLabel="View chat"
      showAll
      onSelectSession={session => openChat(session)}
    />
  )
}
