import { useEffect, useRef, useState } from 'react'
import { Archive, Loader2 } from 'lucide-react'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { cn } from '../../lib/utils'
import { teamRequest, type TeamTarget } from './teamApi'

type BackupLine = { line:number; role:string; text:string }
type Backup = { banner:string; lines:BackupLine[]; older?:boolean }

/** A teammate's Mac agent while its Mac is offline (#3577): the chat backup its Mac synced to the server, under a
 * banner the server words (a copy, how old, asks wait for the real agent). The agent never answers from it; the Ask box's
 * "Answer from its chat" has the viewer's own agent read it. */
export function TeammateChatBackup({target,agentId}:{target:TeamTarget;agentId:string}) {
 const [backup,setBackup]=useState<Backup|null>(null),[error,setError]=useState('')
 const list=useRef<HTMLDivElement>(null)
 useEffect(()=>{
  let active=true
  setBackup(null);setError('')
  teamRequest<Backup>(target,`/agents/${encodeURIComponent(agentId)}/chat-backup`)
   .then(data=>{if(active)setBackup(data)})
   .catch(cause=>{if(active)setError(cause instanceof Error?cause.message:'Its conversation could not be opened.')})
  return()=>{active=false}
 },[target.connection,target.project,agentId]) // eslint-disable-line react-hooks/exhaustive-deps
 useEffect(()=>{list.current?.scrollTo({top:list.current.scrollHeight})},[backup])
 if(error)return <p role="status" className="flex h-full items-center justify-center p-6 text-center text-sm text-muted-foreground">{error}</p>
 if(!backup)return <p role="status" className="flex h-full items-center justify-center gap-2 p-6 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" aria-hidden="true"/>Opening its conversation…</p>
 return <div className="flex h-full min-h-0 flex-col">
  <p role="note" data-chat-backup className="mx-4 mt-3 flex shrink-0 items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-foreground">
   <Archive className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-600 dark:text-amber-400" aria-hidden="true"/>
   <span>{backup.banner}</span>
  </p>
  <div ref={list} className="min-h-0 flex-1 space-y-2 overflow-y-auto px-4 py-3 text-sm">
   {backup.older&&<p className="text-center text-xs text-muted-foreground">Older messages are not shown.</p>}
   {backup.lines.map(row=>{
    const mine=row.role==='user'
    const tool=row.role==='tool'||row.text.startsWith('Called ')
    return <div key={row.line} className={cn('flex',mine?'justify-end':'justify-start')}>
     <div className={cn('max-w-[85%] min-w-0 break-words rounded-2xl px-3 py-2 opacity-90',mine?'bg-primary/10':tool?'bg-muted/60 font-mono text-xs text-muted-foreground':'bg-muted')}>
      {mine||tool?<p className="whitespace-pre-wrap">{row.text}</p>:<MarkdownRenderer content={row.text} untrustedContent disablePathLinking copyableCode />}
     </div>
    </div>})}
  </div>
 </div>
}
