import { useEffect, useRef, useState } from 'react'
import { teamRequest, type TeamTarget } from './teamApi'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'

export type SetupPerson = { person_id:string; name:string; done:number; total:number }
export type TeamSetupSummary = { id:string; title:string; revision:number; instruction_revision:number; instructions_available:boolean; done:number; total:number; people:SetupPerson[] }
export type TeamSetupList = { setups:TeamSetupSummary[]; to_do:number }
type Detail = TeamSetupSummary & { instructions:string; shared_by:string; items:{id:string;label:string}[]; my_items:Record<string,boolean>; key_status:{name:string;provider?:string;state:string;source?:string}[] }
type Target = {id:string;name:string;online:boolean}
const progress = (person:{done:number;total:number}) => `${person.done} of ${person.total}`
const people = (rows:SetupPerson[]) => rows.map(person=>`${person.name}: ${person.done===person.total?'done':progress(person)}`).join(' · ')
const button = 'rounded border border-border px-2 py-1 text-xs hover:bg-background/50 disabled:opacity-50'

/** No timer: the parent uses the existing visible Team refresh. Progress is never edited in the browser. */
export function TeamSetups({target,projectId,list,refreshKey}:{target:TeamTarget;projectId:string;list?:TeamSetupList;refreshKey:number}) {
 const [selected,setSelected]=useState('')
 const [detail,setDetail]=useState<Detail>()
 const [targets,setTargets]=useState<Target[]>([])
 const [agent,setAgent]=useState('')
 const [error,setError]=useState('')
 const [notice,setNotice]=useState('')
 const [busy,setBusy]=useState(false)
 const alive=useRef(false)
 const submission=useRef({key:'',id:''})
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 useEffect(()=>{
  let active=true
  const identity=captureChatIdentity()
  if(!selected) {setDetail(undefined);return}
  void Promise.all([
   teamRequest<Detail>(target,`/checklists?setup_id=${encodeURIComponent(selected)}`),
   teamRequest<{agents:Target[]}>(target,'/checklists','POST',{action:'setup_targets'}),
  ]).then(([value,rows])=>{
   if(!active||!isChatIdentityCurrent(identity)) return
   setDetail(value);setTargets(rows.agents);setError('')
   setAgent(old=>rows.agents.some(row=>row.id===old&&row.online)?old:rows.agents.find(row=>row.online)?.id||'')
  }).catch(e=>{if(active&&isChatIdentityCurrent(identity)){setDetail(undefined);setError(e instanceof Error?e.message:'This setup is unavailable. Ask the sharer.')}})
  return()=>{active=false}
 },[selected,refreshKey,target.project,target.connection])
 const draft=(edit=false,theme=false)=>{
  const prompt=theme
   ? `Share my current personal theme as a Team setup called "Apply this theme" in Team ${target.project}. Use appearance operation=export (settings action=appearance over MCP) to export the real theme, save that JSON and Markdown instructions in the Team shared files, then team_sync setup_share (crew_team_sync over MCP, crew_id=${target.project}). The instructions must import the JSON using appearance operation=import, then apply that named theme to the applying person's own appearance. The agent verifies and fills the checklist; never claim it is applied just because it was imported. No keys or personal account data. Ask me if you cannot access my personal appearance.`
   : `${edit?`Edit Team setup ${selected}`:'Share this as a Team setup'} in Team ${target.project}. Use team_sync (crew_team_sync over MCP, crew_id=${target.project}). ${edit?'Read setup_get and current revisions first.':'Ask what instructions and checklist I want to share.'} Keep instructions in a Markdown file in the existing Team shared folder. Store only its path and revision with setup_share/setup_edit. Required keys are names only. People never tick boxes; their own agents verify and update setup_checklist.`
  if(!fillMainDraft(projectId,prompt)) useChatStore.getState().addToast('Open one of your own agent chats in this Crew, then try again. Nothing was sent.','info')
 }
 const apply=async()=>{
  if(!detail||!agent||busy) return
  const identity=captureChatIdentity()
  const key=JSON.stringify([detail.id,detail.revision,detail.instruction_revision,agent])
  if(submission.current.key!==key) submission.current={key,id:crypto.randomUUID()}
  setBusy(true);setError('')
  try {
   const result=await teamRequest<{notice:string}>(target,'/checklists','POST',{action:'setup_apply',setup_id:detail.id,setup_revision:detail.revision,instruction_revision:detail.instruction_revision,target_agent_id:agent,submission_id:submission.current.id})
   if(alive.current&&isChatIdentityCurrent(identity)) setNotice(result.notice||'Queued for your agent. Its reply will arrive in your inbox. The checklist changes only after it checks the work.')
  } catch(e) {if(alive.current&&isChatIdentityCurrent(identity)) setError(e instanceof Error?e.message:'Could not hand this setup to your agent. Try again.')}
  finally {if(alive.current&&isChatIdentityCurrent(identity)) setBusy(false)}
 }
 return <div className="space-y-3 text-xs">
  {selected?<>
   <button type="button" className="text-primary" disabled={busy} onClick={()=>{setSelected('');setError('');setNotice('')}}>‹ Team setups</button>
   {!detail&&!error&&<p>Loading setup…</p>}
   {detail&&<>
    <h3 className="font-semibold">{detail.title}</h3><p className="text-muted-foreground">Shared by {detail.shared_by} · you: {progress(detail)}</p>
    <h4 className="font-medium">Instructions for your agent</h4><p className="whitespace-pre-wrap break-words rounded border border-border p-2">{detail.instructions}</p>
    <h4 className="font-medium">Checklist · filled by your agent</h4>
    <ul className="space-y-1">{detail.items.map(item=><li key={item.id}><span aria-label={detail.my_items[item.id]?'Done':'Not checked'}>{detail.my_items[item.id]?'✓':'○'}</span> {item.label}</li>)}</ul>
    {!!detail.key_status.length&&<section aria-label="Needed keys"><h4 className="font-medium">Needed keys and sign-ins</h4>{detail.key_status.map(key=><p key={`${key.name}:${key.provider}`}>{key.name}{key.provider?` · ${key.provider}`:''} · {{added:'Added',connected_for_you:'Connected for you',missing:'Missing',unavailable:'Unknown'}[key.state]||'Unknown'}{key.source==='team'?' · Team (server only)':''}</p>)}</section>}
    <label className="block">Your connected agent<select aria-label="Your connected agent" className="mt-1 w-full rounded border border-border bg-background p-1" value={agent} disabled={busy} onChange={e=>{setAgent(e.target.value);setNotice('')}}><option value="">Choose your agent</option>{targets.filter(row=>row.online).map(row=><option key={row.id} value={row.id}>{row.name}</option>)}</select></label>
    {!targets.some(row=>row.online)&&<p className="text-muted-foreground">Connect one of your own agents to apply this setup.</p>}
    <button type="button" className={button} disabled={!agent||busy} onClick={()=>void apply()}>{busy?'Handing to your agent…':'Apply with my agent'}</button>{' '}<button type="button" className={button} disabled={busy} onClick={()=>draft(true)}>Edit from chat</button>
    <section aria-label="Who’s done"><h4 className="font-medium">Who’s done</h4><p className="text-muted-foreground">{people(detail.people)}</p></section>
   </>}
  </>:<>
   {!list?<p>Loading setups…</p>:!list.setups.length?<p className="text-muted-foreground">No Team setups yet. Share instructions and a checklist with your Team.</p>:<ul className="space-y-3">{list.setups.map(row=><li key={row.id}><button type="button" className="w-full text-left hover:text-primary" onClick={()=>{setSelected(row.id);setDetail(undefined);setError('');setNotice('')}}><span className="block font-medium">{row.title}</span><span className="text-muted-foreground">you: {progress(row)}{!row.instructions_available?' · setup unavailable, ask the sharer':''}</span><span className="block text-muted-foreground">{people(row.people)}</span></button></li>)}</ul>}
   <button type="button" className={button} onClick={()=>draft()}>+ Share a setup from chat</button>{' '}<button type="button" className={button} onClick={()=>draft(false,true)}>Share my theme from chat</button>
  </>}
  {notice&&<p role="status" className="text-muted-foreground">{notice}</p>}{error&&<p role="alert">{error}</p>}
 </div>
}
