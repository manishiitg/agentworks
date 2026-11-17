import { useEffect, useRef, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { teamError, type TeamTabState } from './teamApi'
import { openTeamActivity, useTeamActivity, type TeamNeed, type TeamNeedAnswer } from './TeamActivity'
import type { TeamPanelEvent } from './TeamPanel'

const activityOpen=(scope:string)=>{try{return localStorage.getItem(`agentworks.team-activity:${scope}`)!=='minimized'}catch{return true}}
const time = (at?:string) => at ? Date.parse(at) || 0 : 0
function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}

/**
 * Activity, with the Needs-you cards pinned above it, as its own panel section (owner, 16 Nov: "just happening
 * can happen outside team also"). With the Team on it is the Team's feed; with the Team off, the server returns the
 * person's own agents' work log. The complete list opens on the Team's Activity page in the main pane.
 */
export function CrewActivitySection({projectId,scope,team,onNeedsCount}:{projectId:string;scope:string;team:TeamTabState;onNeedsCount?:(count:number)=>void}) {
 const on=!!team.view?.team.on&&!!team.target&&!team.error
 // Team off: this app's own server holds the person's work log, so ask it directly (no Team connection).
 const target=on?team.target:projectId?{project:projectId}:undefined
 const activity=useTeamActivity(target,projectId,!!target&&!team.error)
 const needs=on?activity.view?.needs||[]:[]
 const needsCount=needs.filter(need=>need.for_me).length
 useEffect(()=>{onNeedsCount?.(needsCount)},[onNeedsCount,needsCount])
 if(!target||(!activity.view&&!on))return null
 return <ActivityList scope={scope} events={activity.view?.feed||[]} collapsedItem={activity.view?.collapsed_item} needs={needs} onAnswer={activity.answer}
  onActivitySeen={on?activity.markSeen:undefined} onAll={on?()=>openTeamActivity(projectId):undefined} onEvent={on?item=>openTeamActivity(projectId,item.bridge?.can_open?item.id:undefined):undefined}/>
}

