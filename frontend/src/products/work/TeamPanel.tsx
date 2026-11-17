import { agentPurposeTooltip } from '../../utils/agentPurposeTooltip'
import type { TeamAgentChat, TeamAgentChatRead, TeamAgentProgram, TeamAgentStatus, TeamTabPerson } from './teamApi'
import { CodingCliIcon } from '../../components/chat/CodingCliIcon'
import { AgentPlaceMark } from '../../components/chat/AgentIcon'
import { Avatar, type AvatarFields } from '../../components/ui/Avatar'
import { useEffect, useState, type ReactNode } from 'react'
import { ArrowLeft, CalendarClock, Cloud, Info, Pencil, Users, X } from 'lucide-react'

/** All rows and live lines are already admitted/filtered by the server. */
export type TeamPanelAgent = AvatarFields & {
 purpose?: string; examples?: string[]
 connection?: string; id: string; name: string; person: string; person_id?: string; mine: boolean; team_agent?: boolean
 client_build?:string; update_needed?:boolean; cli?: string; engine?: string; model_id?: string; reasoning_effort?: string
 online: boolean; place: string; activity?: string; last_message?: string; last_message_at?: string; typing?: boolean
 task_state?: string; offline_copy_ready?: boolean
}
export type TeamPanelShared = { key: string; name: string; canChange: boolean; chosen?: number }
export type TeamPanelEvent = { id: string; kind: string; at: string; title: string; verb: string; line?: string; private?: boolean; unseen?:boolean;today?:boolean; cli?:string; bridge?:{thread_id:string;can_open:boolean}; task?:string; task_title?:string; task_state?:string }
export type TeamPanelRoutine = { id: string; name: string; line?: string; enabled: boolean; last_run_at?: string; last_status?: string; next_run_at?: string; chat_id?: string }
export type TeamPanelView = 'live' | 'agents' | 'shared' | 'items' | 'ask'
type Artifact = { id: string; title: string; kind: string; addedBy?: string }
const titles: Record<TeamPanelView,string> = {live:'Team', agents:'All agents', shared:'Shared by you', items:'On the Team', ask:'Team agent'}
function age(at?:string) {
 const elapsed = at ? Math.max(0,Date.now()-Date.parse(at)) : NaN
 return !Number.isFinite(elapsed) ? '' : elapsed<60_000 ? 'now' : elapsed<3_600_000 ? `${Math.floor(elapsed/60_000)}m` : elapsed<86_400_000 ? `${Math.floor(elapsed/3_600_000)}h` : `${Math.floor(elapsed/86_400_000)}d`
}
const name = (row:TeamPanelAgent) => row.team_agent ? 'Team agent' : row.name
const working = (row:TeamPanelAgent) => row.activity==='running'||!row.team_agent&&row.activity==='waiting'
const person = (row:TeamPanelAgent) => row.team_agent?'Team':row.person
const status = (row:TeamPanelAgent) => row.task_state==='needs_you' ? 'needs you' : row.task_state==='blocked' ? 'blocked' : row.task_state==='stalled' ? 'stalled' : row.typing ? 'typing…' : row.activity==='waiting' ? `waiting for ${row.mine?'you':row.person}` : row.activity==='running' ? 'running' : row.mine||row.online ? 'idle' : 'offline'
const runtime = (row:TeamPanelAgent, includeHelper=false) => [person(row),row.place==='mac'?'Mac':'Server',row.cli||row.engine,includeHelper&&row.client_build&&`AgentWorks ${row.client_build}${row.update_needed?' · Update needed':''}`,row.model_id,row.reasoning_effort].filter(Boolean).join(' · ')

