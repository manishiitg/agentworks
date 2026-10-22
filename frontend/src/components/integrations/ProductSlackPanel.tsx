import { useCallback, useEffect, useState } from 'react'
import api, { agentApi } from '../../services/api'
import { useAppStore } from '../../stores/useAppStore'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import type { SlackTargetSettings } from '../../services/api-types'
import { SlackBotWhere } from '../workflow/bots/SlackBotWhere'
import { SlackSlugsSection, slackErrorText } from '../workflow/bots/SlackSlugsSection'
import SlackAccountsPanel from './SlackAccountsPanel'

export interface VaultSlackBot { id: string; name: string; configured: boolean; enabled: boolean; can_manage: boolean; scope_label: string; shared: boolean }
export interface SlackBotState {
 bots: VaultSlackBot[]; scopes: { workspace_path: string; label: string }[]
 selected_id: string; allow_bots?: boolean; can_select?: boolean
 target?: { workspace_path: string; profile_id?: string }; settings?: SlackTargetSettings
}
export const slackBotsApi = {
 list: async (workspacePath = '') => (await api.get<SlackBotState>('/api/integrations/slack/bots', { params: { workspace_path: workspacePath } })).data,
 act: async (action: string, args: Record<string, unknown> = {}) => (await api.post('/api/integrations/slack/bots', { action, ...args })).data,
}

/** One product page. Personal account grants and bot management remain separate server permissions. */
export function ProductSlackPanel({ workspacePath, noun = 'Crew', allowBots = true, section }: { workspacePath: string; noun?: string; allowBots?: boolean; section?: 'account' | 'bot' }) {
 return <div className="w-full max-w-3xl space-y-5">
  {section !== 'bot' && <section aria-label="Send as me" className="space-y-2"><h3 className="text-sm font-semibold">Send as me</h3>
   <p className="text-xs text-muted-foreground">Let this {noun} send Slack messages as you. Only you can read or search your Slack.</p>
   <SlackAccountsPanel workspacePath={workspacePath}/>
  </section>}
  {section !== 'account' && allowBots && <SlackBotChoice workspacePath={workspacePath} noun={noun}/>}
 </div>
}

export function SlackBotChoice({ workspacePath, noun = 'Crew' }: { workspacePath: string; noun?: string }) {
 const [data,setData]=useState<SlackBotState|null>(null)
 const [error,setError]=useState('')
 const [busy,setBusy]=useState(false)
 const [slug,setSlug]=useState('')
 const openVault=useAppStore(state=>state.setAdminPage)
 const refresh=useCallback(async()=>{try{const next=await slackBotsApi.list(workspacePath);setData(next);setSlug(next.settings?.slug||'');setError('')}catch(cause){setError(slackErrorText(cause,'Could not load Slack bots.'))}},[workspacePath])
 useEffect(()=>{void refresh()},[refresh])
 useEffect(()=>{const focus=()=>void refresh();window.addEventListener('focus',focus);return()=>window.removeEventListener('focus',focus)},[refresh])
 if(data?.allow_bots===false)return null
 const chosen=data?.bots.find(bot=>bot.id===data.selected_id)
 const select=async(id:string)=>{setBusy(true);try{const next=await slackBotsApi.act('select',{workspace_path:workspacePath,connection_id:id}) as SlackBotState;setData(next);setSlug(next.settings?.slug||'');setError('')}catch(cause){setError(slackErrorText(cause,'Could not choose this bot.'))}finally{setBusy(false)}}
 return <section aria-label={`Let people talk to this ${noun}`} className="w-full max-w-3xl space-y-5 text-sm">
  <div className="space-y-2"><h3 className="text-base font-semibold">Talk to this {noun} in Slack</h3>
  <p className="text-sm leading-6 text-muted-foreground">Choose a bot from My Vault. People can message it in Slack to reach this {noun}, with their existing access.</p></div>
  {error&&<p role="alert" className="text-destructive">{error} <button className="underline" onClick={()=>void refresh()}>Retry</button></p>}
  {!data&&!error?<p role="status">Loading bots…</p>:data&&<>
   <div className="space-y-3 rounded-lg border border-border p-4"><label className="flex flex-col gap-2 font-medium">Slack bot<select aria-label="Slack bot from My Vault" className="h-9 w-full rounded-md border border-border bg-background px-3 text-sm font-normal" disabled={!data.can_select||busy} value={data.selected_id} onChange={event=>void select(event.target.value)}>
    <option value="" disabled>Choose a bot from My Vault</option>
    {data.bots.map(bot=><option key={bot.id} value={bot.id}>{bot.name}{!bot.configured?' · Needs setup':!bot.enabled?' · Paused':''}{bot.scope_label&&bot.id!==data.selected_id?` · also used by ${bot.scope_label}`:''}</option>)}
   </select></label>
   <p className="text-xs leading-5 text-muted-foreground">One bot can serve several Crews and Goals.</p>
   {!data.can_select&&<p className="text-xs text-muted-foreground">Only an owner can change which bot this {noun} uses.</p>}
   </div>
   {!data.bots.length&&<p className="text-muted-foreground">You have no Slack bot yet. Add one in My Vault, then return here.</p>}
   {chosen&&data.settings&&data.target&&<>
    <p className="text-muted-foreground">{chosen.configured&&chosen.enabled?'Connected':'Check this bot in My Vault'}</p>
    <div className="flex flex-wrap items-end gap-2"><label className="min-w-0 flex-1">Name to use in Slack<Input aria-label="Slack slug" value={slug} onChange={event=>setSlug(event.target.value)} disabled={!data.can_select||busy}/></label><Button size="sm" variant="outline" disabled={!data.can_select||busy||slug===data.settings.slug||!slug} onClick={async()=>{setBusy(true);try{await agentApi.updateSlackTargetSettings(data.target!,{slug});await refresh()}catch(cause){setError(slackErrorText(cause,'Could not save the Slack name.'))}finally{setBusy(false)}}}>Save name</Button></div>
    {chosen.shared?<SlackSlugsSection hideSwitch destination={data.target} noun={noun} readOnly={!data.can_select} dmOnly={data.settings.dm_only} settings={data.settings} onSettings={settings=>setData({...data,settings})}/>:<SlackBotWhere botId={chosen.id} botName={chosen.name} destination={data.target} dmOnly={data.settings.dm_only}/>}
    <p className="break-words text-muted-foreground">{data.settings.dm_only?`In a direct message to ${chosen.name}, start with “${slug}” to reach this ${noun}, for example: ${slug} your question`:`@${chosen.name} ${slug} your question`}</p>
   </>}
  </>}
  <Button size="sm" variant="outline" onClick={()=>openVault('vaults')}>Manage bots in My Vault</Button>
 </section>
}
