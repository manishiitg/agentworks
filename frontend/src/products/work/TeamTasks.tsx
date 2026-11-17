import { useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { PlainMarkdown } from '../../components/ui/PlainMarkdown'

/** One task as the server sends it (#2965): a request thread, linked to its issue (group) and parent task. */
export type TeamTask = {
 id:string; parent?:string; group?:string; group_title?:string; title:string
 state:'open'|'in_progress'|'blocked'|'needs_you'|'stalled'|'done'
 agent?:string; agent_key?:string; person?:string; mine?:boolean; for_me?:boolean; cli?:string
 asked_by?:string; line?:string; proof?:string; at:string; bridge?:{thread_id:string;can_open:boolean}
 links?:string[]; files?:TaskFile[]; category?:string; tags?:string[]
}
/** A file sent in the task's thread; the server opens it for whoever sees the task. */
export type TaskFile={id:string;name:string;size:number}
export type TaskFileContent={name:string;content_base64:string}

const image=/\.(png|jpe?g|gif|webp)$/i, text=/\.(md|markdown|txt|log|json|csv)$/i
const linkLabel=(url:string)=>{const gh=url.match(/github\.com\/[^/]+\/[^/]+\/(issues|pull)\/(\d+)/);if(gh)return gh[1]==='pull'?`PR ${gh[2]}`:`#${gh[2]}`;try{return new URL(url).hostname.replace(/^www\./,'')}catch{return url}}
const fileType=(name:string)=>image.test(name)?`image/${name.split('.').pop()!.toLowerCase().replace('jpg','jpeg')}`:'application/octet-stream'
const fileText=(base64:string)=>new TextDecoder().decode(Uint8Array.from(atob(base64),c=>c.charCodeAt(0)))

/** Links and files of a task or a Needs-you card: links open in a new tab; an image or text file opens in place; anything else downloads. */
export function TaskAttachments({links,files,open}:{links?:string[];files?:TaskFile[];open?:(file:TaskFile)=>Promise<TaskFileContent>}) {
 const [shown,setShown]=useState<{id:string;content?:TaskFileContent;error?:string}>()
 if(!links?.length&&!files?.length)return null
 const click=async(file:TaskFile)=>{
  if(shown?.id===file.id){setShown(undefined);return}
  if(!open)return
  setShown({id:file.id})
  try{
   const content=await open(file)
   if(image.test(file.name)||text.test(file.name)){setShown({id:file.id,content});return}
   const a=document.createElement('a');a.href=`data:${fileType(file.name)};base64,${content.content_base64}`;a.download=content.name;a.click();setShown(undefined)
  }catch(e){setShown({id:file.id,error:(e as {response?:{data?:{error?:{message?:string}}}})?.response?.data?.error?.message||'This file could not be opened.'})}
 }
 const chip='rounded border border-border px-1 text-[10px] hover:bg-muted'
 return <div className="mt-0.5 space-y-1">
  <div className="flex flex-wrap gap-1">
   {links?.map(url=><a key={url} href={url} target="_blank" rel="noreferrer" title={url} className={`${chip} text-primary`}>{linkLabel(url)} ↗</a>)}
   {files?.map(file=><button key={file.id} type="button" aria-expanded={shown?.id===file.id} title={`${file.name} · ${Math.max(1,Math.round(file.size/1024))} KB`} onClick={()=>void click(file)} className={chip}>{image.test(file.name)?'🖼':'📄'} {file.name}</button>)}
  </div>
  {shown&&<div className="rounded border border-border p-1 text-xs">
   {shown.error?<p role="alert">{shown.error}</p>:!shown.content?<p>Opening…</p>:image.test(shown.content.name)?<img alt={shown.content.name} src={`data:${fileType(shown.content.name)};base64,${shown.content.content_base64}`} className="max-h-72 max-w-full rounded"/>
    :/\.(md|markdown)$/i.test(shown.content.name)?<PlainMarkdown content={fileText(shown.content.content_base64)} className="max-h-96 overflow-auto"/>
    :<pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words">{fileText(shown.content.content_base64)}</pre>}
  </div>}
 </div>
}

/** Category and tags are whatever the project's instructions name; a stable colour per category keeps them apart. */
const categoryTones=['bg-rose-500/10 text-rose-700 dark:text-rose-300','bg-sky-500/10 text-sky-700 dark:text-sky-300','bg-emerald-500/10 text-emerald-700 dark:text-emerald-300','bg-amber-500/10 text-amber-700 dark:text-amber-300','bg-violet-500/10 text-violet-700 dark:text-violet-300','bg-teal-500/10 text-teal-700 dark:text-teal-300']
const categoryTone=(category:string)=>categoryTones[[...category].reduce((sum,c)=>(sum*31+c.charCodeAt(0))>>>0,7)%categoryTones.length]
const noCategory='Uncategorised'
type TaskFilter={category?:string;tag?:string}
const matches=(task:TeamTask,filter:TaskFilter)=>(!filter.category||(task.category||noCategory)===filter.category)&&(!filter.tag||!!task.tags?.includes(filter.tag))

function TaskLabels({task,pick}:{task:TeamTask;pick:(filter:TaskFilter)=>void}) {
 if(!task.category&&!task.tags?.length)return null
 return <div className="mt-0.5 flex flex-wrap gap-1">
  {task.category&&<button type="button" title={`Show only ${task.category}`} onClick={()=>pick({category:task.category})} className={`rounded px-1 text-[10px] font-medium ${categoryTone(task.category)}`}>{task.category}</button>}
  {task.tags?.map(tag=><button key={tag} type="button" title={`Show only #${tag}`} onClick={()=>pick({tag})} className="rounded bg-muted px-1 text-[10px] text-muted-foreground hover:text-foreground">#{tag}</button>)}
 </div>
}

/** One row of category and tag chips with counts; clicking one shows only that work. */
function TaskFilterBar({tasks,filter,setFilter}:{tasks:TeamTask[];filter:TaskFilter;setFilter:(filter:TaskFilter)=>void}) {
 const count=(values:string[])=>{const out=new Map<string,number>();for(const value of values)out.set(value,(out.get(value)||0)+1);return [...out.entries()].sort((a,b)=>b[1]-a[1]||a[0].localeCompare(b[0]))}
 const categories=count(tasks.map(task=>task.category||noCategory)), tags=count(tasks.flatMap(task=>task.tags||[]))
 if(categories.length<2&&!filter.category&&!tags.length)return null
 const chip=(on:boolean)=>`rounded px-1.5 py-0.5 text-[10px] ${on?'bg-primary text-primary-foreground':'bg-muted text-muted-foreground hover:text-foreground'}`
 return <div role="group" aria-label="Filter tasks" className="flex flex-wrap items-center gap-1">
  {categories.map(([category,n])=><button key={`c:${category}`} type="button" aria-pressed={filter.category===category} onClick={()=>setFilter({...filter,category:filter.category===category?undefined:category})} className={chip(filter.category===category)}>{category} {n}</button>)}
  {tags.length>0&&<span aria-hidden="true" className="px-0.5 text-muted-foreground">·</span>}
  {tags.map(([tag,n])=><button key={`t:${tag}`} type="button" aria-pressed={filter.tag===tag} onClick={()=>setFilter({...filter,tag:filter.tag===tag?undefined:tag})} className={chip(filter.tag===tag)}>#{tag} {n}</button>)}
  {(filter.category||filter.tag)&&<button type="button" onClick={()=>setFilter({})} className="px-1 text-[10px] text-primary hover:underline">Show all</button>}
 </div>
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

/** By task, by agent and by category: the same server list shown three ways, filtered by a category or tag.
 * Clicking a task opens its thread when the server allows it. */
export function TeamTasks({tasks:all,mode,thread,openFile}:{tasks:TeamTask[];mode:'tasks'|'agents'|'categories';thread:(id:string)=>ReactNode;openFile?:(task:TeamTask,file:TaskFile)=>Promise<TaskFileContent>}) {
 const [open,setOpen]=useState<Record<string,boolean>>({})
 const [shown,setShown]=useState('')
 const [filter,setFilter]=useState<TaskFilter>({})
 const toggle=(key:string,initial:boolean)=>setOpen(current=>({...current,[key]:!(current[key]??initial)}))
 // A sub-task whose parent is filtered out shows at the top level.
 const kept=all.filter(task=>matches(task,filter)), ids=new Set(kept.map(task=>task.id))
 const tasks=kept.map(task=>task.parent&&!ids.has(task.parent)?{...task,parent:undefined}:task)
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
   <div style={{paddingLeft:depth*16+36}}><TaskLabels task={task} pick={setFilter}/><TaskAttachments links={task.links} files={task.files} open={openFile&&(file=>openFile(task,file))}/></div>
   {shown===task.id&&canOpen&&<div style={{paddingLeft:depth*16+20}}>{thread(task.bridge!.thread_id)}</div>}
   {expanded&&!flat&&kids.length>0&&<ol>{kids.map(kid=>row(kid,depth+1))}</ol>}
  </li>
 }
 if(!all.length)return <p className="text-xs text-muted-foreground">No tasks yet. Agents add one each time they ask an agent, a person or the Team.</p>
 return <div className="space-y-2">
  <TaskFilterBar tasks={all} filter={filter} setFilter={setFilter}/>
  {!tasks.length?<p className="text-xs text-muted-foreground">No tasks match. <button type="button" onClick={()=>setFilter({})} className="text-primary hover:underline">Show all</button></p>:mode==='tasks'?byTask():mode==='categories'?byCategory():byAgent()}
 </div>
 function byCategory(){
  const groups=new Map<string,TeamTask[]>()
  for(const task of tasks){const key=task.category||noCategory;groups.set(key,[...(groups.get(key)||[]),task])}
  return <ol aria-label="Categories" className="space-y-1">{[...groups.entries()].sort((a,b)=>Number(a[0]===noCategory)-Number(b[0]===noCategory)||b[1].length-a[1].length).map(([category,list])=>{
   const key=`category:${category}`, done=list.filter(task=>task.state==='done').length, initial=done<list.length, expanded=open[key]??initial
   return <li key={key} className="rounded border border-border/60 px-1">
    <button type="button" aria-expanded={expanded} onClick={()=>toggle(key,initial)} className="flex w-full min-w-0 items-center gap-1.5 py-1 text-left">
     <ChevronRight aria-hidden="true" className={`h-3 w-3 shrink-0 ${expanded?'rotate-90':''}`}/>
     <span className={`shrink-0 rounded px-1 text-[10px] font-medium ${category===noCategory?'bg-muted text-muted-foreground':categoryTone(category)}`}>{category}</span>
     <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">{list.length} {list.length===1?'task':'tasks'} · {done===list.length?'all done':`${done} done`}</span>
    </button>
    {expanded&&<ol className="pb-1">{[...list].sort(newest).map(task=>row(task,1,true))}</ol>}
   </li>
  })}</ol>
 }
 function byTask(){
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
 function byAgent(){
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
}
