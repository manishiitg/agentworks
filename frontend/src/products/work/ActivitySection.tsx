import { useEffect, useState } from 'react'
import { teamError, type TeamTabState } from './teamApi'
import { useTeamActivity, type TeamNeed, type TeamNeedAnswer } from './TeamActivity'
import { TaskAttachments, type TaskFile, type TaskFileContent } from './TeamTasks'

function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}

/**
 * The right menu's Needs-you cards: what agents are waiting on a person for (an approval, a decision). Only the cards;
 * the recent-activity list left this menu with features (owner, 19 Nov: tracking lives in the project's own issue
 * tracker). Cards come from the Team, so with the Team off there are none.
 */
export function CrewActivitySection({projectId,team,onNeedsCount}:{projectId:string;team:TeamTabState;onNeedsCount?:(count:number)=>void}) {
 const on=!!team.view?.team.on&&!!team.target&&!team.error
 const activity=useTeamActivity(on?team.target:undefined,projectId,on)
 const needs=on?activity.view?.needs||[]:[]
 const needsCount=needs.filter(need=>need.for_me).length
 useEffect(()=>{onNeedsCount?.(needsCount)},[onNeedsCount,needsCount])
 return <NeedsList needs={needs} onAnswer={activity.answer} openFile={activity.openFile}/>
}

/** The pinned cards; the server decides what is in them and who may answer. */
export function NeedsList({needs,onAnswer,openFile}:{needs:TeamNeed[];onAnswer?:NeedAnswer;openFile?:NeedFile}) {
 if(!needs.length)return null
 return <section aria-label="Needs you" className="mt-1 border-t border-border pt-1 text-[11px]"><ol className="space-y-1">{needs.map(need=><NeedCard key={need.id} need={need} onAnswer={onAnswer} openFile={openFile}/>)}</ol></section>
}

export type NeedAnswer=(need:string,button:string,helper?:string,comment?:string)=>Promise<TeamNeedAnswer>
export type NeedFile=(need:string,file:TaskFile)=>Promise<TaskFileContent>

/** One pinned Needs-you card. Only the person asked gets buttons and a note box (a review's "why");
 * everyone else sees who it waits on. Its links and files (a PR, a plan .md) open on the card. */
export function NeedCard({need,onAnswer,openFile}:{need:TeamNeed;onAnswer?:NeedAnswer;openFile?:NeedFile}) {
 const [busy,setBusy]=useState('')
 const [note,setNote]=useState('')
 const [comment,setComment]=useState('')
 const [choose,setChoose]=useState<TeamNeedAnswer['choose_agent']>()
 const click=async(button:string,helper?:string)=>{
  if(!onAnswer)return
  setBusy(button||'note');setNote('')
  try {const out=await onAnswer(need.id,button,helper,comment.trim()||undefined);setChoose(out.choose_agent);if(!out.choose_agent){setNote(out.note||'');setComment('')}}
  catch(e){setNote(teamError(e))}
  finally{setBusy('')}
 }
 return <li className="rounded border border-amber-500/50 bg-amber-500/10 px-1.5 py-1 text-[11px]">
  <div className="flex min-w-0 items-center gap-1 text-[10px]"><span className="shrink-0 font-semibold text-amber-700 dark:text-amber-300">{need.for_me?'Needs you':`Needs ${need.for}`}</span><span className="min-w-0 truncate text-muted-foreground">· {need.person}'s {need.agent} · {age(need.at)}</span></div>
  {need.task&&<div className="mt-0.5 flex min-w-0 items-center gap-1 text-[10px]"><span className="shrink-0 rounded bg-primary/10 px-1 font-semibold text-primary">{need.task}</span><span className="min-w-0 truncate">{need.task_title}</span></div>}
  <p className="mt-0.5 break-words">{need.line}</p>
  {need.links||need.files?<TaskAttachments links={need.links} files={need.files} open={openFile&&(file=>openFile(need.id,file))}/>
   :need.link&&<a href={need.link} target="_blank" rel="noreferrer" className="text-[10px] text-primary">Open link ›</a>}
  {need.for_me&&onAnswer&&<textarea aria-label="Note with your answer" rows={comment?3:1} value={comment} onChange={e=>setComment(e.target.value)} maxLength={4000} placeholder="Add a note (optional)" className="mt-1 w-full resize-y rounded border border-border bg-background px-1 py-0.5 text-[11px]"/>}
  {need.for_me?<div className="mt-1 flex flex-wrap gap-1">{comment.trim()&&<button type="button" disabled={!!busy} onClick={()=>void click('')} className="rounded border border-primary/50 bg-background px-1.5 py-0.5 text-[10px] text-primary hover:bg-muted disabled:opacity-50">{busy==='note'?'Sending…':'Send note'}</button>}{need.buttons.map(button=><button key={button} type="button" disabled={!!busy} onClick={()=>void click(button)} className="rounded border border-border bg-background px-1.5 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50">{busy===button?'Sending…':button}</button>)}</div>
   :<p className="mt-0.5 text-[10px] text-muted-foreground">Waiting for {need.for}</p>}
  {choose&&<div className="mt-1 text-[10px]"><p className="text-muted-foreground">Which of your agents should handle requests like this? It's remembered.</p><div className="mt-0.5 flex flex-wrap gap-1">{choose.map(agent=><button key={agent.id} type="button" disabled={!!busy} onClick={()=>void click('Ask my agent',agent.id)} className="rounded border border-border bg-background px-1.5 py-0.5 hover:bg-muted disabled:opacity-50">{agent.name}{!agent.online&&' (offline)'}</button>)}</div></div>}
  {note&&<p className="mt-0.5 text-[10px] text-muted-foreground">{note}</p>}
 </li>
}
