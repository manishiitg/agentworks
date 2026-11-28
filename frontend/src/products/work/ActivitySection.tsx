import { useEffect, useState } from 'react'
import { teamError, type TeamTabState } from './teamApi'
import { useTeamActivity, type TeamNeed, type TeamNeedAnswer } from './TeamActivity'
import { TaskAttachments, type TaskFile, type TaskFileContent } from './TaskAttachments'
import { RunProblemCardView, useRunProblems } from './RunProblems'
import { formatRelativeIso } from '../../utils/relativeTime'

/**
 * The side panel's Needs-you box (owner, 23 Nov, reversing 19 Nov): at the very top, only while something waits on
 * the person. The newest card is open; the rest fold under "Show N more". It also feeds the Team row's count. The same
 * cards stay in the Team monitor; answering in either clears both. Cards come from the Team, so with the Team off
 * there are none.
 */
export function CrewActivitySection({projectId,team,onNeedsCount,onAsk,enabled=true}:{projectId:string;team:TeamTabState;onNeedsCount?:(count:number)=>void;onAsk?:(message:string)=>void;enabled?:boolean}) {
 const on=enabled&&!!team.view?.team.on&&!!team.target&&!team.error
 const activity=useTeamActivity(on?team.target:undefined,projectId,on)
 const mine=(on?activity.view?.needs||[]:[]).filter(need=>need.for_me)
 // Missed and failed workflow runs are the owner's own cards, with or without a Team (#3617).
 const runs=useRunProblems(projectId,enabled)
 const waiting=mine.length+runs.cards.length
 useEffect(()=>{onNeedsCount?.(waiting)},[onNeedsCount,waiting])
 const [open,setOpen]=useState(true)
 const [all,setAll]=useState(false)
 if(!waiting)return null
 const shown=all?mine:mine.slice(0,1)
 return <section aria-label="Needs you" className="mb-2 overflow-hidden rounded-lg border border-amber-500/50 bg-amber-50/60 text-xs dark:bg-amber-500/5">
  <button type="button" aria-expanded={open} onClick={()=>setOpen(!open)} className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-xs font-semibold text-amber-800 dark:text-amber-300">
   <span aria-hidden>⚠</span><span>Needs you</span><span className="ml-auto text-[11px] font-normal text-amber-700/80 dark:text-amber-300/80">{waiting>1&&`${waiting} waiting `}{open?'▾':'▸'}</span>
  </button>
  {open&&<><ol className="divide-y divide-amber-500/20 border-t border-amber-500/20">{runs.cards.map(card=><RunProblemCardView key={card.id} card={card} press={runs.press} onAsk={onAsk}/>)}{shown.map(need=><NeedCard key={need.id} need={need} onAnswer={activity.answer} openFile={activity.openFile}/>)}</ol>
   {mine.length>1&&!all&&<button type="button" onClick={()=>setAll(true)} className="w-full border-t border-amber-500/20 py-1 text-[11px] text-muted-foreground hover:text-foreground">Show {mine.length-1} more</button>}</>}
 </section>
}

export type NeedAnswer=(need:string,button:string,helper?:string,comment?:string)=>Promise<TeamNeedAnswer>
export type NeedFile=(need:string,file:TaskFile)=>Promise<TaskFileContent>

/** One pinned Needs-you card: who asks, the question in full, then the answer buttons (the first is the main one) and an
 * optional note behind "Add a note". Only the person asked gets buttons; everyone else sees who it waits on. Its links
 * and files (a PR, a plan .md) open on the card. */
