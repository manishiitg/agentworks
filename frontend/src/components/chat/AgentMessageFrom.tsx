import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import ConnectCodingAgents from '../providers/ConnectCodingAgents'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'
import { CodingCliIcon } from './CodingCliIcon'

const CODING_APPS = ['claude', 'codex', 'cursor', 'agy']

/** The coding apps that can message an agent, as one stack of logos (owner, 28 Nov). */
export function CodingAppIcons() {
  return <span aria-label="Claude Code, Codex, Cursor, Antigravity" className="isolate flex shrink-0 -space-x-1">
    {CODING_APPS.map(app => <CodingCliIcon key={app} provider={app} className="inline-flex h-5 w-5 items-center justify-center rounded-full border border-border bg-background" />)}
  </span>
}

/** The text Copy puts on the clipboard: the ask plus how to send it and how to connect AgentWorks, so a coding app that
 *  has never heard of AgentWorks (a fresh Cursor chat) knows what to do (owner, 28 Nov). Only for a project on this Mac:
 *  agents on a Mac do not message online projects directly; those are reached through their Team (owner, 28 Nov). */
export function agentMessagePrompt(agent: string, project: string) {
  const find = `Find the agent with action=list_agents and query "${agent}", send the request with action=ask to the address it returns, and tell me its reply when it arrives.`
  return [
    `Ask the "${agent}" agent in my AgentWorks project "${project}" on this Mac to <say what you need>.`,
    '',
    `How: use the "messages" tool of the AgentWorks tools connected to the AgentWorks app on this Mac (this project lives on this Mac, not on an online server). ${find}`,
    '',
    'No AgentWorks tools in this app yet? Tell me to install the AgentWorks Mac app (https://github.com/manishiitg/agentworks/releases/latest), run `agentworks setup` in Terminal, then restart this app and ask again.',
  ].join('\n')
}

// How your other agents reach this one (owner, 28 Nov, #4510): one line to say in Claude Code, Codex or another agent,
// replacing the AgentBridge page. Messages between agents on this Mac need no server. The line shown stays short;
// Copy takes the full how-to (agentMessagePrompt).
export function AgentMessageFrom({ line, copyText, online }: { line: string, copyText?: string, online?: boolean }) {
  const [copied, setCopied] = useState(false)
  const [connect, setConnect] = useState(false)
  const onMac = hasDesktopConnections()
  const copy = () => { void navigator.clipboard?.writeText(copyText || line).then(() => { setCopied(true); window.setTimeout(() => setCopied(false), 1500) }).catch(() => {}) }
  // An online project (owner, 28 Nov): no line to copy and no setup; other agents reach it only through its Team.
  if (online) return <p className="text-muted-foreground">This project is online, so agents on your Mac don’t message it directly. Teammates and their agents ask it through the project’s Team.</p>
  return <div className="space-y-1.5 text-muted-foreground">
    <p>In Claude Code, Codex, Cursor, Antigravity or any agent connected to AgentWorks on this Mac, say:</p>
    <div className="flex items-center gap-2 rounded-md bg-muted/60 px-2 py-1.5 font-mono text-[11px] text-foreground">
      <span className="min-w-0 flex-1 break-words">{line}</span>
      <button type="button" onClick={copy} aria-label="Copy" title="Copy" className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {copied ? <Check aria-hidden="true" className="h-3.5 w-3.5" /> : <Copy aria-hidden="true" className="h-3.5 w-3.5" />}
      </button>
    </div>
    {copyText && <p>Copy also includes how to find it and how to connect AgentWorks, so a new chat in any of these apps knows what to do.</p>}
    {onMac && <p>Works on this Mac with no server. Claude Code or Codex not connected yet? <button type="button" aria-expanded={connect} onClick={() => setConnect(!connect)} className="text-primary hover:underline">Connect it ›</button></p>}
    {connect && <ConnectCodingAgents compact />}
  </div>
}
