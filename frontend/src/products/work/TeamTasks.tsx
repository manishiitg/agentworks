import { useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'

/** One task as the server sends it (#2965): a request thread, linked to its issue (group) and parent task. */
export type TeamTask = {
 id:string; parent?:string; group?:string; group_title?:string; title:string
 state:'open'|'in_progress'|'blocked'|'needs_you'|'stalled'|'done'
 agent?:string; agent_key?:string; person?:string; mine?:boolean; for_me?:boolean; cli?:string
 asked_by?:string; line?:string; proof?:string; at:string; bridge?:{thread_id:string;can_open:boolean}
}

function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}
const newest=(a:{at:string},b:{at:string})=>Date.parse(b.at)-Date.parse(a.at)
const mark=(task:TeamTask)=>({done:'✓',in_progress:'●',needs_you:'◐',blocked:'◐',stalled:'!',open:'○'})[task.state]
const tone=(task:TeamTask)=>task.state==='done'?'text-muted-foreground':task.state==='in_progress'?'text-emerald-600 dark:text-emerald-400':task.state==='open'?'text-muted-foreground':'text-amber-600 dark:text-amber-400'
const who=(task:TeamTask)=>task.agent?`${task.mine?'your ':task.person?`${task.person}'s `:''}${task.agent}`:task.person?'':'not taken'
function status(task:TeamTask) {
 if(task.state==='needs_you')return task.for_me?'needs you':task.agent?'needs its person':`needs ${task.person||'a person'}`
 return ({open:'open',in_progress:'in progress',blocked:'blocked',stalled:'stalled',done:'done'})[task.state]
}
const proofLink=(proof?:string)=>proof?.startsWith('https://')?<a href={proof} target="_blank" rel="noreferrer" onClick={e=>e.stopPropagation()} className="text-primary hover:underline">{proof.replace(/^https:\/\/github\.com\/[^/]+\/[^/]+\//,'')}</a>:proof

/** Tasks and Agents: the same server list shown two ways. Clicking a task opens its thread when the server allows it. */
export function TeamTasks({tasks,mode,thread}:{tasks:TeamTask[];mode:'tasks'|'agents';thread:(id:string)=>ReactNode}) {
 const [open,setOpen]=useState<Record<string,boolean>>({})
 const [shown,setShown]=useState('')
 const toggle=(key:string,initial:boolean)=>setOpen(current=>({...current,[key]:!(current[key]??initial)}))
 const children=new Map<string,TeamTask[]>()
 for(const task of tasks)if(task.parent)children.set(task.parent,[...(children.get(task.parent)||[]),task])
 const row=(task:TeamTask,depth:number,flat=false):ReactNode=>{
  const kids=(children.get(task.id)||[]).sort(newest), key=`task:${task.id}`, expanded=open[key]??depth<2
  const canOpen=!!task.bridge?.can_open
  return <li key={task.id}>
   <div className="flex min-w-0 items-start gap-1.5 py-1" style={{paddingLeft:depth*16}}>
    {kids.length&&!flat?<button type="button" aria-label={expanded?`Hide sub-tasks of ${task.title}`:`Show sub-tasks of ${task.title}`} aria-expanded={expanded} onClick={()=>toggle(key,depth<2)} className="mt-0.5 shrink-0 text-muted-foreground"><ChevronRight aria-hidden="true" className={`h-3 w-3 ${expanded?'rotate-90':''}`}/></button>:<span className="w-3 shrink-0"/>}
    <span aria-hidden="true" className={`shrink-0 ${tone(task)}`}>{mark(task)}</span>
    <button type="button" disabled={!canOpen} onClick={()=>setShown(shown===task.id?'':task.id)} className="min-w-0 flex-1 text-left enabled:hover:text-primary disabled:cursor-default">
     <span className="block truncate text-xs">{flat&&task.group&&<span className="mr-1 rounded bg-primary/10 px-1 text-[10px] font-semibold text-primary">{task.group}</span>}<CodingCliIcon provider={task.cli} className="mr-1 inline-flex h-3.5 w-3.5 items-center align-[-2px]"/>{task.title}</span>
     <span className="block truncate text-[11px] text-muted-foreground">{[who(task),status(task),task.line].filter(Boolean).join(' · ')}</span>
    </button>
    <span className="shrink-0 text-right text-[10px] text-muted-foreground">{proofLink(task.proof)}{task.proof&&' · '}{age(task.at)}</span>
   </div>
   {shown===task.id&&canOpen&&<div style={{paddingLeft:depth*16+20}}>{thread(task.bridge!.thread_id)}</div>}
   {expanded&&!flat&&kids.length>0&&<ol>{kids.map(kid=>row(kid,depth+1))}</ol>}
  </li>
 }
 if(!tasks.length)return <p className="text-xs text-muted-foreground">No tasks yet. Agents add one each time they ask an agent, a person or the Team.</p>
 if(mode==='tasks'){
  const top=tasks.filter(task=>!task.parent)
  const groups=new Map<string,TeamTask[]>()
  for(const task of top)if(task.group)groups.set(task.group,[...(groups.get(task.group)||[]),task])
  const latest=(list:TeamTask[])=>list.reduce((at,task)=>Date.parse(task.at)>at?Date.parse(task.at):at,0)
  const entries:[number,ReactNode][]=[]
  for(const [group,list] of groups){
   const all=tasks.filter(task=>task.group===group), done=all.filter(task=>task.state==='done').length, needs=all.filter(task=>task.state==='needs_you'&&task.for_me).length
   const agents=[...new Set(all.map(task=>task.agent).filter(Boolean))].slice(0,3).join(', ')
   const key=`group:${group}`, initial=done<all.length, expanded=open[key]??initial
   entries.push([latest(all),<li key={key} className="rounded border border-border/60 px-1">
    <button type="button" aria-expanded={expanded} onClick={()=>toggle(key,initial)} className="flex w-full min-w-0 items-center gap-1.5 py-1 text-left">
     <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 ${expanded?'rotate-90':''}`}/>
     <span className="shrink-0 rounded bg-primary/10 px-1 text-[10px] font-semibold text-primary">{group}</span>
     <span className="min-w-0 flex-1 truncate text-xs font-medium">{list[0]?.group_title}</span>
     <span className="shrink-0 text-[10px] text-muted-foreground">{done===all.length?'done':`${done} of ${all.length} done`}{needs>0&&<span className="text-amber-600 dark:text-amber-400"> · {needs} needs you</span>}{agents&&` · ${agents}`}</span>
    </button>
    {expanded&&<ol className="pb-1">{[...list].sort(newest).map(task=>row(task,1))}</ol>}
   </li>])
  }
  for(const task of top)if(!task.group)entries.push([Date.parse(task.at),<ol key={task.id}>{row(task,0)}</ol>])
  return <ol aria-label="Tasks" className="space-y-1">{entries.sort((a,b)=>b[0]-a[0]).map(([,node])=>node)}</ol>
 }
 const byAgent=new Map<string,TeamTask[]>()
 for(const task of tasks){const key=task.agent_key||(task.agent?`name:${task.agent}`:task.person?`person:${task.person}`:'none');byAgent.set(key,[...(byAgent.get(key)||[]),task])}
 const agents=[...byAgent.entries()].map(([key,list])=>({key,list:list.sort(newest),doing:list.find(task=>task.state==='in_progress')})).sort((a,b)=>Number(!!b.doing)-Number(!!a.doing)||newest(a.list[0],b.list[0]))
 return <ol aria-label="Agents" className="space-y-1">{agents.map(({key,list,doing})=>{
  const first=list[0], expanded=open[key]??!!doing, today=list.filter(task=>task.state==='done'&&new Date(task.at).toDateString()===new Date().toDateString()).length
  const name=key==='none'?'Not taken yet':first.agent?`${first.mine?'Your ':first.person?`${first.person}'s `:''}${first.agent}`:first.person||''
  return <li key={key} className="rounded border border-border/60 px-1">
   <button type="button" aria-expanded={expanded} onClick={()=>toggle(key,!!doing)} className="flex w-full min-w-0 items-center gap-1.5 py-1 text-left">
    <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 ${expanded?'rotate-90':''}`}/>
    {key!=='none'&&<CodingCliIcon provider={first.cli} className="inline-flex h-3.5 w-3.5 shrink-0 items-center"/>}
    <span className="shrink-0 text-xs font-medium">{name}</span>
    <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">{doing?`doing: ${doing.title}`:key==='none'?`${list.length} open`:'idle'}{today>0&&` · ${today} done today`}</span>
   </button>
   {expanded&&<ol className="pb-1">{list.map(task=>row(task,1,true))}</ol>}
  </li>
 })}</ol>
}
