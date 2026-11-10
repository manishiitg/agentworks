import { useCallback, useEffect, useState, type ReactNode } from 'react'
import axios from 'axios'
import { Copy, Webhook } from 'lucide-react'
import { apiTriggerURL, productWebhooksApi, type ProductAPITrigger, type ProductTriggerScope, type RunDestinationChoice } from '../../api/productWebhooks'
import { OnlineOnlyNote } from '../../products/work/useMacCrew'
import { AutomationIntro, AutomationListBox, AutomationRow, DetailLine, EmptyAutomations, PageRow, RowButton, relativeTime } from '../automation/AutomationKit'
import { Switch } from '../ui/Switch'
import type { ScheduledJobRun } from '../../services/api-types'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'

const buttonClass = 'rounded-md border border-border px-2 py-1 text-xs hover:bg-muted disabled:opacity-50'

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error) && typeof error.response?.data === 'string') return error.response.data
  return error instanceof Error ? error.message : 'Unable to update project triggers'
}

export default function ProductAPITriggersView({ scope, workspacePath, onViewRuns, deliveryHistory, hideHeader = false, refreshToken = 0, onCounts, onAsk, embedded = false, hideEmpty = false, pageRows = false }: { pageRows?: boolean; embedded?: boolean; hideEmpty?: boolean; scope: ProductTriggerScope; workspacePath?: string; onViewRuns?: () => void; deliveryHistory?: ReactNode; hideHeader?: boolean; refreshToken?: number; onCounts?: (counts: { active: number; paused: number }) => void; onAsk?: (message: string) => void | Promise<void> }) {
  const { profileId, projectId } = scope
  const [triggers, setTriggers] = useState<ProductAPITrigger[]>([])
  const [destinations, setDestinations] = useState<RunDestinationChoice[]>([])
  const [openId, setOpenId] = useState<string | null>(null)
  const [issued, setIssued] = useState<ProductAPITrigger | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState('')
  const [recent, setRecent] = useState<Record<string, ScheduledJobRun[]>>({})
  const [showAllRuns, setShowAllRuns] = useState(false)

  const refresh = useCallback(async () => {
    // Caller bindings (kind=internal) are listed under Functions → Callers;
    // this view is external webhooks only.
    try {
      const listed = await productWebhooksApi.list({ profileId, projectId })
      setTriggers(listed.triggers.filter(trigger => trigger.kind !== 'internal'))
      setDestinations(listed.run_destinations || [])
      if (pageRows) {
        const entries = await Promise.all(listed.triggers.filter(trigger => trigger.kind !== 'internal').map(async trigger => [trigger.id, (await productWebhooksApi.runs({ profileId, projectId }, trigger.id, 30).catch(() => ({ runs: [] }))).runs || []] as const))
        setRecent(Object.fromEntries(entries))
      }
    }
    catch (cause) { setError(errorMessage(cause)) }
  }, [profileId, projectId, pageRows])

  useEffect(() => { setError(''); setIssued(null); void refresh() }, [refresh])

  useEffect(() => {
    if (refreshToken) void refresh()
  }, [refreshToken, refresh])

  const activeTriggers = triggers.filter(trigger => trigger.enabled).length
  useEffect(() => {
    onCounts?.({ active: activeTriggers, paused: triggers.length - activeTriggers })
  }, [onCounts, activeTriggers, triggers.length])

  const save = async (trigger: ProductAPITrigger, rotate = false) => {
    if (busy) return
    setBusy(true); setError(''); setCopied('')
    try {
      const result = await productWebhooksApi.save(scope, trigger, rotate)
      if (result.secret) setIssued(result)
      await refresh()
    } catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }

  const remove = async (id: string) => {
    if (busy) return
    setBusy(true); setError('')
    try { await productWebhooksApi.delete(scope, id); await refresh() }
    catch (cause) { setError(errorMessage(cause)) }
    finally { setBusy(false) }
  }

  const copy = async (text: string, label: string) => {
    try { await navigator.clipboard.writeText(text); setCopied(label) }
    catch { setError('Clipboard access failed. Select and copy the displayed value.') }
  }

  if (pageRows) {
    const where = (trigger: ProductAPITrigger) => trigger.run_destination === 'chat' || !trigger.run_destination ? 'main chat' : trigger.run_destination === 'isolated' ? 'own conversation' : 'new conversation each time'
    const weekAgo = Date.now() - 7 * 86400000
    const lastCall = (id: string) => {
      const runs = recent[id] || []
      if (runs.length === 0) return 'No calls yet'
      const week = runs.filter(run => Date.parse(run.started_at) > weekAgo).length
      return `Last call ${relativeTime(runs[0].started_at)} · ${week} this week`
    }
    const outcome = (run: ScheduledJobRun) => run.status === 'success' ? 'finished' : run.status === 'error' || run.status === 'failed' ? 'failed' : run.status.replaceAll('_', ' ')
    return <div>
      {profileId === 'work' && <div className="px-4 pt-2"><OnlineOnlyNote projectId={scope.projectId} /></div>}
      {error && <p role="alert" className="m-4 rounded-md border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
      {issued?.secret && <section className="m-4 space-y-2 rounded-lg border border-primary/30 bg-primary/5 p-3">
        <h3 className="text-sm font-medium">New secret for {issued.name}</h3>
        <p className="text-xs text-muted-foreground">Copy it now. It is only shown when created or rotated. Anyone holding this address and secret can start work as the owner: keep it private, and rotate it if it leaks.</p>
        <code className="block break-all rounded bg-background p-2 text-xs select-all">{issued.secret}</code>
        <RowButton onClick={() => void copy(issued.secret!, 'Secret copied')}>Copy secret</RowButton>
      </section>}
      {triggers.map(trigger => <PageRow key={trigger.id} testId={`webhook-${trigger.id}`}
        lead={<Switch aria-label={`${trigger.enabled ? 'Turn off' : 'Turn on'} ${trigger.name}`} checked={trigger.enabled} disabled={busy} onCheckedChange={enabled => void save({ ...trigger, enabled })} />}
        title={trigger.name}
        lines={[[trigger.starts, where(trigger)].filter(Boolean).join(' · '), trigger.enabled ? lastCall(trigger.id) : `Off · ${lastCall(trigger.id)}`]}
        action={<RowButton onClick={() => void copy(apiTriggerURL(trigger.path), 'Address copied')}>Copy address</RowButton>}
        open={openId === trigger.id} onToggle={() => setOpenId(openId === trigger.id ? null : trigger.id)}>
        <DetailLine label="When">{trigger.auth_mode === 'github' ? 'GitHub sends a signed request to' : 'A request with the secret key to'} <code className="break-all select-all">{apiTriggerURL(trigger.path)}</code> · <button type="button" className="text-primary hover:underline" onClick={() => void copy(apiTriggerURL(trigger.path), 'Address copied')}>Copy</button> · <button type="button" disabled={busy} className="text-primary hover:underline disabled:opacity-50" onClick={() => void save(trigger, true)}>New secret</button></DetailLine>
        <DetailLine label="What runs">{trigger.starts ? `${trigger.starts}: ` : ''}“{trigger.message}”{onAsk && <> · <button type="button" className="text-primary hover:underline" onClick={() => void onAsk(`Change the message of the webhook “${trigger.name}” (id ${trigger.id}) in this Crew.`)}>Change</button></>}</DetailLine>
        {trigger.changed_by && <DetailLine label="History">{trigger.changed_by}</DetailLine>}
        <DetailLine label="Where"><select aria-label={`Run destination for ${trigger.name}`} disabled={busy} value={trigger.run_destination || 'chat'} onChange={event => void save({ ...trigger, run_destination: event.target.value as ProductAPITrigger['run_destination'] })} className="rounded-md border border-border bg-background px-2 py-0.5 text-xs text-foreground">
          {(destinations.length ? destinations : [{ id: trigger.run_destination || 'chat', label: trigger.run_destination || 'chat' }]).map(choice => <option key={choice.id} value={choice.id}>{choice.label}</option>)}
        </select></DetailLine>
        <DetailLine label="Recent">{(recent[trigger.id] || []).length === 0 ? 'No calls yet' : (recent[trigger.id] || []).slice(0, 2).map(run => `${relativeTime(run.started_at)}: ${outcome(run)}`).join(' · ')}{deliveryHistory && <> · <button type="button" className="text-primary hover:underline" onClick={() => setShowAllRuns(value => !value)}>{showAllRuns ? 'Hide runs' : 'All runs'}</button></>}</DetailLine>
        <p className="pt-1 text-[11px] text-muted-foreground">Send JSON up to 1 MiB. Use the Authorization header for a secret key; GitHub signs its own requests. Reuse an Idempotency-Key to avoid duplicate runs.</p>
        <button type="button" disabled={busy} className="text-destructive hover:underline disabled:opacity-50" onClick={() => void remove(trigger.id)}>Remove</button>
      </PageRow>)}
      {!hideEmpty && triggers.length === 0 && <p className="px-4 py-6 text-center text-sm text-muted-foreground">No webhooks yet. Ask your agent to add one.</p>}
      {copied && <p role="status" className="px-4 py-2 text-xs text-muted-foreground">{copied}</p>}
      {showAllRuns && deliveryHistory && <div className="min-w-0 max-w-full overflow-hidden">{deliveryHistory}</div>}
    </div>
  }
  return <div className={`${embedded ? '' : 'h-full'} min-w-0 w-full max-w-none overflow-x-hidden overflow-y-auto bg-background`}>
    {!hideHeader && <WorkspaceViewHeader
      sticky
      icon={Webhook}
      title="Triggers"
      subtitle="Send one saved message to this project when an external service sends authenticated JSON."
      actions={<WorkspaceViewIconButton label="Refresh project triggers" onClick={() => void refresh()} />}
    />}
    <div className="space-y-4 p-4">
    {!embedded && <AutomationIntro icon={Webhook} tone="amber" title="Webhooks">
      A web address other services (GitHub, your CI, a form) call to start work in this {profileId === 'work' ? 'project' : 'Crew'}. Ask the chat to add one.
    </AutomationIntro>}
    {profileId === 'work' && <OnlineOnlyNote projectId={scope.projectId} />}
    {error && <p role="alert" className="rounded-md border border-destructive/30 p-3 text-sm text-destructive">{error}</p>}
    {issued?.secret && <section className="space-y-3 rounded-lg border border-primary/30 bg-primary/5 p-3">
      <h3 className="text-sm font-medium">New secret for {issued.name}</h3>
      <p className="text-xs text-muted-foreground">Copy it now. It is only shown when created or rotated. Anyone holding this URL and secret can start a run as the owner, with the owner&apos;s connections and secrets: keep it private, and rotate it if it leaks.</p>
      <code className="block break-all rounded bg-background p-2 text-xs select-all">{issued.secret}</code>
      <button type="button" className={buttonClass} onClick={() => void copy(issued.secret!, 'Secret copied')}>Copy secret</button>
    </section>}
    {triggers.length > 0 && <AutomationListBox>{triggers.map(trigger => <AutomationRow key={trigger.id} icon={Webhook} tone="amber" open={openId === trigger.id} onToggle={() => setOpenId(openId === trigger.id ? null : trigger.id)} title={trigger.name} summary={trigger.message} meta={trigger.auth_mode === 'github' ? 'From GitHub' : 'Any service with the key'} status={trigger.enabled ? 'on' : 'paused'} statusLabel={trigger.enabled ? 'On' : 'Off'}>
      <div className="flex items-start gap-2"><code className="min-w-0 flex-1 break-all text-xs select-all">{apiTriggerURL(trigger.path)}</code><button type="button" aria-label={`Copy endpoint for ${trigger.name}`} className={buttonClass} onClick={() => void copy(apiTriggerURL(trigger.path), 'Endpoint copied')}><Copy size={13} /></button></div>
      <p className="line-clamp-2 text-xs text-muted-foreground" title={trigger.message}>{trigger.auth_mode === 'github' ? 'GitHub signature' : 'Bearer token'} · {trigger.message}</p>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <label className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
        <span className="shrink-0 font-medium text-foreground">Run in</span>
        <select aria-label={`Run destination for ${trigger.name}`} disabled={busy} value={trigger.run_destination || 'chat'} onChange={event => void save({ ...trigger, run_destination: event.target.value as ProductAPITrigger['run_destination'] })} className="rounded-md border border-border bg-background px-2 py-1 text-xs text-foreground">
          {(destinations.length ? destinations : [{ id: trigger.run_destination || 'chat', label: trigger.run_destination || 'chat' }]).map(choice => <option key={choice.id} value={choice.id}>{choice.label}</option>)}
        </select>
        </label>
        <div className="flex flex-wrap gap-1.5"><button type="button" disabled={busy} className={buttonClass} onClick={() => void save({ ...trigger, enabled: !trigger.enabled })}>{trigger.enabled ? 'Disable' : 'Enable'}</button><button type="button" disabled={busy} className={buttonClass} onClick={() => void save(trigger, true)}>Rotate secret</button><button type="button" disabled={busy} className={buttonClass} onClick={() => void remove(trigger.id)}>Remove</button></div>
      </div>
    </AutomationRow>)}</AutomationListBox>}{!hideEmpty && triggers.length === 0 && <EmptyAutomations>No webhooks yet. Ask the chat to create one.</EmptyAutomations>}
    {copied && <p role="status" className="text-xs text-muted-foreground">{copied}</p>}
    {deliveryHistory && <div className="min-w-0 max-w-full overflow-hidden rounded-lg border border-border">{deliveryHistory}</div>}
    <p className="text-xs leading-relaxed text-muted-foreground">Send JSON up to 1 MiB. Bearer triggers use the Authorization header; GitHub triggers verify X-Hub-Signature-256. Reuse an Idempotency-Key or GitHub delivery ID to avoid duplicate runs.</p>
    </div>
  </div>
}