/** The section itself; the server decides what is in it, who may answer a card and what is new. */
export function ActivityList({scope,events,collapsedItem,needs=[],onAnswer,onActivitySeen,onEvent,onAll}:{
 scope:string; events:TeamPanelEvent[]; collapsedItem?:TeamPanelEvent|null
 needs?:TeamNeed[]; onAnswer?:(need:string,button:string,helper?:string)=>Promise<TeamNeedAnswer>
 onActivitySeen?:(through:string)=>void; onEvent?:(event:TeamPanelEvent)=>void; onAll?:()=>void
}) {
 const [recentState,setRecentState]=useState({scope,open:activityOpen(scope)})
 const recentOpen=recentState.scope===scope?recentState.open:activityOpen(scope)
 const [shown,setShown]=useState({scope,count:4})
 const count=shown.scope===scope?shown.count:4
 const seen=useRef('')
 const toggleRecent=()=>{const open=!recentOpen;setRecentState({scope,open});try{localStorage.setItem(`agentworks.team-activity:${scope}`,open?'open':'minimized')}catch{/* Optional view preference. */}}
 const all=events.filter(item=>!item.private&&item.kind!=='question').sort((a,b)=>time(b.at)-time(a.at))
 const recent=all.slice(0,count)
 // The server labels each item with its work-log task; items about the same task show together under it.
 const groups=recent.reduce<{task?:string;title?:string;state?:string;items:TeamPanelEvent[]}[]>((out,item)=>{const group=item.task?out.find(g=>g.task===item.task):undefined;if(group)group.items.push(item);else out.push({task:item.task,title:item.task_title,state:item.task_state,items:[item]});return out},[])
 useEffect(()=>{
  const id=recent[0]?.id,key=`${scope}:${id}`
  if(recentOpen&&id&&seen.current!==key&&onActivitySeen){seen.current=key;onActivitySeen(id)}
 },[scope,recentOpen,recent[0]?.id,onActivitySeen])
 const body=(item:TeamPanelEvent)=><><span aria-hidden="true" className="shrink-0 text-primary">{item.kind==='message'?'↗':item.kind==='decision'?'◆':item.kind==='finding'?'●':item.kind==='task'&&item.verb==='started'?'▸':item.kind==='task'&&item.verb==='added'?'+':'✓'}</span><span className="min-w-0 flex-1"><span className="block truncate font-medium">{item.unseen&&<span aria-label="Unseen" className="mr-1 inline-block h-1 w-1 rounded-full bg-primary"/>}{item.title} {item.verb}</span>{item.line&&<span className="block truncate text-muted-foreground">{item.line}</span>}</span><span className="shrink-0 text-[10px] text-muted-foreground">{age(item.at)}</span></>
 const row='flex w-full min-w-0 items-start gap-1.5 rounded px-1 py-1 text-left'
 const recentItem=(item:TeamPanelEvent)=><li key={item.id}>{onEvent?<button type="button" title={`${item.title} ${item.verb} ${item.line||''}`} onClick={()=>onEvent(item)} className={`${row} hover:bg-background/40`}>{body(item)}</button>:<div title={`${item.title} ${item.verb} ${item.line||''}`} className={row}>{body(item)}</div>}</li>
 return <div className="space-y-1 text-[11px]">
  {needs.length>0&&<section aria-label="Needs you" className="mt-1 border-t border-border pt-1"><ol className="space-y-1">{needs.map(need=><NeedCard key={need.id} need={need} onAnswer={onAnswer}/>)}</ol></section>}
  <section aria-label="Activity" className="mt-1 border-t border-border pt-1">
   <div className="flex min-w-0 items-center gap-1 px-1"><button type="button" aria-label="Toggle Activity" aria-expanded={recentOpen} onClick={toggleRecent} className="flex h-6 min-w-0 flex-1 items-center gap-1 text-left text-[10px] text-muted-foreground"><ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 ${recentOpen?'rotate-90':''}`}/>{recentOpen?<span>Activity</span>:<span className="min-w-0 truncate">{collapsedItem?`${collapsedItem.title} ${collapsedItem.verb} ${collapsedItem.line||''}`:'Activity · Nothing new'}</span>}</button>{recentOpen&&onAll&&<button type="button" onClick={onAll} className="shrink-0 text-[10px] text-primary">All ›</button>}</div>
   {recentOpen&&<><ol>{groups.map(group=>group.task?<li key={`task:${group.task}`} className="mb-1 rounded border border-border/60"><div className="flex min-w-0 items-center gap-1 px-1 pt-1 text-[10px]"><span className="shrink-0 rounded bg-primary/10 px-1 font-semibold text-primary">{group.task}</span><span className="min-w-0 flex-1 truncate font-medium">{group.title}</span>{group.state&&<span className="shrink-0 text-muted-foreground">{group.state}</span>}</div><ol>{group.items.map(recentItem)}</ol></li>:recentItem(group.items[0]))}</ol>
    {all.length>recent.length&&<button type="button" onClick={()=>setShown({scope,count:count+10})} className="px-1 text-[11px] text-primary">▸ Show more</button>}
    {!recent.length&&<p className="px-1 text-[10px] text-muted-foreground">Nothing new</p>}</>}
  </section>
 </div>
}

/** One pinned Needs-you card. Only the person asked gets buttons; everyone else sees who it waits on. */
export function NeedCard({need,onAnswer}:{need:TeamNeed;onAnswer?:(need:string,button:string,helper?:string)=>Promise<TeamNeedAnswer>}) {
 const [busy,setBusy]=useState('')
 const [note,setNote]=useState('')
 const [choose,setChoose]=useState<TeamNeedAnswer['choose_agent']>()
 const click=async(button:string,helper?:string)=>{
  if(!onAnswer)return
  setBusy(button);setNote('')
  try {const out=await onAnswer(need.id,button,helper);setChoose(out.choose_agent);if(!out.choose_agent)setNote(out.note||'')}
  catch(e){setNote(teamError(e))}
  finally{setBusy('')}
 }
 return <li className="rounded border border-amber-500/50 bg-amber-500/10 px-1.5 py-1 text-[11px]">
  <div className="flex min-w-0 items-center gap-1 text-[10px]"><span className="shrink-0 font-semibold text-amber-700 dark:text-amber-300">{need.for_me?'Needs you':`Needs ${need.for}`}</span><span className="min-w-0 truncate text-muted-foreground">· {need.person}'s {need.agent} · {age(need.at)}</span></div>
  {need.task&&<div className="mt-0.5 flex min-w-0 items-center gap-1 text-[10px]"><span className="shrink-0 rounded bg-primary/10 px-1 font-semibold text-primary">{need.task}</span><span className="min-w-0 truncate">{need.task_title}</span></div>}
  <p className="mt-0.5 break-words">{need.line}</p>
  {need.link&&<a href={need.link} target="_blank" rel="noreferrer" className="text-[10px] text-primary">Open link ›</a>}
  {need.for_me?<div className="mt-1 flex flex-wrap gap-1">{need.buttons.map(button=><button key={button} type="button" disabled={!!busy} onClick={()=>void click(button)} className="rounded border border-border bg-background px-1.5 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50">{busy===button?'Sending…':button}</button>)}</div>
   :<p className="mt-0.5 text-[10px] text-muted-foreground">Waiting for {need.for}</p>}
  {choose&&<div className="mt-1 text-[10px]"><p className="text-muted-foreground">Which of your agents should handle requests like this? It's remembered.</p><div className="mt-0.5 flex flex-wrap gap-1">{choose.map(agent=><button key={agent.id} type="button" disabled={!!busy} onClick={()=>void click('Ask my agent',agent.id)} className="rounded border border-border bg-background px-1.5 py-0.5 hover:bg-muted disabled:opacity-50">{agent.name}{!agent.online&&' (offline)'}</button>)}</div></div>}
  {note&&<p className="mt-0.5 text-[10px] text-muted-foreground">{note}</p>}
 </li>
}