/** Shared Mac/web live panel; detail views replace the right panel, not inline disclosures. */
export function TeamPanel({scope,agents,onAsk,people=[],selectedPerson,onPersonSelect,personAction,onNavigate,view='live',shared=[],onShared,artifacts=[],onArtifact,routines=[],teamAgentAsk,teamAgent,onTeamAgent,teamAgentProgram,teamAgentChats,error,askLabel}: {
 people?:TeamTabPerson[]; selectedPerson?:string; onPersonSelect?:(id?:string)=>void; personAction?:ReactNode
 scope:string; agents:TeamPanelAgent[]; onAsk:(row:TeamPanelAgent)=>void
 view?:TeamPanelView; onNavigate:(view:TeamPanelView)=>void; shared?:TeamPanelShared[]; onShared?:(row:TeamPanelShared)=>void
 artifacts?:Artifact[]; onArtifact?:(id:string)=>void; routines?:TeamPanelRoutine[]; teamAgentAsk?:ReactNode; error?:string; askLabel?:string
 teamAgent?:TeamAgentStatus; onTeamAgent?:()=>void
 /** The Team owner's program switch for the Team agent; shown only when the server says they can change it. */
 teamAgentProgram?:{load:()=>Promise<TeamAgentProgram[]>;choose:(engine:string,model?:string)=>Promise<void>}
 teamAgentChats?:TeamAgentChatsSource
}) {
 const [query,setQuery]=useState('')
 const [howOpen,setHowOpen]=useState(false)
 useEffect(()=>{setQuery('')},[scope,view])

 const rows=agents.filter(row=>row.team_agent||!row.mine)
 const hasHome=rows.some(row=>row.team_agent)
 const busy=rows.filter(working)
 const home=rows.find(row=>row.team_agent)
 const selected=people.find(person=>person.user_id===selectedPerson&&person.note!=='invited')
 const personal=selected?[...agents.filter(row=>row.person_id===selected.user_id)].sort((a,b)=>Number(working(b))-Number(working(a))):[]
 const button='flex w-full min-w-0 items-start gap-2 rounded-md px-1 py-1.5 text-left hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
 const agent=(row:TeamPanelAgent)=><button key={row.id} type="button" aria-label={`Open ${row.team_agent?'Team agent':`${row.person}'s ${row.name}`}`} onClick={()=>onAsk(row)} className={button} title={agentPurposeTooltip(row,runtime(row,true))}>
  <Avatar {...row} name={person(row)} className="mt-0.5 h-6 w-6 shrink-0 text-[10px]"/><AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
  <span className="min-w-0 flex-1"><span className="block truncate text-xs font-medium">{name(row)}{row.update_needed&&<span> · Update needed</span>}</span>
   {row.purpose && <span className="block text-xs text-muted-foreground">{row.purpose}</span>}
   {!!row.examples?.length && <span className="block text-xs text-muted-foreground">Ask me to: {row.examples.join(' · ')}</span>}
   <span className="block truncate text-[10px] text-muted-foreground">{runtime(row)}</span>
   <span className={`block truncate text-[11px] ${row.activity==='waiting'?'text-amber-600 dark:text-amber-400':working(row)?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>
    {status(row)}
    {row.last_message&&<span className="text-muted-foreground"> · {!working(row)&&age(row.last_message_at)?`${age(row.last_message_at)} · `:''}{row.last_message}</span>}
   </span>
   {!row.online&&!working(row)&&<span className="block text-[10px] text-muted-foreground">{row.offline_copy_ready?'Its copy answers':hasHome?'New asks go to the Team agent':'No Team agent available'}</span>}
  </span>
 </button>
 const compactAgent=(row:TeamPanelAgent,showPerson=true)=>{
  const state=row.activity==='waiting'?'waiting':working(row)?'running':'idle'
  return <button key={row.id} type="button" aria-label={`Open ${row.team_agent?'Team agent':`${row.person}'s ${row.name}`}`} onClick={()=>onAsk(row)} title={agentPurposeTooltip(row,[runtime(row,true),status(row),row.update_needed&&'Update needed',row.last_message].filter(Boolean).join(' · '))} className={`flex h-6 w-full min-w-0 items-center gap-1.5 rounded px-1 text-left text-[11px] hover:bg-background/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${state==='idle'?'opacity-50':''}`}>
   <CodingCliIcon provider={row.cli||row.engine||'terminal'} className="inline-flex h-3.5 w-3.5 shrink-0 items-center"/>
   <span className="max-w-[120px] shrink-0 truncate font-medium">{name(row)}</span>
   <span role="img" aria-label={state} title={state} className={`shrink-0 ${state==='waiting'?'text-amber-600 dark:text-amber-400':state==='running'?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>{state==='running'?'●':state==='waiting'?'◐':'○'}</span>
   <span className="min-w-0 flex-1 truncate text-muted-foreground">{row.last_message||(state==='idle'?'idle':status(row))}</span>
   <AgentPlaceMark place={row.place==='server'?'server':'mac'}/>
   {showPerson&&<span className="shrink-0" title={person(row)}><Avatar {...row} name={person(row)} connection={row.connection} className="h-3.5 w-3.5 shrink-0 text-[8px]"/></span>}
  </button>
 }
 const ordered=[...busy,...rows.filter(row=>!working(row)&&row.online),...rows.filter(row=>!working(row)&&!row.online)]
 const results=ordered.filter(row=>[name(row),runtime(row),status(row)].join(' ').toLowerCase().includes(query.trim().toLowerCase()))
 return <section aria-label={view==='live'?'Team live panel':titles[view]} className={`min-w-0 ${view==='live'?'space-y-1 text-[11px]':'space-y-2 text-sm'}`}>
  {view!=='live'&&<header className="sticky top-0 z-10 space-y-2 bg-surface-panel pb-2"><button type="button" onClick={()=>onNavigate('live')} className="flex items-center gap-1 text-xs text-primary"><ArrowLeft aria-hidden="true" className="h-3 w-3"/>Team</button><h2 className="text-sm font-semibold">{view === 'ask' ? askLabel || titles.ask : titles[view]}</h2></header>}
  {view==='live'&&<>
   {teamAgent&&<TeamAgentRow status={teamAgent} working={!!home&&working(home)} open={howOpen} onToggle={()=>setHowOpen(!howOpen)} onAsk={onTeamAgent} program={teamAgent.can_change_program?teamAgentProgram:undefined} chats={teamAgentChats}/>}
   {selected?<section aria-label={`${selected.name}'s agents`}>
    <div className="flex items-center gap-1.5 border-t border-border px-1 py-1"><Avatar {...selected} name={selected.name} className="h-4 w-4 shrink-0 aspect-square leading-none text-[8px]"/><span className="min-w-0 truncate font-medium">{selected.name}</span><span className="shrink-0 text-muted-foreground">· {personal.length} agents</span><button type="button" aria-label="Back to working agents" onClick={()=>onPersonSelect?.(undefined)} className="ml-auto"><X className="h-3 w-3"/></button></div>
    {personal.map(row=>compactAgent(row,false))}
    {!personal.length&&<p className="px-1 py-2 text-xs text-muted-foreground">{selected.name} hasn't added an agent to this Team yet.</p>}
    {personal.length>0&&personAction}
   </section>:busy.filter(row=>!teamAgent||!row.team_agent).length>0?<section aria-label="Working now">{busy.filter(row=>!teamAgent||!row.team_agent).map(row=>compactAgent(row))}</section>:null}
  </>}
  {view==='agents'&&<><input type="search" aria-label="Search Team agents" placeholder="Search agents or people…" value={query} onChange={e=>setQuery(e.target.value)} className="w-full rounded border border-border bg-background px-2 py-1.5 text-xs"/><div aria-label="All Team agents" className="space-y-0.5">{results.map(row=>agent(row))}{!results.length&&<p className="text-xs text-muted-foreground">No matching agents.</p>}</div></>}
  {view==='shared'&&<div aria-label="Your shared agents">{shared.map(row=><button key={row.key} type="button" onClick={()=>onShared?.(row)} className={button}><Users aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0 flex-1"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.canChange?'Can make changes':'Answers only'} · {row.chosen===undefined?'Whole team':`${row.chosen} chosen`}</span></span><Pencil aria-hidden="true" className="h-3 w-3 text-muted-foreground"/></button>)}</div>}
  {view==='items'&&<div aria-label="Team artifacts and automations">{artifacts.map(row=><button key={row.id} type="button" onClick={()=>onArtifact?.(row.id)} className={button}><span className="min-w-0"><span className="block truncate text-xs">{row.title}</span><span className="block text-[10px] text-muted-foreground">{row.kind}{row.addedBy?` · Added by ${row.addedBy}`:''}</span></span></button>)}{routines.map(row=><div key={row.id} className="flex gap-2 px-1 py-1.5"><CalendarClock aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground"/><span className="min-w-0"><span className="block truncate text-xs">{row.name}</span><span className="block text-[10px] text-muted-foreground">{row.line}{!row.enabled?' · off':''}</span></span></div>)}</div>}
  {view==='ask'&&teamAgentAsk}
  {error&&<p role="alert" className="text-[11px] text-destructive">{error}</p>}
 </section>
}

const teamAgentState: Record<TeamAgentStatus['state'], { label: string; tone: string }> = {
 ready: { label: 'Online', tone: 'text-emerald-600 dark:text-emerald-400' },
 setting_up: { label: 'Setting up', tone: 'text-muted-foreground' },
 off: { label: 'Off', tone: 'text-muted-foreground' },
 unavailable: { label: 'Not reachable', tone: 'text-amber-600 dark:text-amber-400' },
 sign_in_needed: { label: 'Sign-in needed', tone: 'text-destructive' },
}

/** Reads the Team agent's chats (server decides who may: every current Team member). */
export type TeamAgentChatsSource = { list: () => Promise<TeamAgentChat[]>; read: (session: string) => Promise<TeamAgentChatRead> }

/** The Team agent, pinned at the top like a teammate: name, provider and model (owner picks), state (its reason on hover), Ask, and its chats; the info icon opens how it works (server wording). */
function TeamAgentRow({status,working,open,onToggle,onAsk,program,chats}:{status:TeamAgentStatus;working:boolean;open:boolean;onToggle:()=>void;onAsk?:()=>void;program?:{load:()=>Promise<TeamAgentProgram[]>;choose:(engine:string,model?:string)=>Promise<void>};chats?:TeamAgentChatsSource}) {
 const [choices,setChoices]=useState<TeamAgentProgram[]>()
 const [engine,setEngine]=useState<TeamAgentProgram>()
 const [saving,setSaving]=useState(false)
 const [problem,setProblem]=useState('')
 const [list,setList]=useState<TeamAgentChat[]>()
 const pick=()=>{setProblem('');setEngine(undefined);program?.load().then(setChoices).catch((e:unknown)=>setProblem(e instanceof Error?e.message:'Could not load the providers. Try again.'))}
 const choose=(c:TeamAgentProgram,model?:string)=>{if(!program)return;if(!model&&(c.models?.length||0)>1){setEngine(c);return}setSaving(true);setProblem('');program.choose(c.engine,model).then(()=>{setChoices(undefined);setEngine(undefined)}).catch((e:unknown)=>setProblem(e instanceof Error?e.message:'Could not change the provider. Try again.')).finally(()=>setSaving(false))}
 const toggleChats=()=>{if(list){setList(undefined);return}setProblem('');chats?.list().then(setList).catch((e:unknown)=>setProblem(e instanceof Error?e.message:'Could not load its chats. Try again.'))}
 const state=working?{label:'Working',tone:'text-emerald-600 dark:text-emerald-400'}:teamAgentState[status.state]
 const canAsk=(status.state==='ready'||status.state==='sign_in_needed')&&!!onAsk
 return <section aria-label="Team agent" title={status.line} className="rounded-md border border-border px-1.5 py-0.5">
  <div className="flex min-w-0 items-center gap-1 text-[11px]">
   <Cloud aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-sky-500"/>
   {canAsk?<button type="button" onClick={onAsk} className="shrink-0 text-left font-medium hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">Team agent</button>:<span className="shrink-0 font-medium">Team agent</span>}
   {program?<button type="button" onClick={pick} title="Change the Team agent's provider and model" className="min-w-0 truncate text-[10px] text-muted-foreground underline decoration-dotted hover:text-primary">· {status.program||'Choose provider'}</button>
    :status.program&&<span className="min-w-0 truncate text-[10px] text-muted-foreground">· {status.program}</span>}
   <span className={`shrink-0 text-[10px] ${state.tone}`}>· {state.label}</span>
   <span className="flex-1"/>
   {chats&&status.state!=='off'&&status.state!=='setting_up'&&<button type="button" aria-expanded={!!list} onClick={toggleChats} className="shrink-0 text-[10px] text-muted-foreground hover:text-primary">{list?'Hide chats':'Chats'}</button>}
   <button type="button" aria-expanded={open} aria-label={open?'Hide how the Team agent works':'How the Team agent works'} title="How it works" onClick={onToggle} className="shrink-0 text-muted-foreground hover:text-primary"><Info aria-hidden="true" className="h-3 w-3"/></button>
   {canAsk&&<button type="button" onClick={onAsk} className="shrink-0 text-[10px] text-primary hover:underline">Ask</button>}
  </div>
  {choices&&!engine&&<div role="group" aria-label="Team agent provider" className="flex flex-wrap items-center gap-1 pb-1 text-[10px]"><span className="text-muted-foreground">Run the Team agent on:</span>{choices.map(c=><button key={c.engine} type="button" disabled={saving} onClick={()=>choose(c)} className={`rounded border px-1.5 py-0.5 ${status.program?.startsWith(c.label)?'border-primary text-primary':'border-border hover:border-primary'}`}>{c.label}</button>)}{!choices.length&&<span className="text-muted-foreground">No providers are set up on this server.</span>}<button type="button" aria-label="Close" onClick={()=>setChoices(undefined)} className="text-muted-foreground"><X className="h-3 w-3"/></button></div>}
  {engine&&<div role="group" aria-label={`${engine.label} model`} className="flex flex-wrap items-center gap-1 pb-1 text-[10px]"><button type="button" onClick={()=>setEngine(undefined)} className="text-primary"><ArrowLeft aria-hidden="true" className="inline h-3 w-3"/></button><span className="text-muted-foreground">{engine.label} model:</span>{engine.models?.map(m=><button key={m.id} type="button" disabled={saving} onClick={()=>choose(engine,m.id)} className={`rounded border px-1.5 py-0.5 ${m.id===engine.model_id?'border-primary text-primary':'border-border hover:border-primary'}`}>{m.label}</button>)}<button type="button" aria-label="Close" onClick={()=>{setChoices(undefined);setEngine(undefined)}} className="text-muted-foreground"><X className="h-3 w-3"/></button></div>}
  {problem&&<p role="alert" className="pb-1 text-[10px] text-amber-600 dark:text-amber-400">{problem}</p>}
  {!open&&status.state==='sign_in_needed'&&<p role="alert" className="px-0.5 pb-1 text-[10px] text-destructive">{status.line}</p>}
  {open&&<div className="pb-1 text-[10px] text-muted-foreground"><p className="px-0.5">{status.line}</p><ul className="mt-0.5 list-disc space-y-0.5 pl-5">{status.how.map(line=><li key={line}>{line}</li>)}</ul></div>}
  {list&&chats&&<TeamAgentChats chats={list} read={chats.read}/>}
 </section>
}

/** The Team agent's chats, one per person who asked it plus its own; any member opens one to read it. Only that person writes in it, through Ask. */
function TeamAgentChats({chats,read}:{chats:TeamAgentChat[];read:(session:string)=>Promise<TeamAgentChatRead>}) {
 const [opened,setOpened]=useState<{chat:TeamAgentChat;read?:TeamAgentChatRead;problem?:string}>()
 const openChat=(chat:TeamAgentChat)=>{setOpened({chat});read(chat.session_id).then(r=>setOpened({chat,read:r})).catch((e:unknown)=>setOpened({chat,problem:e instanceof Error?e.message:'Could not open this chat. Try again.'}))}
 if(opened) return <div className="border-t border-border pb-1 text-[11px]">
  <div className="flex items-center gap-1 py-1"><button type="button" onClick={()=>setOpened(undefined)} className="flex items-center gap-1 text-[10px] text-primary"><ArrowLeft aria-hidden="true" className="h-3 w-3"/>Chats</button><span className="min-w-0 truncate font-medium">{opened.chat.you?'Your chat':opened.chat.person}</span></div>
  <p className="mb-1 rounded bg-amber-50 px-1.5 py-0.5 text-[10px] text-amber-800 dark:bg-amber-950/40 dark:text-amber-300">{opened.read?.note||'Your Team can see this chat.'}</p>
  {opened.problem&&<p role="alert" className="text-[10px] text-amber-600 dark:text-amber-400">{opened.problem}</p>}
  {!opened.read&&!opened.problem&&<p className="text-[10px] text-muted-foreground">Opening…</p>}
  {opened.read&&<div className="max-h-80 space-y-1 overflow-y-auto">{opened.read.messages.length?opened.read.messages.map((m,i)=><p key={i} className={`whitespace-pre-wrap break-words rounded px-1.5 py-1 ${m.from==='person'?'ml-6 bg-primary/10':'mr-6 bg-muted'}`}>{m.text}</p>):<p className="text-[10px] text-muted-foreground">No messages yet.</p>}</div>}
  {opened.read&&!opened.chat.you&&opened.chat.person_id&&<p className="pt-1 text-[10px] text-muted-foreground">Only {opened.chat.person} writes in this chat. Use Ask to talk to the Team agent in your own chat.</p>}
 </div>
 return <ul aria-label="Team agent's chats" className="border-t border-border py-0.5">
  {chats.map(chat=><li key={chat.session_id}><button type="button" onClick={()=>openChat(chat)} className="block w-full min-w-0 rounded px-0.5 py-0.5 text-left hover:bg-muted">
   <span className="flex items-center gap-1 text-[11px]"><span className="min-w-0 truncate font-medium">{chat.you?'You':chat.person}</span><span className={`shrink-0 text-[10px] ${chat.activity==='working'?'text-emerald-600 dark:text-emerald-400':'text-muted-foreground'}`}>· {chat.activity==='working'?'Working':'Idle'}</span></span>
   {chat.last_message&&<span className="block truncate text-[10px] text-muted-foreground">{chat.last_message}</span>}
  </button></li>)}
  {!chats.length&&<li className="px-0.5 py-0.5 text-[10px] text-muted-foreground">No one has asked the Team agent anything yet.</li>}
 </ul>
}
