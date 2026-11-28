import { randomUUID } from '../../utils/randomUUID'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { Components } from 'react-markdown'
import { teamRequest, type TeamTarget } from './teamApi'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { fillLastAgentDraft } from '../../utils/agentPurposeDraft'
import { useChatStore } from '../../stores/useChatStore'
import { PlainMarkdown } from '../../components/ui/PlainMarkdown'

export type SetupNeed = { name:string; provider?:string; kind?:string; state:string; source?:string }
export type SetupPerson = { person_id:string; name:string; done:number; total:number; needs?:SetupNeed[] }
export type TeamSetupSummary = { id:string; title:string; shared_by?:string; revision:number; instruction_revision:number; instructions_available:boolean; done:number; total:number; for_you:boolean; assigned_to:string[]; people:SetupPerson[] }
export type TeamSetupList = { setups:TeamSetupSummary[]; to_do:number }
type Detail = TeamSetupSummary & { instruction:{path:string;revision:number}; instructions:string; shared_by:string; items:{id:string;label:string}[]; my_items:Record<string,boolean>; key_status:SetupNeed[] }
type Target = {id:string;name:string;online:boolean}
const progress = (person:{done:number;total:number}) => `${person.done} of ${person.total}`
const finished = (person:{done:number;total:number}) => person.total>0&&person.done>=person.total
const Bar = ({done,total}:{done:number;total:number}) => <div role="progressbar" aria-label="Progress" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} className="my-2 h-1.5 overflow-hidden rounded bg-muted"><div className="h-full rounded bg-emerald-500" style={{width:`${total?Math.min(100,Math.round(done/total*100)):0}%`}}/></div>
/** Everyone's progress as small chips: initial, name, a tick when finished or "2 of 4". */
const People = ({rows}:{rows:SetupPerson[]}) => <div className="flex flex-wrap gap-1.5">{rows.map(person=><span key={person.person_id} className="inline-flex max-w-full items-center gap-1.5 rounded-full border border-border py-0.5 pl-0.5 pr-2 text-[11.5px] text-muted-foreground"><span aria-hidden="true" className="grid h-[18px] w-[18px] shrink-0 place-items-center rounded-full bg-muted text-[10px] text-foreground">{person.name.trim().charAt(0).toUpperCase()}</span><span className="truncate">{person.name}</span>{finished(person)?<span className="text-emerald-500" aria-label="Done">✓</span>:<span>{progress(person)}</span>}</span>)}</div>
const needLabels:Record<string,string> = {added:'Added',connected:'Connected',installed:'Installed',missing:'Missing',unreported:'Not reported yet',unavailable:'Unknown'}
/** What a setup step needs, in the words a person uses: a key, a sign-in, an MCP tool or a skill. A key the Team holds is "Available" (nobody sees its value). */
const needKind = (need:SetupNeed) => need.kind==='mcp'?'MCP tool':need.kind==='skill'?'Skill':need.provider?'Sign-in':need.source==='team'?'Shared key':'Key'
const needLabel = (need:SetupNeed) => need.source==='team'&&need.state==='added'?'Available':needLabels[need.state]||'Unknown'
const needTone = (state:string) => state==='missing'?'border-amber-500/50 text-amber-500':state==='unreported'||state==='unavailable'?'border-border text-muted-foreground':'border-emerald-500/50 text-emerald-500'
const NeedPill = ({need}:{need:SetupNeed}) => <span className={`rounded-full border px-2 py-px text-[10.5px] ${needTone(need.state)}`}>{needLabel(need)}</span>
const button = 'rounded-lg border border-border bg-muted/40 px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-50'
const primary = 'rounded-lg bg-primary px-3 py-1.5 text-xs font-semibold text-primary-foreground hover:opacity-90 disabled:opacity-50'
const heading = 'mb-1.5 mt-3 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'

