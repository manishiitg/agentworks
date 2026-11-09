import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { ChevronDown, RefreshCw } from 'lucide-react'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { VaultSlackBots } from './VaultSlackBots'
import api, { agentApi } from '../../services/api'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import type { SlackTargetSettings, SlackTestResponse } from '../../services/api-types'
import { SlackBotWhere } from '../workflow/bots/SlackBotWhere'
import { SlackSlugsSection, slackErrorText } from '../workflow/bots/SlackSlugsSection'
import { SlackChecksView } from '../workflow/bots/SlackAppSetupSteps'
import SlackAccountsPanel from './SlackAccountsPanel'

export interface VaultSlackBot { id: string; name: string; configured: boolean; enabled: boolean; can_manage: boolean; scope_label: string; shared: boolean; default: boolean; can_select: boolean; used_in: { label: string; kind: string }[]; identity?: { name?: string; workspace?: string; verified: boolean } }
export interface SlackBotState {
 bots: VaultSlackBot[]; scopes: { workspace_path: string; label: string }[]
 selected_id: string; allow_bots?: boolean; can_select?: boolean; can_create_company?: boolean
 target?: { workspace_path: string; profile_id?: string }; settings?: SlackTargetSettings
}
export const slackBotName = (bot: VaultSlackBot) => bot.identity?.name || (bot.name === 'Default' ? 'Saved Slack bot' : bot.name)
export const slackBotsApi = {
 list: async (workspacePath = '') => (await api.get<SlackBotState>('/api/integrations/slack/bots', { params: { workspace_path: workspacePath } })).data,
 act: async (action: string, args: Record<string, unknown> = {}) => (await api.post('/api/integrations/slack/bots', { action, ...args })).data,
}

export type SlackBotOverview = { status: string; names: string[]; allowed?: boolean }

/** Two independent connections on one page; status comes from the saved server state. */
export function ProductSlackPanel(props: { workspacePath: string; noun?: string; allowBots?: boolean; section?: 'account' | 'bot'; tabId?: string }) {
 const actor = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 return <SlackConnections key={`${server}:${actor}:${props.workspacePath}`} {...props}/>
}

function SlackConnections({ workspacePath, noun = 'Crew', allowBots = true, section = 'account', tabId }: { workspacePath: string; noun?: string; allowBots?: boolean; section?: 'account' | 'bot'; tabId?: string }) {
 const [bot, setBot] = useState<SlackBotOverview>({status:'Checking…',names:[]})
 const [account, setAccount] = useState('Checking…')
 return <div className="w-full max-w-3xl space-y-4">
  <p className="text-sm text-muted-foreground">Use a bot, your own account, or both. Each connection keeps its own permissions.</p>
  {allowBots && bot.allowed !== false && <details open={section === 'bot'} aria-label="Slack bot connection" className="group rounded-lg border border-border p-4">
   <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-2"><span className="flex items-center gap-2 text-sm font-semibold"><ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180"/>Let people message this {noun}</span><span className="text-xs text-muted-foreground">{bot.status}</span></summary>
   <div className="mt-4"><SlackBotChoice workspacePath={workspacePath} noun={noun} embedded onOverview={setBot}/></div>
  </details>}
  <details open={section === 'account' || !allowBots} aria-label="Personal Slack connection" className="group rounded-lg border border-border p-4">
   <summary className="flex cursor-pointer list-none flex-wrap items-center justify-between gap-2"><span className="flex items-center gap-2 text-sm font-semibold"><ChevronDown className="h-4 w-4 transition-transform group-open:rotate-180"/>Use Slack as me</span><span className="text-xs text-muted-foreground">{account}</span></summary>
   <div className="mt-4"><SlackAccountsPanel workspacePath={workspacePath} noun={noun} tabId={tabId} embedded savedBotNames={bot.names} onStatus={setAccount}/></div>
  </details>
 </div>
}

/**
 * The Crew's Slack bot: which saved bot it answers with, its Slack name and where it answers. layout "list" is the
 * Goal's Slack page (one list, settings under the chosen bot); layout "cards" is the Crew's Share › Slack (owner, 9 Nov
 * mock): the same controls in a Bot card, a How people reach it card and the server's Who can talk to it card.
 */
