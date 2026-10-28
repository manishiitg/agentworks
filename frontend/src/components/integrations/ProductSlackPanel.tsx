import { useCallback, useEffect, useId, useState } from 'react'
import { RefreshCw } from 'lucide-react'
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

/** Presentation tabs only: personal-account lending and bot administration keep their own server checks. */
export function ProductSlackPanel({ workspacePath, noun = 'Crew', allowBots = true, section = 'account', tabId }: { workspacePath: string; noun?: string; allowBots?: boolean; section?: 'account' | 'bot'; tabId?: string }) {
 const actor = useAuthStore(state => state.user?.id)
 const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
 const [active, setActive] = useState(section)
 const id = useId()
 useEffect(() => { setActive(section) }, [section, workspacePath])
 const current = allowBots ? active : 'account'
 return <div className="w-full max-w-3xl space-y-5">
  {allowBots && <div role="tablist" aria-label="Slack connection type" className="flex gap-1 border-b border-border">
   {(['account', 'bot'] as const).map(value => <button key={value} type="button" role="tab" id={`${id}-${value}-tab`} aria-controls={`${id}-${value}`} aria-selected={current === value} onClick={() => setActive(value)} className={`min-h-11 border-b-2 px-3 py-2 text-sm ${current === value ? 'border-primary font-medium text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}`}>{value === 'account' ? 'My Slack account' : 'Slack bot'}</button>)}
  </div>}
  <section key={`${server}:${actor}:${workspacePath}:${current}`} role={allowBots ? 'tabpanel' : undefined} id={`${id}-${current}`} aria-labelledby={allowBots ? `${id}-${current}-tab` : undefined}>
   {current === 'account' ? <SlackAccountsPanel workspacePath={workspacePath} noun={noun} tabId={tabId}/> : <SlackBotChoice workspacePath={workspacePath} noun={noun}/>}
  </section>
 </div>
}

export function SlackBotChoice({ workspacePath, noun = 'Crew' }: { workspacePath: string; noun?: string }) {
 const [data, setData] = useState<SlackBotState | null>(null)
 const [error, setError] = useState('')
 const [busy, setBusy] = useState(false)
 const [slug, setSlug] = useState('')
 const [checks, setChecks] = useState<{ id: string; result: SlackTestResponse } | null>(null)
 const refresh = useCallback(async () => { try { const next = await slackBotsApi.list(workspacePath); setData(next); setSlug(next.settings?.slug || ''); setError('') } catch (cause) { setError(slackErrorText(cause, 'Could not load Slack bots.')) } }, [workspacePath])
 useEffect(() => { void refresh(); const focus = () => void refresh(); window.addEventListener('focus', focus); return () => window.removeEventListener('focus', focus) }, [refresh])
 if (data?.allow_bots === false) return null
 const chosen = data?.bots.find(bot => bot.id === data.selected_id)
 const result = checks?.id === chosen?.id ? checks?.result : null
 const select = async (id: string) => { setBusy(true); setChecks(null); try { const next = await slackBotsApi.act('select', { workspace_path: workspacePath, connection_id: id }) as SlackBotState; setData(next); setSlug(next.settings?.slug || ''); setError('') } catch (cause) { setError(slackErrorText(cause, 'Could not choose this bot.')) } finally { setBusy(false) } }
 const check = async () => {
  if (!chosen) return
  setBusy(true); setError(''); setChecks(null)
  try { setChecks({ id: chosen.id, result: await slackBotsApi.act('test', { connection_id: chosen.id }) as SlackTestResponse }); await refresh() }
  catch (cause) { setError(slackErrorText(cause, 'Could not check this bot.')) }
  finally { setBusy(false) }
 }
 return <section aria-label={`Let people talk to this ${noun}`} className="space-y-5 text-sm">
  <div><h3 className="text-base font-semibold">Slack bots</h3><p className="mt-1 text-sm text-muted-foreground">Let people talk to this {noun} in Slack, with their existing access.</p></div>
  {error && <p role="alert" className="text-destructive">{error} <button className="underline" onClick={() => void refresh()}>Retry</button></p>}
  {!data && !error ? <p role="status" className="text-muted-foreground">Loading bots…</p> : data && <>
   <VaultSlackBots data={data} workspacePath={workspacePath} busy={busy} onSelect={select} onUpdated={refresh}>
   {chosen && <div className="space-y-4 border-t border-border pt-3">
    <div className="flex flex-wrap items-center justify-between gap-2"><p role="status" className="text-xs text-muted-foreground">{!chosen.configured ? 'Needs setup' : !chosen.enabled ? 'Paused' : result ? result.success ? 'Token checks passed · Verify message delivery in Slack' : 'Connection check failed' : chosen.identity?.verified ? 'Identity verified · Delivery not checked' : 'Saved · Connection not checked'}</p>
     {chosen.can_manage && <Button size="sm" variant="outline" disabled={busy || !chosen.configured || !chosen.enabled} onClick={() => void check()}><RefreshCw className={`mr-1.5 h-3.5 w-3.5 ${busy ? 'animate-spin' : ''}`}/>Check connection</Button>}
    </div>
    {data.settings && data.target && <>
     <div className="flex flex-wrap items-end gap-2 border-t border-border pt-4"><label className="min-w-0 flex-1 text-xs text-muted-foreground">Name for this {noun} in Slack<Input className="mt-1.5" aria-label="Slack slug" value={slug} onChange={event => setSlug(event.target.value)} disabled={!data.can_select || busy}/></label><Button size="sm" variant="outline" disabled={!data.can_select || busy || slug === data.settings.slug || !slug} onClick={async () => { setBusy(true); try { await agentApi.updateSlackTargetSettings(data.target!, { slug }); await refresh() } catch (cause) { setError(slackErrorText(cause, 'Could not save the Slack name.')) } finally { setBusy(false) } }}>Save name</Button></div>
     <p className="break-words text-xs text-muted-foreground">{data.settings.dm_only ? `Message ${slackBotName(chosen)} directly with “${slug} your question”.` : `Message @${slackBotName(chosen)} ${slug} followed by your question.`}</p>
    </>}
   </div>}
   {result && <details className="text-xs" open={!result.success}><summary className="cursor-pointer text-muted-foreground">Connection check details</summary><div className="mt-3"><SlackChecksView result={result}/></div></details>}
   {chosen && data.settings && data.target && <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Channels and display settings</summary><div className="mt-4">{chosen.shared ? <SlackSlugsSection hideSwitch destination={data.target} noun={noun} readOnly={!data.can_select} dmOnly={data.settings.dm_only} settings={{...data.settings, platform_name: slackBotName(chosen)}} onSettings={settings => setData({ ...data, settings })}/> : <SlackBotWhere botId={chosen.id} botName={slackBotName(chosen)} destination={data.target} dmOnly={data.settings.dm_only}/>}</div></details>}
   </VaultSlackBots>
   {!data.can_select && <p className="text-xs text-muted-foreground">Only an owner can change which bot this {noun} uses.</p>}
  </>}
 </section>
}
