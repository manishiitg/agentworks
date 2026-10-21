import { useState, useEffect, type ReactNode } from 'react'
import { ArrowRight, Check, Copy, Laptop, MessageSquare } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { getApiBaseUrl } from '../../services/api'

import { messagingGuides, type MessagingClient } from './agentMessagingSetup'
import { AI_APPS, AiAppPicker, isHostedAiApp, type AiApp } from '../../components/integrations/AiAppPicker'
import { HostedAiAppGuide } from '../../components/integrations/HostedAiAppGuide'
import { HowBullpenWorks } from './HowBullpenWorks'

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
  const [app, setApp] = useState<AiApp | 'pi' | 'agy' | null>(null)
  const origin = (getApiBaseUrl() || window.location.origin).replace(/\/+$/, '')
  const common = app === 'pi' || app === 'agy' ? null : app
  const hosted = isHostedAiApp(common)
  const coding = app === 'claude-code' ? 'claude' : app
  return <section aria-label="AgentBridge setup" className="max-w-4xl space-y-5">
    <div><h2 className="text-xl font-semibold">Let your agents work together</h2><p className="mt-1 text-sm text-muted-foreground">Connect a chat or app inbox, then send messages between your agents.</p></div>
    <AiAppPicker value={common} onChange={setApp} extraCodingChoices={<div className="mt-2 flex gap-2"><Button variant="outline" size="sm" aria-pressed={app === 'pi'} onClick={() => setApp('pi')}>Pi</Button><Button variant="outline" size="sm" aria-pressed={app === 'agy'} onClick={() => setApp('agy')}>agy</Button></div>} />
    {hosted ? <><HostedAiAppGuide app={common} endpoint={`${origin}/api/external/v1/mcp`} localServer={/^https?:\/\/(localhost|127\.0\.0\.1|\[::1\])(?::|\/|$)/.test(origin)} /><div className="space-y-2 rounded-lg border border-border p-4"><h3 className="text-sm font-medium">After connecting, ask your app</h3><Command label="Register an app inbox" text="Use your AgentWorks messages tool to register an inbox for this chat, then show me which agents I can message." /><p className="text-xs text-muted-foreground">App inboxes receive messages when the app checks them. Keep the same inbox for replies.</p><Button variant="outline" size="sm" onClick={onOpenMcp}>Open AI app connections</Button></div></>
      : app === 'json-client' ? <div className="space-y-3 rounded-lg border border-border p-4"><p className="text-sm">Connect your app to AgentWorks, then use its messages tool to register an inbox.</p><Button onClick={onOpenMcp}>Open AI app connections</Button></div>
        : coding && <CodingMessagingGuide key={coding} clientID={coding as MessagingClient} />}
  </section>
}