export function SlackBotChoice({ workspacePath, noun = 'Crew', embedded = false, onOverview, layout = 'list', who }: { workspacePath: string; noun?: string; embedded?: boolean; onOverview?: (overview: SlackBotOverview) => void; layout?: 'list' | 'cards'; who?: ReactNode }) {
 const [data, setData] = useState<SlackBotState | null>(null)
 const [error, setError] = useState('')
 const [busy, setBusy] = useState(false)
 const [slug, setSlug] = useState('')
 const [checks, setChecks] = useState<{ id: string; result: SlackTestResponse } | null>(null)
 const [changing, setChanging] = useState(false)
 const update = useCallback((next: SlackBotState) => {
  setData(next); setSlug(next.settings?.slug || ''); setError('')
  const selected = next.bots.find(bot => bot.id === next.selected_id)
  onOverview?.({allowed:next.allow_bots,status:selected ? !selected.configured ? 'Needs setup' : !selected.enabled ? 'Paused' : `Bot saved · ${slackBotName(selected)}` : next.bots.some(bot => bot.configured) ? 'Choose a saved bot' : 'Not set up',names:next.bots.filter(bot => bot.configured).map(slackBotName)})
 },[onOverview])
 const refresh = useCallback(async () => { try { update(await slackBotsApi.list(workspacePath)) } catch (cause) { setError(slackErrorText(cause, 'Could not load Slack bots.')); onOverview?.({status:'Could not load',names:[]}) } }, [workspacePath, update, onOverview])
 useEffect(() => { void refresh(); const focus = () => void refresh(); window.addEventListener('focus', focus); return () => window.removeEventListener('focus', focus) }, [refresh])
 if (data?.allow_bots === false) return null
 const chosen = data?.bots.find(bot => bot.id === data.selected_id)
 const result = checks?.id === chosen?.id ? checks?.result : null
 const select = async (id: string) => { setBusy(true); setChecks(null); try { const next = await slackBotsApi.act('select', { workspace_path: workspacePath, connection_id: id }) as SlackBotState; update(next); setChanging(false) } catch (cause) { setError(slackErrorText(cause, 'Could not choose this bot.')) } finally { setBusy(false) } }
 const check = async () => {
  if (!chosen) return
  setBusy(true); setError(''); setChecks(null)
  try { setChecks({ id: chosen.id, result: await slackBotsApi.act('test', { connection_id: chosen.id }) as SlackTestResponse }); await refresh() }
  catch (cause) { setError(slackErrorText(cause, 'Could not check this bot.')) }
  finally { setBusy(false) }
 }
 const status = chosen ? !chosen.configured ? 'Needs setup' : !chosen.enabled ? 'Paused' : result ? result.success ? 'Token checks passed · Verify message delivery in Slack' : 'Connection check failed' : chosen.identity?.verified ? 'Identity verified · Delivery not checked' : 'Saved · Connection not checked' : ''
 const checkButton = chosen?.can_manage ? <Button size="sm" variant="outline" disabled={busy || !chosen.configured || !chosen.enabled} onClick={() => void check()}><RefreshCw className={`mr-1.5 h-3.5 w-3.5 ${busy ? 'animate-spin' : ''}`}/>Check connection</Button> : null
 const nameRow = chosen && data?.settings && data.target ? <>
  <div className="flex flex-wrap items-end gap-2 border-t border-border pt-4"><label className="min-w-0 flex-1 text-xs text-muted-foreground">Name for this {noun} in Slack<Input className="mt-1.5" aria-label="Slack slug" value={slug} onChange={event => setSlug(event.target.value)} disabled={!data.can_select || busy}/></label><Button size="sm" variant="outline" disabled={!data.can_select || busy || slug === data.settings.slug || !slug} onClick={async () => { setBusy(true); try { await agentApi.updateSlackTargetSettings(data.target!, { slug }); await refresh() } catch (cause) { setError(slackErrorText(cause, 'Could not save the Slack name.')) } finally { setBusy(false) } }}>Save name</Button></div>
  <p className="break-words text-xs text-muted-foreground">{data.settings.dm_only ? `Message ${slackBotName(chosen)} directly with “${slug} your question”.` : `Message @${slackBotName(chosen)} ${slug} followed by your question.`}</p>
 </> : null
 const where = chosen && data?.settings && data.target ? chosen.shared ? <SlackSlugsSection hideSwitch destination={data.target} noun={noun} readOnly={!data.can_select} dmOnly={data.settings.dm_only} settings={{...data.settings, platform_name: slackBotName(chosen)}} onSettings={settings => setData({ ...data, settings })}/> : <SlackBotWhere botId={chosen.id} botName={slackBotName(chosen)} destination={data.target} dmOnly={data.settings.dm_only}/> : null
 const checkDetails = result ? <details className="text-xs" open={!result.success}><summary className="cursor-pointer text-muted-foreground">Connection check details</summary><div className="mt-3"><SlackChecksView result={result}/></div></details> : null
 const ownerOnly = data && !data.can_select ? <p className="text-xs text-muted-foreground">Only an owner can change which bot this {noun} uses.</p> : null
 const errorLine = error ? <p role="alert" className="text-destructive">{error} <button className="underline" onClick={() => void refresh()}>Retry</button></p> : null
 if (layout === 'cards') {
  const card = 'space-y-3 rounded-xl border border-border px-3.5 py-3'
  return <section aria-label={`Let people talk to this ${noun} in Slack`} className="space-y-2.5 text-sm">
   {errorLine}
   {!data && !error ? <p role="status" className="text-muted-foreground">Loading bots…</p> : data && <>
    <div aria-label="Bot" className={card}>
     <p className="text-[15px] font-semibold">Bot</p>
     {chosen && !changing ? <div className="flex flex-wrap items-center gap-2">
      <span className="min-w-0 flex-1"><span className="block break-words font-medium">{slackBotName(chosen)}</span><span role="status" className="block text-xs text-muted-foreground">{[chosen.identity?.workspace, status].filter(Boolean).join(' · ')}</span></span>
      {checkButton}
      {data.can_select && <Button size="sm" variant="outline" disabled={busy} onClick={() => setChanging(true)}>Change bot</Button>}
     </div> : data.can_select ? <>
      {!chosen && <p><span className="block font-medium">No Slack bot yet</span><span className="block text-xs text-muted-foreground">Use your company's shared bot, or add your own.</span></p>}
      <VaultSlackBots data={data} workspacePath={workspacePath} busy={busy} onSelect={select} onUpdated={refresh}/>
      {chosen && <Button size="sm" variant="ghost" disabled={busy} onClick={() => setChanging(false)}>Done</Button>}
     </> : null}
     {checkDetails}
     {ownerOnly}
    </div>
    {nameRow && chosen && data.settings && <div aria-label="How people reach it" className={card}>
     <p className="text-[15px] font-semibold">How people reach it</p>
     {nameRow}
     <div className="border-t border-border pt-3">{where}</div>
     {!data.settings.dm_only && <p className="text-xs text-muted-foreground">Direct messages: people can also message @{slackBotName(chosen)} directly.</p>}
    </div>}
    {who && <div aria-label="Who can talk to it" className={card}>
     <p className="text-[15px] font-semibold">Who can talk to it</p>
     {who}
    </div>}
   </>}
  </section>
 }
 return <section aria-label={`Let people talk to this ${noun}`} className="space-y-5 text-sm">
  {!embedded && <div><h3 className="text-base font-semibold">Slack bots</h3><p className="mt-1 text-sm text-muted-foreground">Let people talk to this {noun} in Slack, with their existing access.</p></div>}
  {errorLine}
  {!data && !error ? <p role="status" className="text-muted-foreground">Loading bots…</p> : data && <>
   <VaultSlackBots data={data} workspacePath={workspacePath} busy={busy} onSelect={select} onUpdated={refresh}>
   {chosen && <div className="space-y-4 border-t border-border pt-3">
    <div className="flex flex-wrap items-center justify-between gap-2"><p role="status" className="text-xs text-muted-foreground">{status}</p>
     {checkButton}
    </div>
    {nameRow}
   </div>}
   {checkDetails}
   {where && <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Channels and display settings</summary><div className="mt-4">{where}</div></details>}
   </VaultSlackBots>
   {ownerOnly}
  </>}
 </section>
}
