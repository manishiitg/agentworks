import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import ConnectCodingAgents from '../providers/ConnectCodingAgents'
import { hasDesktopConnections } from '../../stores/useConnectedWorkStore'

// How your other agents reach this one (owner, 28 Nov, #4510): one line to say in Claude Code, Codex or another agent,
// replacing the AgentBridge page. Messages between agents on this Mac need no server.
export function AgentMessageFrom({ line }: { line: string }) {
  const [copied, setCopied] = useState(false)
  const [connect, setConnect] = useState(false)
  const onMac = hasDesktopConnections()
  const copy = () => { void navigator.clipboard?.writeText(line).then(() => { setCopied(true); window.setTimeout(() => setCopied(false), 1500) }).catch(() => {}) }
  return <div className="space-y-1.5 text-muted-foreground">
    <p>In Claude Code, Codex or any agent in this project, say:</p>
    <div className="flex items-center gap-2 rounded-md bg-muted/60 px-2 py-1.5 font-mono text-[11px] text-foreground">
      <span className="min-w-0 flex-1 break-words">{line}</span>
      <button type="button" onClick={copy} aria-label="Copy" title="Copy" className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-background hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        {copied ? <Check aria-hidden="true" className="h-3.5 w-3.5" /> : <Copy aria-hidden="true" className="h-3.5 w-3.5" />}
      </button>
    </div>
    {onMac && <p>Works on this Mac with no server. Claude Code or Codex not connected yet? <button type="button" aria-expanded={connect} onClick={() => setConnect(!connect)} className="text-primary hover:underline">Connect it ›</button></p>}
    {connect && <ConnectCodingAgents compact />}
  </div>
}
