import { useEffect, useRef, useState } from 'react'
import { teamRequest, type TeamTarget } from './teamApi'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { fillMainDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { PlainMarkdown } from '../../components/ui/PlainMarkdown'

export type SetupPerson = { person_id:string; name:string; done:number; total:number }
export type TeamSetupSummary = { id:string; title:string; revision:number; instruction_revision:number; instructions_available:boolean; done:number; total:number; people:SetupPerson[] }
export type TeamSetupList = { setups:TeamSetupSummary[]; to_do:number }
type Detail = TeamSetupSummary & { instructions:string; shared_by:string; items:{id:string;label:string}[]; my_items:Record<string,boolean>; key_status:{name:string;provider?:string;state:string;source?:string}[] }
type Target = {id:string;name:string;online:boolean}
const progress = (person:{done:number;total:number}) => `${person.done} of ${person.total}`
const finished = (person:{done:number;total:number}) => person.total>0&&person.done>=person.total
const Bar = ({done,total}:{done:number;total:number}) => <div role="progressbar" aria-label="Progress" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} className="my-2 h-1.5 overflow-hidden rounded bg-muted"><div className="h-full rounded bg-emerald-500" style={{width:`${total?Math.min(100,Math.round(done/total*100)):0}%`}}/></div>
/** Everyone's progress as small chips: initial, name, a tick when finished or "2 of 4". */
const People = ({rows}:{rows:SetupPerson[]}) => <div className="flex flex-wrap gap-1.5">{rows.map(person=><span key={person.person_id} className="inline-flex max-w-full items-center gap-1.5 rounded-full border border-border py-0.5 pl-0.5 pr-2 text-[11.5px] text-muted-foreground"><span aria-hidden="true" className="grid h-[18px] w-[18px] shrink-0 place-items-center rounded-full bg-muted text-[10px] text-foreground">{person.name.trim().charAt(0).toUpperCase()}</span><span className="truncate">{person.name}</span>{finished(person)?<span className="text-emerald-500" aria-label="Done">✓</span>:<span>{progress(person)}</span>}</span>)}</div>
const keyLabels:Record<string,string> = {added:'Added',connected_for_you:'Connected for you',missing:'Missing',unavailable:'Unknown'}
const button = 'rounded-lg border border-border bg-muted/40 px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50'
const primary = 'rounded-lg bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:opacity-90 disabled:opacity-50'
const heading = 'mb-1.5 mt-3 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'

/** Count the server's current checklist progress, never sign-ins or private setup details. */
const SetupPeople = ({rows}:{rows:SetupPerson[]}) => <section aria-label="Who is set up">
 <h4 className={heading}>Who is set up · {rows.filter(finished).length} of {rows.length}</h4>
 <People rows={rows}/>
</section>

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
   <button type="button" className="text-primary" disabled={busy} onClick={()=>{setSelected('');setError('');setNotice('')}}>‹ Setups</button>
   {!detail&&!error&&<p>Loading setup…</p>}
   {detail&&<>
    <div><h3 className="text-base font-semibold">{detail.title}</h3><p className="text-muted-foreground">Shared by {detail.shared_by} · you: {progress(detail)}</p><Bar done={detail.done} total={detail.total}/></div>
    <h4 className={heading}>What to do</h4><div className="break-words rounded-xl border border-border bg-muted/30 p-3"><PlainMarkdown content={detail.instructions}/></div>
    <h4 className={heading}>Checklist · your agent ticks these</h4>
    <ul className="divide-y divide-border">{detail.items.map(item=><li key={item.id} className="flex items-start gap-2.5 py-1.5"><span aria-label={detail.my_items[item.id]?'Done':'Not checked'} className={`mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded border text-[11px] ${detail.my_items[item.id]?'border-emerald-500 bg-emerald-500 text-background':'border-muted-foreground/60'}`}>{detail.my_items[item.id]?'✓':''}</span><span className="text-[13px]">{item.label}</span></li>)}</ul>
    {!!detail.key_status.length&&<section aria-label="Needed keys"><h4 className={heading}>Needed keys and sign-ins</h4>{detail.key_status.map(key=><p key={`${key.name}:${key.provider}`} className="flex items-center gap-2 py-0.5 text-[13px]"><span>{key.name}{key.provider?` · ${key.provider}`:''}</span><span className={`rounded-full border px-2 py-px text-[10.5px] ${key.state==='missing'?'border-amber-500/50 text-amber-500':'border-emerald-500/50 text-emerald-500'}`}>{keyLabels[key.state]||'Unknown'}</span>{key.source==='team'&&<span className="text-muted-foreground">Team (server only)</span>}</p>)}</section>}
    <div className="rounded-xl border border-border p-3">
     <label className="block">Your connected agent<select aria-label="Your connected agent" className="mt-1 w-full rounded-lg border border-border bg-background p-1.5" value={agent} disabled={busy} onChange={e=>{setAgent(e.target.value);setNotice('')}}><option value="">Choose your agent</option>{targets.filter(row=>row.online).map(row=><option key={row.id} value={row.id}>{row.name}</option>)}</select></label>
     {!targets.some(row=>row.online)&&<p className="mt-1.5 text-muted-foreground">Connect one of your own agents to apply this setup.</p>}
     <div className="mt-2.5 flex items-center gap-2"><button type="button" className={primary} disabled={!agent||busy} onClick={()=>void apply()}>{busy?'Handing to your agent…':'Apply with my agent'}</button><button type="button" className="text-muted-foreground underline disabled:opacity-50" disabled={busy} onClick={()=>draft(true)}>Edit from chat</button></div>
    </div>
    <SetupPeople rows={detail.people}/>
   </>}
  </>:<>
   {!list?<p>Loading setups…</p>:!list.setups.length?<div className="rounded-xl border border-dashed border-border p-5 text-center"><p className="mb-1 text-sm font-semibold">No Team setups yet</p><p className="text-muted-foreground">Share instructions and a checklist, and each teammate’s agent sets itself up from them.</p></div>:<ul className="space-y-2.5">{list.setups.map(row=><li key={row.id} className="rounded-xl border border-border bg-muted/20 p-3">
    <button type="button" className="block w-full text-left" aria-label={`Open ${row.title}`} onClick={()=>{setSelected(row.id);setDetail(undefined);setError('');setNotice('')}}>
     <span className="flex items-center gap-2"><span className="text-sm font-semibold">{row.title}</span><span className="ml-auto text-muted-foreground">{finished(row)?'Done ✓':`you: ${progress(row)}`}</span></span>
     <Bar done={row.done} total={row.total}/>
     {!row.instructions_available&&<span className="mb-1.5 block text-amber-500">Setup unavailable, ask the sharer</span>}
    </button>
    <SetupPeople rows={row.people}/>
   </li>)}</ul>}
   <div className="flex flex-wrap items-center gap-2"><button type="button" className={button} onClick={()=>draft()}>+ Share a setup</button><button type="button" className="text-muted-foreground underline" onClick={()=>draft(false,true)}>Share my theme</button></div>
  </>}
  {notice&&<p role="status" className="text-muted-foreground">{notice}</p>}{error&&<p role="alert" className="text-destructive">{error}</p>}
 </div>
}
