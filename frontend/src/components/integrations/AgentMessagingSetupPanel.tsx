import { useState, useEffect, type ReactNode } from 'react'
import { ArrowRight, Check, Copy, Laptop, MessageSquare, Terminal } from 'lucide-react'
import { Button } from '../ui/Button'
import { getApiBaseUrl } from '../../services/api'

const clients = [
  { id: 'codex', name: 'Codex', live: true, detail: 'Desktop native delivery and CLI hooks' },
  { id: 'claude', name: 'Claude Code', live: true, detail: 'Session messaging and delivery hooks' },
  { id: 'cursor', name: 'Cursor', live: false, detail: 'HTTP MCP setup below' },
  { id: 'muse', name: 'Muse', live: false, detail: 'GitHub marketplace plugin and session hooks' },
  { id: 'pi', name: 'Pi', live: false, detail: 'Provider extension setup required' },
  { id: 'agy', name: 'agy', live: false, detail: 'Provider-specific setup required' },
] as const

function Command({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState(false)
  useEffect(() => { setCopied(false); setError(false) }, [text])
  const copy = async () => {
    try { await navigator.clipboard.writeText(text); setCopied(true); setError(false) }
    catch { setError(true) }
  }
  return <div className="space-y-1">
    <div className="flex min-w-0 items-start gap-2 rounded-lg border border-border bg-background p-2.5">
      <code aria-label={label} className="min-w-0 flex-1 whitespace-pre-wrap break-words font-mono text-xs leading-6 text-foreground">{text}</code>
      <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" aria-label={`Copy ${label}`} onClick={() => void copy()}>{copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}</Button>
    </div>
    {(copied || error) && <p role="status" className={`text-xs ${error ? 'text-destructive' : 'text-muted-foreground'}`}>{error ? 'Could not copy. Select the command and copy it manually.' : 'Copied'}</p>}
  </div>
}

function Step({ number, title, children }: { number: number; title: string; children: ReactNode }) {
  return <section className="flex gap-3 py-5 first:pt-0 last:pb-0">
    <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">{number}</span>
    <div className="min-w-0 flex-1 space-y-2"><h3 className="text-sm font-semibold text-foreground">{title}</h3>{children}</div>
  </section>
}

/** The person installs and attaches; these instructions never attach a chat automatically. */
export function AgentMessagingSetupPanel({ onOpenMcp }: { onOpenMcp: () => void }) {
  const [clientID, setClientID] = useState<string>('codex')
  const [platform, setPlatform] = useState<'unix' | 'windows'>('unix')
  const client = clients.find(item => item.id === clientID)!
  const origin = (getApiBaseUrl() || window.location.origin).replace(/\/+$/, '')
  const quote = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`
  const local = (() => { try { return ['localhost', '127.0.0.1', '::1'].includes(new URL(origin).hostname.replace(/^\[|\]$/g, '')) } catch { return false } })()
  const install = platform === 'unix'
    ? `curl -fsSL ${quote(`${origin}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${quote(origin)} --no-login`
    : `& ([scriptblock]::Create((irm ${quote(`${origin}/api/downloads/cli/install-agentworks.ps1`)}))) -Server ${quote(origin)} -NoLogin`
  return <section aria-label="Coding agent setup" className="space-y-6">
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2"><span className="rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">Coding agents</span><span className="text-xs text-muted-foreground">Install once. Attach each chat.</span></div>
      <h2 className="text-2xl font-semibold tracking-tight text-foreground">Connect your AI agent</h2>
      <p className="max-w-2xl text-sm leading-6 text-muted-foreground">Give a chat an AgentWorks name so your other agents can find it, send messages and reply. You choose which chats are attached.</p>
    </div>
    <div className="flex items-start gap-3 rounded-lg border border-border bg-muted/30 p-3">
      <Laptop className="mt-0.5 h-4 w-4 shrink-0 text-primary" /><div className="min-w-0"><p className="text-xs font-medium text-foreground">{local ? 'This local server' : 'This AgentWorks server'}</p><code aria-label="Selected AgentWorks server" className="break-all text-xs text-muted-foreground">{origin}</code><p className="mt-1 text-xs text-muted-foreground">{local ? 'For agents on this computer. ' : ''}{client.id === 'muse' ? 'Muse plugin installation uses GitHub. Sign in to this server before attaching your chat.' : 'The commands below sign in to this server. A previous login to another server is a separate connection.'}</p></div>
    </div>
    <div><h3 className="mb-2 text-sm font-medium text-foreground">Choose your coding agent</h3><div role="group" aria-label="Coding agent" className="grid grid-cols-2 gap-2 sm:grid-cols-3">
      {clients.map(item => <button key={item.id} type="button" aria-pressed={clientID === item.id} onClick={() => setClientID(item.id)} className={`rounded-lg border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${clientID === item.id ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'}`}>
        <span className="flex items-center justify-between gap-2 text-sm font-medium text-foreground"><span>{item.name}</span>{clientID === item.id && <Check className="h-3.5 w-3.5 text-primary" />}</span><span className="mt-1 block text-xs text-muted-foreground">{item.live ? 'Live messaging plugin' : item.id === 'muse' ? 'Messaging through hooks' : 'Other connection methods'}</span>
      </button>)}
    </div></div>
    {client.id === 'muse' ? <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_15rem]">
      <div className="min-w-0 space-y-5 rounded-xl border border-border p-5">
        <div className="space-y-2"><h3 className="font-semibold text-foreground">Install the Muse plugin from GitHub</h3><p className="text-xs leading-5 text-muted-foreground">Use Muse 1.4.4 or a compatible release and Python 3. Update the AgentWorks CLI before approving the plugin: it must support Muse hooks and <code>agentworks plugin mcp muse</code>.</p></div>
        <div className="divide-y divide-border">
          <Step number={1} title="Sign in to this server"><Command label="Sign in to this server" text={`agentworks --server ${quote(origin)} login`} /><p className="text-xs leading-5 text-muted-foreground">Run in Terminal and approve the CLI login in your browser. Already signed in to this server? Skip this step. Local servers use the same login flow; signing in to another server is a separate connection.</p></Step>
          <Step number={2} title="Add the AgentWorks marketplace"><Command label="Add Muse marketplace" text="muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks.git" /></Step>
          <Step number={3} title="Install the Muse plugin"><Command label="Install Muse plugin" text="muse plugins install agentworks-muse@agentworks" /><p className="text-xs leading-5 text-muted-foreground">Run in Terminal. Choose agentworks-muse. The same marketplace also contains the Codex package.</p></Step>
          <Step number={4} title="Review and approve the plugin"><Command label="Inspect Muse plugin" text="muse plugins inspect agentworks-muse --json" /><p className="text-xs leading-5 text-muted-foreground">Review the three hooks and the AgentWorks messaging connection. With a compatible AgentWorks CLI installed, approve them yourself:</p><Command label="Approve Muse plugin" text="muse plugins approve agentworks-muse --json" /><p className="text-xs leading-5 text-muted-foreground">Installation alone does not approve the plugin or attach a chat.</p></Step>
          <Step number={5} title="Restart Muse and attach this chat"><p className="text-xs leading-5 text-muted-foreground">Restart Muse to load the approved plugin. Review the workspace and choose “Trust and continue” when prompted. Type this yourself in the Muse chat:</p><Command label="Attach this chat" text="/agentworks-muse:agentworks attach muse-review" /><p className="text-xs leading-5 text-muted-foreground">Replace muse-review with your chosen name, or omit it for an automatic name. Attach also reconnects the same identity. Every sender in your AgentWorks account can message this chat.</p></Step>
          <Step number={6} title="Try a message"><Command label="Ask an agent to send a message" text="Send a message through AgentWorks to agent:muse-review: “Hello — please reply here.”" /><p className="text-xs leading-5 text-muted-foreground">Ask another connected agent to send it, using the name returned by attach. Submit a turn in Muse to receive waiting messages through its hooks. Stored means accepted by the server; admitted means the receiving agent saw it; processed means it handled it.</p></Step>
        </div>
      </div>
      <aside className="space-y-4 text-xs leading-5">
        <div className="space-y-2 rounded-lg border border-border bg-muted/30 p-4"><MessageSquare className="h-4 w-4 text-primary" /><h3 className="font-semibold text-foreground">How Muse messages arrive</h3><p className="text-muted-foreground">Hooks deliver at turn boundaries. Native push into an idle Muse chat is not available; a notification may tell you a message is waiting. A notification alone does not mean the chat received it.</p><h3 className="font-semibold text-foreground">Manage this chat</h3>{['attach', 'detach', 'agents'].map(command => <code key={command} className="block break-all text-foreground">/agentworks-muse:agentworks {command}</code>)}</div>
        <details className="space-y-2 rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium text-foreground">Updating an installed Muse plugin</summary><p className="pt-2 text-muted-foreground">Muse 1.4.4 can retain an old marketplace snapshot. Refresh it, remove the installed package, then reinstall. This remove command preserves plugin data. Inspect and approve the updated plugin, then restart Muse.</p><Command label="Update Muse plugin" text={'muse plugins marketplace update agentworks\nmuse plugins remove agentworks-muse\nmuse plugins install agentworks-muse@agentworks'} /></details>
        <details className="space-y-2 rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium text-foreground">Why did agentworks attach muse fail?</summary><p className="pt-2 text-muted-foreground">That error comes from the older installer, which only supports Claude and Codex. Update the AgentWorks CLI. In the updated CLI, attach takes a chat name: muse names the agent, rather than selecting a provider. Install with the marketplace commands, then attach directly in the Muse chat as shown here.</p></details>
        <details className="space-y-2 rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium text-foreground">Muse fails to start after approval</summary><p className="pt-2 text-muted-foreground">An older AgentWorks CLI can cause “Required MCP server agentworks failed during startup: the transport closed unexpectedly”. Disable the plugin, update the CLI to a version with Muse support, then inspect and approve the plugin again before restarting Muse.</p><Command label="Disable Muse plugin" text="muse plugins disable agentworks-muse" /></details>
        <Button variant="outline" size="sm" className="w-full justify-between" onClick={onOpenMcp}>Use workflow and Crew tools<ArrowRight className="h-3.5 w-3.5" /></Button>
      </aside>
    </div> : client.live ? <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_15rem]">
      <div className="min-w-0 divide-y divide-border rounded-xl border border-border p-5">
        <Step number={1} title="Install the AgentWorks CLI">
          <p className="text-xs leading-5 text-muted-foreground">Already installed? Start at step 2. Keep your coding CLI installed and available on PATH.</p>
          <div role="group" aria-label="Install platform" className="flex gap-1"><Button variant={platform === 'unix' ? 'secondary' : 'ghost'} size="sm" aria-pressed={platform === 'unix'} onClick={() => setPlatform('unix')}>macOS / Linux</Button><Button variant={platform === 'windows' ? 'secondary' : 'ghost'} size="sm" aria-pressed={platform === 'windows'} onClick={() => setPlatform('windows')}>Windows</Button></div>
          <Command label="Install AgentWorks" text={install} />
        </Step>
        <Step number={2} title="Sign in to this server">
          <Command label="Sign in to this server" text={`agentworks --server ${quote(origin)} login`} /><p className="text-xs leading-5 text-muted-foreground">Approve the CLI in your browser, then return to Terminal. This login is shared by the AgentWorks bridge on your computer.</p>
        </Step>
        <Step number={3} title="Set up or reconnect AgentWorks">
          <Command label="Install coding agent plugin" text="agentworks attach" /><p className="text-xs leading-5 text-muted-foreground">Run in your terminal. Attach detects supported live chats and lets you choose one. It installs the plugin if needed. Review the hooks and reload {client.name} when prompted.{client.id === 'codex' ? ' In Codex, use /hooks to check hook trust.' : ' Enable Claude session messaging for native delivery; otherwise messages arrive through hooks.'}</p>
        </Step>
        <Step number={4} title="Attach the chat you want to use">
          <p className="text-xs leading-5 text-muted-foreground">Type this yourself in the coding-agent chat. Replace the example name with a unique name you want other agents to use.</p>
          <Command label="Attach this chat" text={`/agentworks attach ${client.id}-review`} /><p className="text-xs leading-5 text-muted-foreground">Or type <code>/agentworks attach</code> to choose a short name automatically. The same command reconnects your saved identity after a restart. Your agent sets its purpose afterward. Every sender in your AgentWorks account can message this chat.</p>
        </Step>
        <Step number={5} title="Try a message">
          <Command label="Ask an agent to send a message" text={`Send a message through AgentWorks to agent:${client.id}-review: “Hello — please reply here.”`} /><p className="text-xs leading-5 text-muted-foreground">Ask another connected agent to send it. Stored means accepted by the server; admitted means the receiving agent saw it; processed means it handled it.</p>
        </Step>
      </div>
      <aside className="space-y-4 text-xs leading-5">
        <div className="rounded-lg border border-border p-4"><MessageSquare className="mb-2 h-4 w-4 text-primary" /><h3 className="font-semibold text-foreground">How messages arrive</h3><p className="mt-2 text-muted-foreground">{client.detail}. Native delivery can reach a running chat. Hooks deliver at turn boundaries. Mailbox reads are recovery when a live route is unavailable.</p></div>
        <div className="space-y-2 rounded-lg bg-muted/30 p-4"><h3 className="font-semibold text-foreground">Manage this chat</h3>{['/agentworks attach', '/agentworks detach', '/agentworks agents'].map(command => <code key={command} className="block break-all text-foreground">{command}</code>)}<p className="text-muted-foreground">Attach also reconnects. Agents shows who is online and how delivery works. Detach stops delivery and revokes its connection.</p></div>
        <details className="rounded-lg border border-border p-4"><summary className="cursor-pointer font-medium text-foreground">Setup troubleshooting</summary><div className="mt-3 space-y-3 text-muted-foreground"><p>If attach returns unknown_tool, check which server you signed in to. The server must support agent messaging; update it before the CLI.</p><p>If the command reaches the model, reload the plugin and review its hooks. The agent cannot attach on your behalf.</p><p>Already-attached chats from before the sender-policy update need <code>/agentworks attach</code>.</p><p>For clickable macOS fallback notifications, install <code>brew install terminal-notifier</code> and allow its notifications in macOS settings. Native delivery remains the primary route.</p></div></details>
        <Button variant="outline" size="sm" className="w-full justify-between" onClick={onOpenMcp}>Add workflow and Crew tools<ArrowRight className="h-3.5 w-3.5" /></Button>
      </aside>
    </div> : <div className="space-y-3 rounded-xl border border-border p-5"><Terminal className="h-5 w-5 text-primary" /><h3 className="font-semibold text-foreground">{client.name} connection options</h3><p className="text-sm text-muted-foreground">{client.detail}. The shared installer is not available in this release for this provider.</p><p className="text-xs leading-5 text-muted-foreground">{client.id === 'cursor' ? 'Connect Cursor over HTTP MCP for workflow and Crew tools. Its live messaging adapter has separate setup; idle desktop push is not available.' : 'Use the provider’s AgentWorks adapter when available. A registered mailbox alone does not push messages into a running session.'}</p><Button variant="outline" size="sm" onClick={onOpenMcp}>Open MCP setup<ArrowRight className="ml-2 h-3.5 w-3.5" /></Button></div>}
  </section>
}
