import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { Button } from '../ui/Button'

export function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch { setCopied(false) }
  }
  return <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" title={copied ? 'Copied' : `Copy: ${label}`} onClick={() => void copy()}>
    {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
  </Button>
}

export function CommandRow({ command, label }: { command: string; label: string }) {
  return <div className="flex items-center gap-2">
    <code className="min-w-0 flex-1 overflow-x-auto rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground" aria-label={label}>{command}</code>
    <CopyButton text={command} label={label} />
  </div>
}

/** Setup instructions only. Copying a command does not install or connect anything. */
export function InstallAgentWorksStep({ origin }: { origin: string }) {
  const [windows, setWindows] = useState(false)
  const shellQuote = (value: string) => `'${value.replace(/'/g, "'\\''")}'`
  const psQuote = (value: string) => `'${value.replace(/'/g, "''")}'`
  const command = windows
    ? `& ([scriptblock]::Create((irm ${psQuote(`${origin}/api/downloads/cli/install-agentworks.ps1`)}))) -Server ${psQuote(origin)} -NoLogin`
    : `curl -fsSL ${shellQuote(`${origin}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${shellQuote(origin)} --no-login`
  return <section className="space-y-2" aria-label="Install the terminal app">
    <h4 className="text-sm font-medium text-foreground">1. Install the AgentWorks app for your terminal</h4>
    <p className="text-xs text-muted-foreground">This is optional for MCP tools. Install it if you want your agents to message each other; otherwise skip to step 2.</p>
    <div className="flex flex-wrap gap-2" role="group" aria-label="Terminal app platform">
      <Button variant={windows ? 'outline' : 'default'} size="sm" aria-pressed={!windows} onClick={() => setWindows(false)}>macOS / Linux</Button>
      <Button variant={windows ? 'default' : 'outline'} size="sm" aria-pressed={windows} onClick={() => setWindows(true)}>Windows</Button>
    </div>
    <p className="text-xs text-muted-foreground">Run this in {windows ? 'PowerShell' : 'Terminal'} on your computer. It installs from this server without signing you in.</p>
    <CommandRow label="Install AgentWorks terminal app" command={command} />
  </section>
}

type MessagingApp = 'claude' | 'codex' | 'cursor' | 'muse'

export function AgentMessagingStep({ app }: { app?: MessagingApp }) {
  const [selected, setSelected] = useState<MessagingApp>('claude')
  return <section className="space-y-2" aria-label="Optional agent messaging">
    <h4 className="text-sm font-medium text-foreground">3. Optional: let your agents message each other</h4>
    <p className="text-xs text-muted-foreground">After installing the terminal app, run this setup command for your AI app.</p>
    {!app && <label className="block text-xs text-muted-foreground">AI app for messaging<select className="ml-2 rounded border border-border bg-background p-2" value={selected} onChange={event => setSelected(event.target.value as MessagingApp)}><option value="claude">Claude Code</option><option value="codex">Codex</option><option value="cursor">Cursor</option><option value="muse">Muse</option></select></label>}
    <CommandRow label="Set up agent messaging" command={`agentworks setup ${app ?? selected}`} />
    <p className="text-xs text-muted-foreground">Then open <a className="text-primary underline" href="/agentbridge/connect">AgentBridge → Connect an agent</a> to sign in, check support for your app and computer, and connect the chat you choose.</p>
  </section>
}