/** A relative Markdown reference means a file in the Team's shared files, next to the instructions (a leading "/" starts at the folder's top). */
const teamFilePath = (base:string,ref:string) => {
 if(!ref||/^([a-z][a-z0-9+.-]*:|\/\/|#)/i.test(ref)) return ''
 const out:string[]=[]
 try {
  for(const part of [...(ref.startsWith('/')?[]:base.split('/').slice(0,-1)),...ref.split(/[?#]/)[0].split('/')]) {
   if(!part||part==='.') continue
   if(part==='..'){if(!out.length) return '';out.pop()} else out.push(decodeURIComponent(part))
  }
 } catch { return '' }
 return out.join('/')
}
const readTeamFile = (target:TeamTarget,path:string) => teamRequest<Blob>(target,`/file?path=${encodeURIComponent(path)}`,'GET',undefined,'blob')
/** An image saved in Team files, fetched with the person's own access. */
function TeamImage({target,path,alt}:{target:TeamTarget;path:string;alt:string}) {
 const [url,setUrl]=useState('')
 const [failed,setFailed]=useState(false)
 useEffect(()=>{
  let active=true,made=''
  setUrl('');setFailed(false)
  readTeamFile(target,path).then(blob=>{if(active){made=URL.createObjectURL(blob);setUrl(made)}}).catch(()=>{if(active)setFailed(true)})
  return()=>{active=false;if(made)URL.revokeObjectURL(made)}
 },[target.project,target.connection,target.folderId,path])
 if(failed) return <span className="text-muted-foreground">[Image unavailable: {alt||path.split('/').pop()}]</span>
 return url?<img src={url} alt={alt} className="my-1.5 max-h-96 max-w-full rounded-lg border border-border"/>:<span className="text-muted-foreground">Loading image…</span>
}
/** Markdown for a setup's instructions: images show and file links download from the Team's shared files; other links open as usual. */
const teamMarkdown = (target:TeamTarget,base:string,fail:(message:string)=>void):Components => ({
 img:({src,alt})=>{const path=teamFilePath(base,String(src||''));return path?<TeamImage target={target} path={path} alt={alt||''}/>:<span className="text-muted-foreground">[{alt||'Image'}]</span>},
 a:({href,children})=>{
  const path=teamFilePath(base,href||'')
  if(!path) return <a href={href} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">{children}</a>
  return <button type="button" className="text-primary underline underline-offset-2" onClick={()=>{
   void readTeamFile(target,path).then(blob=>{const url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download=path.split('/').pop()||'file';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}).catch(()=>fail('That file is not available in the Team files.'))
  }}>{children}</button>
 },
})

/** One row per person, one column per thing the setup step needs: names and states only, never a value. */
const NeedsTable = ({needs,rows}:{needs:SetupNeed[];rows:SetupPerson[]}) => <section aria-label="Who has what">
 <h4 className={heading}>Who has what</h4>
 <div className="overflow-x-auto"><table className="w-full border-collapse text-left text-[12px]"><thead><tr><th className="py-1 pr-2 font-medium text-muted-foreground">Person</th>{needs.map(need=><th key={`${need.kind}:${need.name}:${need.provider}`} className="px-2 py-1 font-medium">{need.provider||need.name}<span className="block text-[10px] font-normal text-muted-foreground">{needKind(need)}</span></th>)}</tr></thead>
 <tbody>{rows.map(person=><tr key={person.person_id} className="border-t border-border"><td className="py-1.5 pr-2">{person.name}</td>{needs.map((need,i)=>{const state=person.needs?.[i];return <td key={`${need.kind}:${need.name}:${need.provider}`} className="px-2 py-1.5">{state?<NeedPill need={state}/>:null}</td>})}</tr>)}</tbody></table></div>
 <p className="mt-1 text-[11px] text-muted-foreground">Keys and sign-ins are read from each person's Vault. MCP tools and skills are what that person's own agent reported.</p>
</section>

/** Count the server's current checklist progress, never sign-ins or private setup details. */
const SetupPeople = ({rows}:{rows:SetupPerson[]}) => <section aria-label="Who is set up">
 <h4 className={heading}>Who is set up · {rows.filter(finished).length} of {rows.length}</h4>
 <People rows={rows}/>
</section>

/** No timer: the parent uses the existing visible Team refresh. Progress is never edited in the browser. */
export function TeamSetups({target,projectId,list,refreshKey,onOpen}:{target:TeamTarget;projectId:string;list?:TeamSetupList;refreshKey:number;onOpen?:(open:boolean)=>void}) {
 const [selected,setSelected]=useState('')
 // The Team kit shows only the open step while one is open (#3141).
 useEffect(()=>{onOpen?.(Boolean(selected))},[selected]) // eslint-disable-line react-hooks/exhaustive-deps
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
 const markdown=useMemo(()=>teamMarkdown(target,detail?.instruction.path||'',setError),[target.project,target.connection,target.folderId,detail?.instruction.path])
 const draft=(edit=false,theme=false)=>{
  const prompt=theme
   ? `Share my current personal theme as a Team setup called "Apply this theme" in Team ${target.project}. Use appearance operation=export (settings action=appearance over MCP) to export the real theme, save that JSON and Markdown instructions in the Team shared files, then team_sync setup_share (crew_team_sync over MCP, crew_id=${target.project}). The instructions must import the JSON using appearance operation=import, then apply that named theme to the applying person's own appearance. The agent verifies and fills the checklist; never claim it is applied just because it was imported. No keys or personal account data. Ask me if you cannot access my personal appearance.`
   : `${edit?`Edit Team setup ${selected}`:'Share this as a Team setup'} in Team ${target.project}. Use team_sync (crew_team_sync over MCP, crew_id=${target.project}). ${edit?'Read setup_get and current revisions first.':'Ask what instructions and checklist I want to share, and who it is for (everyone, or chosen people from team_sync people).'} Keep instructions in a Markdown file in the existing Team shared folder. Store only its path and revision with setup_share/setup_edit. Required keys are names only. If it includes a skill, save its files in the Team shared folder and link them from the instructions so each person's agent can install them; list each MCP or skill by name in the setup's keys with kind mcp or skill. A key or secret never goes in the setup or its files: add it once to the Team vault with way_add (the Team's way of working) and list only its name. Nothing installs by itself; people apply it with their own agent. People never tick boxes; their own agents verify and update setup_checklist.`
  if(!fillLastAgentDraft(projectId,prompt)) useChatStore.getState().addToast('Open one of your own agent chats in this project, then try again. Nothing was sent.','info')
 }
 const apply=async()=>{
  if(!detail||!agent||busy) return
  const identity=captureChatIdentity()
  const key=JSON.stringify([detail.id,detail.revision,detail.instruction_revision,agent])
  if(submission.current.key!==key) submission.current={key,id:randomUUID()}
  setBusy(true);setError('')
  try {
   const result=await teamRequest<{notice:string}>(target,'/checklists','POST',{action:'setup_apply',setup_id:detail.id,setup_revision:detail.revision,instruction_revision:detail.instruction_revision,target_agent_id:agent,submission_id:submission.current.id})
   if(alive.current&&isChatIdentityCurrent(identity)) setNotice(result.notice||'Queued for your agent. Its reply will arrive in your inbox. The checklist changes only after it checks the work.')
  } catch(e) {if(alive.current&&isChatIdentityCurrent(identity)) setError(e instanceof Error?e.message:'Could not hand this setup to your agent. Try again.')}
  finally {if(alive.current&&isChatIdentityCurrent(identity)) setBusy(false)}
 }
 return <div className="space-y-3 text-xs">
  {selected?<>
   <button type="button" className="text-primary" disabled={busy} onClick={()=>{setSelected('');setError('');setNotice('')}}>‹ Team kit</button>
   {!detail&&!error&&<p>Loading…</p>}
   {detail&&<>
    <div><h3 className="text-base font-semibold">{detail.title}</h3><p className="text-muted-foreground">Shared by {detail.shared_by} · for {detail.assigned_to.length?detail.assigned_to.join(', '):'everyone'}{detail.for_you&&` · you: ${progress(detail)}`}</p>{detail.for_you&&<Bar done={detail.done} total={detail.total}/>}</div>
    <h4 className={heading}>What to do</h4><div className="break-words rounded-xl border border-border bg-muted/30 p-3"><PlainMarkdown content={detail.instructions} components={markdown}/></div>
    <h4 className={heading}>Checklist · your agent ticks these</h4>
    <ul className="divide-y divide-border">{detail.items.map(item=><li key={item.id} className="flex items-start gap-2.5 py-1.5"><span aria-label={detail.my_items[item.id]?'Done':'Not checked'} className={`mt-0.5 grid h-4 w-4 shrink-0 place-items-center rounded border text-[11px] ${detail.my_items[item.id]?'border-emerald-500 bg-emerald-500 text-background':'border-muted-foreground/60'}`}>{detail.my_items[item.id]?'✓':''}</span><span className="text-[13px]">{item.label}</span></li>)}</ul>
    {!!detail.key_status.length&&<section aria-label="Needed keys"><h4 className={heading}>Needed keys, sign-ins, tools and skills</h4>{detail.key_status.map(need=><p key={`${need.kind}:${need.name}:${need.provider}`} className="flex items-center gap-2 py-0.5 text-[13px]"><span>{need.name}{need.provider?` · ${need.provider}`:''}</span><span className="text-[10.5px] text-muted-foreground">{needKind(need)}</span><NeedPill need={need}/>{need.source==='team'&&<span className="text-muted-foreground">Team (server only)</span>}</p>)}</section>}
    {!detail.for_you?<div className="rounded-xl border border-border p-3"><p className="text-muted-foreground">This is assigned to other people, so there is nothing for you to do.</p><button type="button" className="mt-2 text-muted-foreground underline" onClick={()=>draft(true)}>Edit from chat</button></div>:<div className="rounded-xl border border-border p-3">
     <label className="block">Your connected agent<select aria-label="Your connected agent" className="mt-1 w-full rounded-lg border border-border bg-background p-1.5" value={agent} disabled={busy} onChange={e=>{setAgent(e.target.value);setNotice('')}}><option value="">Choose your agent</option>{targets.filter(row=>row.online).map(row=><option key={row.id} value={row.id}>{row.name}</option>)}</select></label>
     {!targets.some(row=>row.online)&&<p className="mt-1.5 text-muted-foreground">None of your agents in this project is connected. Open one of your agent chats in this project to apply this.</p>}
     <div className="mt-2.5 flex items-center gap-2"><button type="button" className={primary} disabled={!agent||busy} onClick={()=>void apply()}>{busy?'Handing to your agent…':'Apply with my agent'}</button><button type="button" className="text-muted-foreground underline disabled:opacity-50" disabled={busy} onClick={()=>draft(true)}>Edit from chat</button></div>
    </div>}
    <SetupPeople rows={detail.people}/>
    {!!detail.key_status.length&&detail.people.some(person=>person.needs?.length)&&<NeedsTable needs={detail.key_status} rows={detail.people}/>}
   </>}
  </>:<>
   {!list?<p>Loading setup steps…</p>:!list.setups.length?<div className="rounded-xl border border-dashed border-border p-5 text-center"><p className="mb-1 text-sm font-semibold">No setup steps yet</p><p className="text-muted-foreground">Add instructions and a checklist, and each teammate’s agent does it for them.</p></div>:<ul className="space-y-2.5">{list.setups.map(row=><li key={row.id} className="rounded-xl border border-border bg-muted/20 p-3">
    <button type="button" className="block w-full text-left" aria-label={`Open ${row.title}`} onClick={()=>{setSelected(row.id);setDetail(undefined);setError('');setNotice('')}}>
     <span className="flex items-center gap-2"><span className="text-sm font-semibold">{row.title}</span><span className="ml-auto text-muted-foreground">{!row.for_you?'Not for you':finished(row)?'Done ✓':`you: ${progress(row)}`}</span></span>
     <span className="block text-muted-foreground">{row.shared_by?`Shared by ${row.shared_by} · `:''}For {row.assigned_to.length?row.assigned_to.join(', '):'everyone'}</span>
     {row.for_you&&<Bar done={row.done} total={row.total}/>}
     {!row.instructions_available&&<span className="mb-1.5 block text-amber-500">Instructions unavailable, ask the sharer</span>}
    </button>
    <SetupPeople rows={row.people}/>
   </li>)}</ul>}
   <div className="flex flex-wrap items-center gap-2"><button type="button" className={button} onClick={()=>draft()}>+ Add a setup step</button><button type="button" className="text-muted-foreground underline" onClick={()=>draft(false,true)}>Share my theme</button></div>
  </>}
  {notice&&<p role="status" className="text-muted-foreground">{notice}</p>}{error&&<p role="alert" className="text-destructive">{error}</p>}
 </div>
}