function CodingMessagingGuide({ clientID }: { clientID: MessagingClient }) {
  const [platform, setPlatform] = useState<'unix' | 'windows'>('unix')
  const [codexMode, setCodexMode] = useState<'app' | 'cli'>('app')
  const [cursorMode, setCursorMode] = useState<'cli' | 'desktop'>('desktop')
  const guide = messagingGuides.find(item => item.id === clientID)!
  const client = clientID === 'codex' && codexMode === 'app' ? { ...guide, ...guide.app } : clientID === 'cursor' && cursorMode === 'desktop' ? { ...guide, ...guide.desktop } : guide
  const origin = (getApiBaseUrl() || window.location.origin).replace(/\/+$/, '')
  const quote = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`
  const local = (() => { try { return ['localhost', '127.0.0.1', '::1'].includes(new URL(origin).hostname.replace(/^\[|\]$/g, '')) } catch { return false } })()
  const install = platform === 'unix'
    ? `curl -fsSL ${quote(`${origin}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${quote(origin)} --no-login`
    : `& ([scriptblock]::Create((irm ${quote(`${origin}/api/downloads/cli/install-agentworks.ps1`)}))) -Server ${quote(origin)} -NoLogin`
  return <section aria-label="Coding agent setup" className="space-y-4">
    {clientID === 'codex' && <div className="space-y-2"><h3 className="text-sm font-medium">How do you use Codex?</h3><div role="group" aria-label="Codex app or CLI" className="flex flex-wrap gap-2"><Button variant={codexMode === 'app' ? 'secondary' : 'outline'} size="sm" aria-pressed={codexMode === 'app'} onClick={() => setCodexMode('app')}>Codex app</Button><Button variant={codexMode === 'cli' ? 'secondary' : 'outline'} size="sm" aria-pressed={codexMode === 'cli'} onClick={() => setCodexMode('cli')}>Codex CLI</Button></div></div>}
    {clientID === 'cursor' && <div role="group" aria-label="Cursor mode" className="flex flex-wrap gap-2"><Button variant={cursorMode === 'desktop' ? 'secondary' : 'outline'} size="sm" aria-pressed={cursorMode === 'desktop'} onClick={() => setCursorMode('desktop')}>Cursor app</Button><Button variant={cursorMode === 'cli' ? 'secondary' : 'outline'} size="sm" aria-pressed={cursorMode === 'cli'} onClick={() => setCursorMode('cli')}>Cursor CLI</Button></div>}
    <div className="space-y-4">
      <div key={client.id + cursorMode + codexMode} className="min-w-0 rounded-xl border border-border p-5">
        <h3 className="mb-5 font-semibold">{client.name} setup</h3>
        <div className="divide-y divide-border">
          <details className="py-4"><summary className="cursor-pointer text-sm font-semibold">1. Set up this app once</summary><div className="space-y-4 pt-4"><h4 className="text-sm font-medium">Requirements</h4>
            {client.requirements.map(text => <p key={text} className="text-xs leading-5 text-muted-foreground">{text}</p>)}
            <details className="text-xs leading-5 text-muted-foreground"><summary className="cursor-pointer text-primary">Tested versions</summary><p className="mt-2">{client.versions}</p></details>
          <h4 className="text-sm font-medium">Install or update</h4>
            {client.available && <><p className="text-xs leading-5 text-muted-foreground">Install the AgentWorks CLI first, or update it from a server that publishes the required messaging build.</p><div role="group" aria-label="Install platform" className="flex flex-wrap gap-1"><Button variant={platform === 'unix' ? 'secondary' : 'ghost'} size="sm" aria-pressed={platform === 'unix'} onClick={() => setPlatform('unix')}>macOS / Linux</Button><Button variant={platform === 'windows' ? 'secondary' : 'ghost'} size="sm" aria-pressed={platform === 'windows'} onClick={() => setPlatform('windows')}>Windows</Button></div><Command label="Install AgentWorks" text={install} /><Command label="Update AgentWorks CLI" text={`agentworks --server ${quote(origin)} update`} /><p className="text-xs leading-5 text-muted-foreground">The Windows installer does not establish provider IPC compatibility; check the requirements above.</p></>}
            {client.installSteps && <ol aria-label="Install in the Codex app" className="list-decimal space-y-2 pl-5 text-xs leading-5 text-muted-foreground">{client.installSteps.map(text => <li key={text}>{text}</li>)}</ol>}
            {client.install.map(command => <Command key={command.label} {...command} />)}
            <p className="text-xs leading-5 text-muted-foreground">{client.installNote}</p>
            {client.alternativeInstall && <details className="space-y-2"><summary className="cursor-pointer text-sm font-medium">Terminal alternative after sign-in</summary><div className="space-y-2 pt-2">{client.alternativeInstall.map(command => <Command key={command.label} {...command} />)}<p className="text-xs leading-5 text-muted-foreground">{client.alternativeNote}</p></div></details>}
            {(client.update.length > 0 || client.updateSteps) && <details className="space-y-2"><summary className="cursor-pointer text-sm font-medium">Updating an older installation</summary><div className="space-y-2 pt-2">{client.updateSteps && <ol className="list-decimal space-y-2 pl-5 text-xs leading-5 text-muted-foreground">{client.updateSteps.map(text => <li key={text}>{text}</li>)}</ol>}{client.update.map(command => <Command key={command.label} {...command} />)}<p className="text-xs leading-5 text-muted-foreground">{client.approval}</p><p className="text-xs leading-5 text-muted-foreground">Older installed skills may only allow the typed attach command. Update the CLI and plugin, then reload the provider before asking an agent to attach itself.</p></div></details>}
            <p className="text-xs leading-5 text-muted-foreground">{client.approval}</p>
          <h4 className="text-sm font-medium">Sign in to this server</h4>
            {client.available ? <><Command label="Sign in to this server" text={`agentworks --server ${quote(origin)} login`} /><p className="text-xs leading-5 text-muted-foreground">Run in Terminal and approve the CLI login in your browser. Check the server address before approval. This changes the saved default connection for future attaches; it does not move existing attached chats.</p><details><summary className="cursor-pointer text-xs font-medium">Multiple servers & saved logins</summary><div className="mt-2 space-y-2"><Command label="Save a named login" text={`agentworks login --server ${quote(origin)} --name NAME`} /><Command label="List saved logins" text="agentworks logins" /><Command label="Remove a named login" text="agentworks logout --name NAME" /><p className="text-xs leading-5 text-muted-foreground">A named login is separate from the default and from other named logins. Existing chats stay on their current login until you move them.</p></div></details></> : <p className="text-xs leading-5 text-muted-foreground">A separate approved MCP tools connection can sign in to this server. It does not establish {client.name} live messaging.</p>}
          </div></details>
          <Step number={2} title="Connect this chat">
            {client.available ? <>{client.ownRequest && <><p className="text-xs leading-5 text-muted-foreground">Ask your agent in plain words (no typing of commands needed):</p><Command label="Connect this chat" text={client.ownRequest} /><p className="text-xs leading-5 text-muted-foreground">You can also say “publish yourself to the network”, “which agents can you see?” or “ping them if they have not answered”. Keep the app running so AgentWorks can reconnect after a server restart. Windows Codex checks through its approved hooks when a chat starts or a turn runs. If it remains offline, check setup and sign-in before reconnecting the chat.</p></>}<p className="text-xs leading-5 text-muted-foreground">Or type this yourself in the chat you want to connect (a shortcut that also works when the agent cannot). Choose a stable name, or omit it for an automatic name.</p><Command label="Attach this chat" text={client.attach} />{client.id === 'pi' && <><p className="text-xs leading-5 text-muted-foreground">If the Pi extension is missing, first run this in Terminal, accept the bundled/pinned install, reload Pi, then type the chat command again:</p><Command label="Install Pi through terminal attach" text="agentworks attach pi-review" /></>}<p className="text-xs leading-5 text-muted-foreground">Use the current AgentWorks CLI and plugin or extension, available from 12 Oct 2026. Muse additionally needs the CLI’s Monitor support. It does not install a plugin or select another chat. If an older skill or sandbox refuses, use the typed command. Every sender in your AgentWorks account can message this chat; the agent sets its own purpose, up to 512 characters.</p><details><summary className="cursor-pointer text-xs font-medium">Move between servers</summary><div className="mt-2 space-y-2"><Command label="Move this chat to another login" text="/agentworks attach [NAME] --on LOGIN" /><p className="text-xs leading-5 text-muted-foreground">Moving keeps the chat’s name unless you give a new one, and warns when unread messages are waiting on the old login. To move every attached chat at once, run <code>agentworks attach --all --on LOGIN</code> in your own terminal; it lists the chats and asks you to retype the count.</p></div></details></> : <p className="text-xs leading-5 text-muted-foreground">{client.id === 'cursor' ? 'Cursor desktop: attach is not available yet; use Cursor CLI above. Asking the agent to attach itself is not a supported workaround.' : 'No supported agy live attachment is published. Asking the agent to attach itself does not supply a missing adapter.'}</p>}
          </Step>
          <details className="py-4"><summary className="cursor-pointer text-sm font-medium">Commands and menus</summary><div className="pt-3">
            <div className="space-y-3">{client.menus.map(item => <div key={item.entry} className="space-y-1"><code className="block break-words text-xs">{item.entry}</code><p className="text-xs leading-5 text-muted-foreground"><strong className="font-medium">{item.who}: </strong>{item.description}</p></div>)}</div>
          </div></details>
          <Step number={3} title="Try a message">
            {client.available ? <><Command label="Ask an agent to send a message" text={`Send a message through AgentWorks to agent:${client.id}-review: “Hello — please reply here.”`} /><p className="text-xs leading-5 text-muted-foreground">Ask another connected agent to send it, using the actual address returned by attach. Look for a reply in the same conversation and an admission receipt.</p></> : <p className="text-xs leading-5 text-muted-foreground">Try live messaging with a supported connected CLI. A successful workflow/Crew tool call does not prove that this {client.name} chat receives messages.</p>}
            <p className="text-xs leading-5 text-muted-foreground">{client.delivery}</p>
          </Step>
          <details className="py-4"><summary className="cursor-pointer text-sm font-medium">Troubleshooting</summary><div className="space-y-3 pt-3">
            {client.available && <><p className="text-xs leading-5 text-muted-foreground"><strong>Offline: </strong>re-attach in this chat with the typed command. CLI stop/start/update restores still-live attachments; a provider process restart can require a new attach. A stored message is not proof of a live route.</p><p className="text-xs leading-5 text-muted-foreground"><strong>Cannot identify this exact provider process: </strong>send one message in the chat so its hook sees the session, then attach again.</p><p className="text-xs leading-5 text-muted-foreground"><strong>unknown_tool: </strong>check the signed-in server. The server must expose messaging before updating the CLI; ask its administrator to update an older deployment.</p></>}
            {client.troubleshooting.map(item => <details key={item.title} className="space-y-2"><summary className="cursor-pointer text-sm font-medium">{item.title}</summary><p className="pt-2 text-xs leading-5 text-muted-foreground">{item.text}</p></details>)}
          </div></details>
        </div>
        <details className="border-t border-border pt-4"><summary className="cursor-pointer text-sm font-medium">How AgentBridge works</summary><HowBullpenWorks /></details>
      </div>
      <details className="rounded-lg border border-border p-4 text-xs leading-5"><summary className="cursor-pointer text-sm font-medium">Delivery, usage & connection details</summary><div className="mt-3 space-y-4">
        <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="font-semibold">Optional token usage</h3><p className="text-muted-foreground">Off by default. In your own terminal, run <code>agentworks usage on</code>, <code>agentworks usage off</code>, or <code>agentworks usage status</code>. Attach does not enable it. Supported CLIs report their recorded numbers hourly to your account only; shared recipients cannot see them. AgentBridge keeps hourly history for up to 90 days, with a Delete saved history control beside the off command.</p></div>
        <div className="space-y-2 rounded-lg border border-border bg-muted/30 p-4"><MessageSquare className="h-4 w-4 text-primary" /><h3 className="font-semibold">How {client.name} messages arrive</h3><p className="text-muted-foreground">{client.delivery}</p></div>
        <div className="space-y-2 rounded-lg border border-border p-4"><h3 className="font-semibold">Delivery receipts</h3><p className="text-muted-foreground"><strong>Stored:</strong> the server accepted it. <strong>Admitted:</strong> the receiving side acknowledged receipt. <strong>Processed:</strong> the receiving agent handled it. A menu entry, notification or native display alone does not prove processing.</p></div>
      </div></details>
    </div>
  </section>
}
