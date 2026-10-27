import { useCallback, useEffect, useState } from 'react'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { SecretField } from '../ui/SecretField'
import { ToggleRow } from '../ui/ToggleRow'
import { SlackChecksView, SlackManifestSetup, SlackPermissionsChecklist } from '../workflow/bots/SlackAppSetupSteps'
import { slackErrorText } from '../workflow/bots/SlackSlugsSection'
import type { SlackTestResponse } from '../../services/api-types'
import { slackBotName, slackBotsApi, type SlackBotState, type VaultSlackBot } from './ProductSlackPanel'

/** Write-only credentials over the existing central registry. No token values are loaded. */
export function VaultSlackBots() {
 const [data,setData]=useState<SlackBotState|null>(null)
 const [editing,setEditing]=useState<VaultSlackBot|'new'|null>(null)
 const [name,setName]=useState('')
 const [scope,setScope]=useState('')
 const [bot,setBot]=useState('')
 const [app,setApp]=useState('')
 const [enabled,setEnabled]=useState(true)
 const [busy,setBusy]=useState(false)
 const [error,setError]=useState('')
 const [notice,setNotice]=useState('')
 const [checks,setChecks]=useState<SlackTestResponse|null>(null)
 const load=useCallback(async()=>{try{setData(await slackBotsApi.list());setError('')}catch(cause){setError(slackErrorText(cause,'Could not load your Slack bots.'))}},[])
 useEffect(()=>{void load()},[load])
 const edit=(item:VaultSlackBot|'new')=>{setEditing(item);setName(item==='new'?'':item.name);setEnabled(item==='new'?true:item.enabled);setBot('');setApp('');setScope('');setChecks(null);setError('');setNotice('')}
 const save=async()=>{setBusy(true);try{await slackBotsApi.act('save',{connection_id:editing==='new'?'':editing?.id,workspace_path:editing==='new'?scope:'',display_name:name,enabled,...(bot?{bot_token:bot}:{}),...(app?{app_token:app}:{})});setBot('');setApp('');setEditing(null);setNotice('Bot saved. Choose it on each Crew or Goal’s Slack page.');await load()}catch(cause){setError(slackErrorText(cause,'Could not save this bot.'))}finally{setBusy(false)}}
 const act=async(action:string,item:VaultSlackBot)=>{setBusy(true);setChecks(null);try{const result=await slackBotsApi.act(action,{connection_id:item.id});if(action==='test')setChecks(result as SlackTestResponse);else {setNotice('Bot removed.');await load()};setError('')}catch(cause){setError(slackErrorText(cause,'Could not update this bot.'))}finally{setBusy(false)}}
 return <section aria-label="Slack bots" className="space-y-3 rounded-lg border border-border p-4">
  <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-sm font-semibold">Slack bots</h2>{!!data?.scopes.length&&<Button size="sm" variant="outline" onClick={()=>edit('new')} disabled={busy}>Add Slack bot</Button>}</div>
  <p className="text-xs text-muted-foreground">Save each bot once. Choose it from a Crew or Goal’s Slack page. Its existing owners and administrators keep management access.</p>
  {error&&<p role="alert" className="text-xs text-destructive">{error} <button className="underline" onClick={()=>void load()}>Retry</button></p>}
  {notice&&<p role="status" className="text-xs">{notice}</p>}
  {!data&&!error&&<p className="text-xs">Loading bots…</p>}
  {data?.bots.filter(item=>!item.shared).map(item=><div key={item.id} className="flex flex-wrap items-center gap-2 rounded-md border border-border p-3 text-xs">
   <div className="min-w-0 flex-1"><p className="break-all font-medium">{slackBotName(item)}</p>{item.name !== 'Default' && item.name !== slackBotName(item) && <p className="mt-1 text-muted-foreground">Saved as {item.name}</p>}<p className="mt-1 text-muted-foreground">{item.identity?.workspace ? `${item.identity.workspace} · ` : ''}{!item.configured?'Needs setup':item.enabled?'Saved':'Paused'} · Managed by {item.scope_label}</p></div>
   {item.can_manage&&<><Button variant="outline" size="xs" disabled={busy} onClick={()=>edit(item)}>Change bot</Button><Button variant="ghost" size="xs" disabled={busy} onClick={()=>void act('test',item)}>Test</Button><Button variant="ghost" size="xs" disabled={busy} onClick={()=>void act('remove',item)}>Remove</Button></>}
  </div>)}
  {data&&!data.bots.some(item=>!item.shared)&&<p className="text-xs text-muted-foreground">No saved bot yet.{!data.scopes.length&&' Create a Crew or Goal first to manage its bot.'}</p>}
  {editing&&<form aria-label="Save Slack bot" className="space-y-3 rounded-md border border-border p-3 text-xs" onSubmit={event=>{event.preventDefault();void save()}}>
   <label className="block">Label in AgentWorks<Input aria-label="Bot label" value={name} onChange={event=>setName(event.target.value)}/></label>
   {editing==='new'&&<label className="flex flex-col gap-1.5">Managed by<select aria-label="Bot management scope" className="h-9 w-full rounded-md border border-border bg-background px-2" value={scope} onChange={event=>setScope(event.target.value)}><option value="" disabled>Choose a Crew or Goal you own</option>{data?.scopes.map(item=><option key={item.workspace_path} value={item.workspace_path}>{item.label}</option>)}</select><span className="text-muted-foreground">Its existing owners manage this bot. It can be selected by other items they manage.</span></label>}
   <SecretField label="Bot token" value={bot} onChange={setBot} placeholder={editing==='new'?'xoxb-…':'Leave blank to keep the saved token'}/>
   <SecretField label="App token" value={app} onChange={setApp} placeholder={editing==='new'?'xapp-…':'Leave blank to keep the saved token'}/>
   <p className="text-muted-foreground">Tokens are encrypted. Saved values are never shown here.</p>
   <ToggleRow label="Enabled" checked={enabled} onCheckedChange={setEnabled}/>
   <details><summary className="cursor-pointer">Slack app setup instructions</summary><div className="mt-2 space-y-3"><SlackManifestSetup defaultName={name||'AgentWorks'} finalStep="Save the bot here, then choose it on the Crew or Goal’s Slack page."/><SlackPermissionsChecklist/></div></details>
   <div className="flex gap-2"><Button type="submit" size="sm" disabled={busy||!name.trim()||(editing==='new'&&(!scope||!bot||!app))}>{busy?'Saving…':'Save bot'}</Button><Button type="button" size="sm" variant="outline" disabled={busy} onClick={()=>{setEditing(null);setBot('');setApp('')}}>Cancel</Button></div>
  </form>}
  {checks&&<SlackChecksView result={checks}/>}
 </section>
}
