import type { ReactNode } from 'react'
import { CodingCliIcon } from '../chat/CodingCliIcon'

/** Compact monitor row: the agent identity keeps its width; live text yields first. */
export function TeamMonitorAgentRow({name,cli,activity,line,team,server,avatar,onOpen}:{name:string;cli?:string;activity:string;line?:string;team:string;server?:string;avatar?:ReactNode;onOpen:()=>void}) {
 const idle=activity==='idle', waiting=activity==='waiting'
 return <button type="button" role="menuitem" aria-label={`Open ${name} · ${team}`} onClick={onOpen} title={[name,team,server,line].filter(Boolean).join(' · ')} className={`flex h-7 w-full min-w-0 items-center gap-1.5 rounded px-2 text-left text-[11px] hover:bg-muted ${idle?'opacity-50':''}`}>
  <CodingCliIcon provider={cli||'terminal'} className="inline-flex h-3.5 w-3.5 shrink-0 items-center"/>
  <span className="max-w-[140px] shrink-0 truncate font-medium">{name}</span>
  <span role="img" aria-label={idle?'idle':waiting?'waiting':'running'} className={`shrink-0 ${idle?'text-muted-foreground':waiting?'text-warning':'text-emerald-600 dark:text-emerald-400'}`}>{idle?'○':waiting?'◐':'●'}</span>
  <span className="min-w-0 flex-1 truncate text-muted-foreground">{line||(idle?'idle':waiting?'waiting':'running')}</span>
  <span title={server?`${team} · ${server}`:team} className="max-w-[100px] shrink-0 truncate rounded border border-border px-1 text-[10px] text-muted-foreground">{team}</span>
  {server&&<span className="max-w-[64px] truncate text-[9px] text-muted-foreground">{server}</span>}
  {avatar}
 </button>
}
