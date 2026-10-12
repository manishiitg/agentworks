import { useState, useEffect, type ReactNode } from 'react'
import { ArrowRight, Check, Copy, Laptop, MessageSquare } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { getApiBaseUrl } from '../../services/api'

import { messagingGuides, type MessagingClient } from './agentMessagingSetup'

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

/** Instructions only; no installation, login or attachment runs from this page. */
export function AgentMessagingSetupPanel({ onOpenMcp }: { onOpenMcp: () => void }) {
  const [clientID, setClientID] = useState<MessagingClient>('codex')
  const [platform, setPlatform] = useState<'unix' | 'windows'>('unix')
  const client = messagingGuides.find(item => item.id === clientID)!
  const origin = (getApiBaseUrl() || window.location.origin).replace(/\/+$/, '')
  const quote = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`
  const local = (() => { try { return ['localhost', '127.0.0.1', '::1'].includes(new URL(origin).hostname.replace(/^\[|\]$/g, '')) } catch { return false } })()
  const install = platform === 'unix'
    ? `curl -fsSL ${quote(`${origin}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${quote(origin)} --no-login`
    : `& ([scriptblock]::Create((irm ${quote(`${origin}/api/downloads/cli/install-agentworks.ps1`)}))) -Server ${quote(origin)} -NoLogin`
  return <section aria-label="Coding agent setup" className="space-y-6">
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2"><span className="rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">Coding agents</span><span className="text-xs text-muted-foreground">Install once. Attach each chat.</span></div>
      <h2 className="text-2xl font-semibold tracking-tight">Connect your AI agent</h2>
      <p className="max-w-2xl text-sm leading-6 text-muted-foreground">Choose your CLI for its requirements and exact setup. You decide which chat connects; the agent handles messages and receipts.</p>
    </div>
    <div className="flex items-start gap-3 rounded-lg border border-border bg-muted/30 p-3">
      <Laptop className="mt-0.5 h-4 w-4 shrink-0 text-primary" /><div className="min-w-0"><p className="text-xs font-medium">{local ? 'This local server' : 'This AgentWorks server'}</p><code aria-label="Selected AgentWorks server" className="break-all text-xs text-muted-foreground">{origin}</code><p className="mt-1 text-xs leading-5 text-muted-foreground">Messages and purposes are stored on the connected server. Signing in does not move existing chat attachments. Named connections and chat switching are planned.</p></div>
    </div>
    <div><h3 className="mb-2 text-sm font-medium">Choose your coding agent</h3><div role="group" aria-label="Coding agent" className="grid grid-cols-2 gap-2 sm:grid-cols-3">
      {messagingGuides.map(item => <button key={item.id} type="button" aria-label={item.name} aria-pressed={clientID === item.id} onClick={() => setClientID(item.id)} className={`rounded-lg border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${clientID === item.id ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/40'}`}>
        <span className="flex items-center justify-between gap-2 text-sm font-medium"><span>{item.name}</span>{clientID === item.id && <Check className="h-3.5 w-3.5 text-primary" />}</span><span className="mt-1 block text-xs leading-5 text-muted-foreground">{item.status}</span>
      </button>)}
    </div></div>
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_15rem]">
      <div key={client.id} className="min-w-0 rounded-xl border border-border p-5">
        <h3 className="mb-5 font-semibold">{client.name} setup</h3>
        <div className="divide-y divide-border">
          <Step number={1} title="Requirements and tested versions">
            {client.requirements.map(text => <p key={text} className="text-xs leading-5 text-muted-foreground">{text}</p>)}
            <p className="text-xs leading-5 text-muted-foreground">{client.versions}</p>
          </Step>
          <Step number={2} title="Install or update the plugin">
            {client.available && <><p className="text-xs leading-5 text-muted-foreground">Install the AgentWorks CLI first, or update it from a server that publishes the required messaging build.</p><div role="group" aria-label="Install platform" className="flex flex-wrap gap-1"><Button variant={platform === 'unix' ? 'secondary' : 'ghost'} size="sm" aria-pressed={platform === 'unix'} onClick={() => setPlatform('unix')}>macOS / Linux</Button><Button variant={platform === 'windows' ? 'secondary' : 'ghost'} size="sm" aria-pressed={platform === 'windows'} onClick={() => setPlatform('windows')}>Windows</Button></div><Command label="Install AgentWorks" text={install} /><Command label="Update AgentWorks CLI" text={`agentworks --server ${quote(origin)} update`} /><p className="text-xs leading-5 text-muted-foreground">The Windows installer does not establish provider IPC compatibility; check the requirements above.</p></>}
            {client.install.map(command => <Command key={command.label} {...command} />)}
            <p className="text-xs leading-5 text-muted-foreground">{client.installNote}</p>
            {client.update.length > 0 && <details className="space-y-2"><summary className="cursor-pointer text-sm font-medium">Updating an older installation</summary><div className="space-y-2 pt-2">{client.update.map(command => <Command key={command.label} {...command} />)}<p className="text-xs leading-5 text-muted-foreground">{client.approval}</p><p className="text-xs leading-5 text-muted-foreground">Older installed skills may only allow the typed attach command. Update the CLI and plugin, then reload the provider before asking an agent to attach itself.</p></div></details>}
            <p className="text-xs leading-5 text-muted-foreground">{client.approval}</p>
          </Step>
          <Step number={3} title="Sign in to this server">
            {client.available ? <><Command label="Sign in to this server" text={`agentworks --server ${quote(origin)} login`} /><p className="text-xs leading-5 text-muted-foreground">Run in Terminal and approve the CLI login in your browser. Check the server address before approval. This changes the saved default connection for future attaches; it does not move existing attached chats.</p></> : <p className="text-xs leading-5 text-muted-foreground">A separate approved MCP tools connection can sign in to this server. It does not establish {client.name} live messaging.</p>}
          </Step>
          <Step number={4} title="Connect this chat">
            {client.available ? <><p className="text-xs leading-5 text-muted-foreground">Type this yourself in the chat you want to connect. Choose a stable name, or omit it for an automatic name.</p><Command label="Attach this chat" text={client.attach} />{client.id === 'pi' && <><p className="text-xs leading-5 text-muted-foreground">If the Pi extension is missing, first run this in Terminal, accept the bundled/pinned install, reload Pi, then type the chat command again:</p><Command label="Install Pi through terminal attach" text="agentworks attach pi-review" /></>}<p className="text-xs leading-5 text-muted-foreground">Or ask the agent to attach itself with the current CLI and integration loaded:</p><Command label="Connect this chat" text={client.ownRequest} /><p className="text-xs leading-5 text-muted-foreground">Use the current AgentWorks CLI and plugin or extension, available from 12 Oct 2026. Muse additionally needs the CLI’s Monitor support. It does not install a plugin or select another chat. If an older skill or sandbox refuses, use the typed command. Every sender in your AgentWorks account can message this chat; the agent sets its own purpose, up to 512 characters.</p></> : <p className="text-xs leading-5 text-muted-foreground">{client.id === 'cursor' ? 'Cursor is preview: attach is not available yet. Asking the agent to attach itself is not a supported workaround.' : 'No supported agy live attachment is published. Asking the agent to attach itself does not supply a missing adapter.'}</p>}
          </Step>
          <Step number={5} title="What appears in your CLI menu">
            <div className="space-y-3">{client.menus.map(item => <div key={item.entry} className="space-y-1"><code className="block break-words text-xs">{item.entry}</code><p className="text-xs leading-5 text-muted-foreground"><strong className="font-medium">{item.who}: </strong>{item.description}</p></div>)}</div>
          </Step>
          <Step number={6} title="Try a message">
            {client.available ? <><Command label="Ask an agent to send a message" text={`Send a message through AgentWorks to agent:${client.id}-review: “Hello — please reply here.”`} /><p className="text-xs leading-5 text-muted-foreground">Ask another connected agent to send it, using the actual address returned by attach. Look for a reply in the same conversation and an admission receipt.</p></> : <p className="text-xs leading-5 text-muted-foreground">Try live messaging with a supported connected CLI. A successful workflow/Crew tool call does not prove that this {client.name} chat receives messages.</p>}
            <p className="text-xs leading-5 text-muted-foreground">{client.delivery}</p>
          </Step>
          <Step number={7} title="Troubleshooting">
            {client.available && <><p className="text-xs leading-5 text-muted-foreground"><strong>Offline: </strong>re-attach in this chat with the typed command. CLI stop/start/update restores still-live attachments; a provider process restart can require a new attach. A stored message is not proof of a live route.</p><p className="text-xs leading-5 text-muted-foreground"><strong>Cannot identify this exact provider process: </strong>send one message in the chat so its hook sees the session, then attach again.</p><p className="text-xs leading-5 text-muted-foreground"><strong>unknown_tool: </strong>check the signed-in server. The server must expose messaging before updating the CLI; ask its administrator to update an older deployment.</p></>}
            {client.troubleshooting.map(item => <details key={item.title} className="space-y-2"><summary className="cursor-pointer text-sm font-medium">{item.title}</summary><p className="pt-2 text-xs leading-5 text-muted-foreground">{item.text}</p></details>)}
          </Step>
        </div>
      </div>
      <aside className="space-y-4 text-xs leading-5">
        <div className="space-y-2 rounded-lg border border-border bg-muted/30 p-4"><MessageSquare className="h-4 w-4 text-primary" /><h3 className="font-semibold">How {client.name} messages arrive</h3><p className="text-muted-foreground">{client.delivery}</p></div>
        <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="font-semibold">Delivery receipts</h3><p className="text-muted-foreground"><strong>Stored:</strong> the server accepted it. <strong>Admitted:</strong> the receiving side acknowledged receipt. <strong>Processed:</strong> the receiving agent handled it. A menu entry, notification or native display alone does not prove processing.</p></div>
        <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="font-semibold">AgentWorks tools</h3><p className="text-muted-foreground">Workflow and Crew tools use a separate MCP connection. HTTP MCP setup is tool access; it does not create a coding-chat messaging attachment.</p><Button variant="outline" size="sm" className="w-full justify-between" onClick={onOpenMcp}>Open MCP tools setup<ArrowRight className="h-3.5 w-3.5" /></Button></div>
      </aside>
    </div>
  </section>
}