export function NeedCard({need,onAnswer,openFile}:{need:TeamNeed;onAnswer?:NeedAnswer;openFile?:NeedFile}) {
 const [busy,setBusy]=useState('')
 const [note,setNote]=useState('')
 const [comment,setComment]=useState('')
 const [noting,setNoting]=useState(false)
 const [full,setFull]=useState(false)
 const [choose,setChoose]=useState<TeamNeedAnswer['choose_agent']>()
 const click=async(button:string,helper?:string)=>{
  if(!onAnswer)return
  setBusy(button||'note');setNote('')
  try {const out=await onAnswer(need.id,button,helper,comment.trim()||undefined);setChoose(out.choose_agent);if(!out.choose_agent){setNote(out.note||'');setComment('');setNoting(false)}}
  catch(e){setNote(teamError(e))}
  finally{setBusy('')}
 }
 const long=(need.message?.length||0)>280
 const answering=need.for_me&&!!onAnswer
 const button='rounded-md px-2.5 py-1 text-xs font-medium disabled:opacity-50'
 return <li className="px-2.5 py-2 text-xs">
  <div className="flex min-w-0 items-center gap-1 text-[11px] text-muted-foreground"><span className="min-w-0 truncate">{need.mine?'Your':`${need.person}'s`} <span className="font-medium text-foreground/80">{need.agent}</span> asks{!need.for_me&&need.for?` ${need.for}`:''}</span><span className="shrink-0">· {formatRelativeIso(need.at)}</span></div>
  {need.task&&<div className="mt-1 flex min-w-0 items-center gap-1 text-[11px]"><span className="shrink-0 rounded bg-primary/10 px-1 font-semibold text-primary">{need.task}</span><span className="min-w-0 truncate">{need.task_title}</span></div>}
  <p className="mt-1 break-words text-[13px] font-semibold leading-snug text-foreground">{need.line}</p>
  {need.message&&<><p className={`mt-1 whitespace-pre-line break-words leading-relaxed text-foreground/80 ${long&&!full?'line-clamp-5':''}`}>{need.message}</p>
   {long&&<button type="button" onClick={()=>setFull(!full)} className="mt-0.5 text-[11px] text-primary hover:underline">{full?'Show less':'Show all'}</button>}</>}
  {need.links||need.files?<div className="mt-1"><TaskAttachments links={need.links} files={need.files} open={openFile&&(file=>openFile(need.id,file))}/></div>
   :need.link&&<a href={need.link} target="_blank" rel="noreferrer" className="mt-1 inline-block text-[11px] text-primary">Open link ›</a>}
  {answering?<>
   {noting&&<textarea aria-label="Note with your answer" autoFocus rows={2} value={comment} onChange={e=>setComment(e.target.value)} maxLength={4000} placeholder="Add a note, sent with your answer" className="mt-2 w-full resize-y rounded-md border border-border bg-background px-2 py-1 text-xs focus:outline-none focus:ring-1 focus:ring-primary/50"/>}
   <div className="mt-2 flex flex-wrap items-center gap-1.5">
    {need.buttons.map((label,i)=><button key={label} type="button" title={label==='Ask my agent'?`Hand this to one of your own agents: it handles it and answers ${need.agent} for you`:undefined} disabled={!!busy} onClick={()=>void click(label)} className={`${button} ${i===0?'bg-primary text-primary-foreground hover:bg-primary/90':'border border-border bg-background hover:bg-muted'}`}>{busy===label?'Sending…':label}</button>)}
    {noting&&comment.trim()&&<button type="button" disabled={!!busy} onClick={()=>void click('')} className={`${button} border border-primary/50 bg-background text-primary hover:bg-muted`}>{busy==='note'?'Sending…':'Send note only'}</button>}
    {!noting&&<button type="button" onClick={()=>setNoting(true)} className="ml-auto text-[11px] text-muted-foreground hover:text-foreground">Add a note</button>}
   </div></>
   :!need.for_me&&<p className="mt-1 text-[11px] text-muted-foreground">Waiting for {need.for}</p>}
  {choose&&<div className="mt-2 text-[11px]"><p className="text-muted-foreground">Which of your agents should handle requests like this? It's remembered.</p><div className="mt-1 flex flex-wrap gap-1.5">{choose.map(agent=><button key={agent.id} type="button" disabled={!!busy} onClick={()=>void click('Ask my agent',agent.id)} className={`${button} border border-border bg-background hover:bg-muted`}>{agent.name}{!agent.online&&' (offline)'}</button>)}</div></div>}
  {note&&<p className="mt-1 text-[11px] text-muted-foreground">{note}</p>}
 </li>
}
