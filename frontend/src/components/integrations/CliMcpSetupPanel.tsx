import { useEffect, useState } from 'react'
import { Check, Copy, Download, Globe, Plug, Terminal } from 'lucide-react'
import { SettingsCard } from '../ui/SettingsCard'
import { Button } from '../ui/Button'
import api, { externalSkillApi, getApiBaseUrl } from '../../services/api'

type Destination = 'local-assistant' | 'hosted-assistant'
type LocalClient = 'claude-code' | 'codex' | 'cursor' | 'muse' | 'json-client'
type HostedClient = 'chatgpt' | 'cowork'
interface OAuthConnection { id: string; client_name: string; scopes: string[]; expires_at: string }

function CopyButton({ text, label }: { text: string; label: string }) {
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

function CommandRow({ command, label }: { command: string; label: string }) {
  return <div className="flex items-center gap-2">
    <code className="min-w-0 flex-1 overflow-x-auto rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground" aria-label={label}>{command}</code>
    <CopyButton text={command} label={label} />
  </div>
}

function JsonBlock({ json, label, hint }: { json: string; label: string; hint: string }) {
  return <div className="space-y-1">
    <div className="flex items-center justify-between gap-2"><span className="text-xs font-medium text-foreground">{label}</span><CopyButton text={json} label={label} /></div>
    <pre className="overflow-x-auto rounded-md border border-border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground" aria-label={label}>{json}</pre>
    <p className="text-xs text-muted-foreground">{hint}</p>
  </div>
}

/** One connection path at a time. Browser approval keeps credentials out of setup instructions. */
export function CliMcpSetupPanel({ target = 'agentworks', endpoint }: { target?: 'agentworks' | 'vault'; endpoint?: string } = {}) {
  const serviceName = target === 'vault' ? 'Vault' : 'AgentWorks'
  const connectionPath = `/api/oauth/${target === 'vault' ? 'vault' : 'mcp'}/connections`
  const [destination, setDestination] = useState<Destination>('local-assistant')
  const [localClient, setLocalClient] = useState<LocalClient>('claude-code')
  const [hostedClient, setHostedClient] = useState<HostedClient>('chatgpt')
  const [connections, setConnections] = useState<OAuthConnection[]>([])
  const [connectionLoadError, setConnectionLoadError] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [skillBusy, setSkillBusy] = useState<'download' | 'copy' | 'plugin' | null>(null)
  const [skillMsg, setSkillMsg] = useState<string | null>(null)
  const [pluginMsg, setPluginMsg] = useState<string | null>(null)
  const origin = (getApiBaseUrl() || window.location.origin).replace(/\/+$/, '')
  const quoted = (value: string) => `'${value.replace(/'/g, `'\\''`)}'`
  const mcpUrl = endpoint ?? `${origin}/api/external/v1/mcp`
  const mcpJson = JSON.stringify({ mcpServers: { [target]: { url: mcpUrl } } }, null, 2)
  const museJson = JSON.stringify({ schema_version: 1, mcpServers: { [target]: { url: mcpUrl } } }, null, 2)
  const isLoopbackOrigin = (() => { try { return ['localhost', '127.0.0.1', '0.0.0.0', '::1'].includes(new URL(mcpUrl).hostname) } catch { return false } })()

  useEffect(() => {
    let cancelled = false
    api.get<{ connections: OAuthConnection[] }>(connectionPath)
      .then(({ data }) => {
        if (cancelled) return
        if (Array.isArray(data?.connections)) {
          setConnections(data.connections)
          return
        }
        setConnections([])
        setConnectionLoadError(true)
      })
      .catch(() => { /* Setup instructions remain available. */ })
    return () => { cancelled = true }
  }, [connectionPath])

  const revoke = async (id: string) => {
    setError(null)
    try {
      await api.delete(`${connectionPath}/${encodeURIComponent(id)}`)
      setConnections(current => current.filter(item => item.id !== id))
    } catch (err) { setError(err instanceof Error ? err.message : 'Could not revoke connection') }
  }

  const downloadSkill = async () => {
    setSkillBusy('download'); setSkillMsg(null)
    try {
      const blob = await externalSkillApi.downloadSkillZIP()
      const url = URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = url; anchor.download = 'agentworks-skill.zip'
        document.body.appendChild(anchor); anchor.click(); anchor.remove()
      } finally { URL.revokeObjectURL(url) }
      setSkillMsg('Downloaded agentworks-skill.zip')
    } catch { setSkillMsg('Download failed — try again.') }
    finally { setSkillBusy(null) }
  }

  const downloadCoworkPlugin = async () => {
    setSkillBusy('plugin'); setPluginMsg(null)
    try {
      const blob = await externalSkillApi.downloadCoworkPlugin()
      const url = URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = url; anchor.download = 'agentworks.plugin'
        document.body.appendChild(anchor); anchor.click(); anchor.remove()
      } finally { URL.revokeObjectURL(url) }
      setPluginMsg('Downloaded agentworks.plugin')
    } catch { setPluginMsg('Plugin download failed — try again.') }
    finally { setSkillBusy(null) }
  }

  const copySkill = async () => {
    setSkillBusy('copy'); setSkillMsg(null)
    try { await navigator.clipboard.writeText(await externalSkillApi.fetchSkillMD()); setSkillMsg('Skill text copied') }
    catch { setSkillMsg('Copy failed — try again.') }
    finally { setSkillBusy(null) }
  }

  const destinations = [
    { id: 'local-assistant', icon: Terminal, title: 'AI agent on this computer', description: 'Claude Code, Codex, Cursor, Muse, or another local MCP client.' },
    { id: 'hosted-assistant', icon: Globe, title: 'Hosted AI app', description: 'ChatGPT or Claude Cowork.' },
  ] as const

  return <div className="space-y-5">
    <div className="space-y-3">
      <div><h3 className="text-base font-semibold text-foreground">Connect an AI agent to {serviceName}</h3>
        <p className="mt-1 text-sm text-muted-foreground">Choose your app, add the MCP URL, and approve access in your browser.</p></div>
      {target === 'vault' && <div className="space-y-2">
        <p className="text-sm text-muted-foreground">Everyone uses this same MCP URL. Sign in with your own platform account; your user and group permissions determine which MCP tools and shared secrets you can access.</p>
        <CommandRow label="Vault MCP URL" command={mcpUrl} />
      </div>}
      <div className="grid gap-2" role="group" aria-label="Connection destination">
        {destinations.map(({ id, icon: Icon, title, description }) => <button key={id} type="button" aria-pressed={destination === id} onClick={() => setDestination(id)} className={`flex items-center gap-3 rounded-lg border p-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${destination === id ? 'border-primary bg-primary/5' : 'border-border hover:border-primary/50 hover:bg-muted/30'}`}>
          <Icon className="h-4 w-4 shrink-0 text-primary" /><span className="flex min-w-0 flex-col gap-0.5"><span className="text-sm font-medium text-foreground">{title}</span><span className="text-xs text-muted-foreground">{description}</span></span>
        </button>)}
      </div>
    </div>
    <SettingsCard icon={destination === 'local-assistant' ? <Plug className="h-4 w-4 text-primary" /> : <Globe className="h-4 w-4 text-primary" />}
      title={destination === 'local-assistant' ? 'Connect a local AI agent' : 'Connect a hosted AI app'}
      description={destination === 'local-assistant' ? `Connect directly to ${serviceName} over HTTP MCP. No AgentWorks binary is needed.` : 'Use the MCP URL in an AI app that connects from the cloud.'}>
      {destination === 'local-assistant' ? <div className="space-y-4">
        <div className="space-y-2">
          <p className="text-xs font-medium text-foreground">Choose your AI agent</p>
          <div className="flex flex-wrap gap-2" role="group" aria-label="Local MCP client">
            <Button variant={localClient === 'claude-code' ? 'default' : 'outline'} size="sm" aria-pressed={localClient === 'claude-code'} onClick={() => setLocalClient('claude-code')}>Claude Code</Button>
            <Button variant={localClient === 'codex' ? 'default' : 'outline'} size="sm" aria-pressed={localClient === 'codex'} onClick={() => setLocalClient('codex')}>Codex</Button>
            <Button variant={localClient === 'cursor' ? 'default' : 'outline'} size="sm" aria-pressed={localClient === 'cursor'} onClick={() => setLocalClient('cursor')}>Cursor</Button>
            <Button variant={localClient === 'muse' ? 'default' : 'outline'} size="sm" aria-pressed={localClient === 'muse'} onClick={() => setLocalClient('muse')}>Muse</Button>
            <Button variant={localClient === 'json-client' ? 'default' : 'outline'} size="sm" aria-pressed={localClient === 'json-client'} onClick={() => setLocalClient('json-client')}>JSON MCP client</Button>
          </div>
          {localClient === 'claude-code' ? <CommandRow label={`Add ${serviceName} to Claude Code`} command={`claude mcp add --transport http ${target} ${quoted(mcpUrl)}`} />
            : localClient === 'codex' ? <div className="space-y-2"><CommandRow label={`Add ${serviceName} to Codex`} command={`codex mcp add ${target} --url ${quoted(mcpUrl)}`} /><CommandRow label={`Sign in to ${serviceName}`} command={`codex mcp login ${target}`} /></div>
              : localClient === 'cursor' ? <div className="space-y-2"><JsonBlock label="Add to ~/.cursor/mcp.json" json={mcpJson} hint={`Merge the ${target} entry into mcpServers if the file already has servers.`} /><CommandRow label={`Sign in to ${serviceName}`} command={`cursor-agent mcp login ${target}`} /></div>
              : localClient === 'muse' ? <div className="space-y-2"><JsonBlock label="Add to ~/.config/muse/settings.json" json={museJson} hint={`Merge the ${target} entry into mcpServers if the file already has settings.`} /><CommandRow label={`Sign in to ${serviceName}`} command={`muse mcp login ${target}`} /></div>
              : <JsonBlock label="MCP client config" json={mcpJson} hint="Use the URL with your client's HTTP MCP transport and OAuth sign-in." />}
        </div>
        <p className="text-xs text-muted-foreground">Approve the OAuth connection in your browser when your agent asks. A local server URL works only for agents on this computer.</p>
        {target === 'agentworks' && localClient === 'claude-code' && <details className="rounded-md border border-border p-3 text-xs text-muted-foreground"><summary className="cursor-pointer font-medium text-foreground">Optional: use the AgentWorks skill</summary><p className="mt-2">The skill teaches Claude Code how to use the workflow tools. Copy its text into your agent&apos;s skills folder.</p><div className="mt-3 flex flex-wrap gap-2"><Button variant="ghost" size="sm" disabled={skillBusy !== null} onClick={() => void copySkill()}><Copy className="mr-1 h-3.5 w-3.5" />Copy skill text</Button></div>{skillMsg && <p className="mt-2">{skillMsg}</p>}</details>}
      </div> : <div className="space-y-4">
        <div className="flex flex-wrap gap-2" role="group" aria-label="Hosted MCP client">
          <Button variant={hostedClient === 'chatgpt' ? 'default' : 'outline'} size="sm" aria-pressed={hostedClient === 'chatgpt'} onClick={() => setHostedClient('chatgpt')}>ChatGPT</Button>
          <Button variant={hostedClient === 'cowork' ? 'default' : 'outline'} size="sm" aria-pressed={hostedClient === 'cowork'} onClick={() => setHostedClient('cowork')}>Claude Cowork</Button>
        </div>
        {hostedClient === 'cowork' && target === 'agentworks' ? <div className="space-y-2 rounded-md border border-border p-3">
          <p className="text-sm font-medium text-foreground">Install AgentWorks in Cowork</p>
          <p className="text-xs text-muted-foreground">Download the plugin, then open Customize → Plugins in Cowork and upload it. Connect AgentWorks and approve access in your browser.</p>
          <Button variant="outline" size="sm" disabled={skillBusy !== null || isLoopbackOrigin} onClick={() => void downloadCoworkPlugin()}><Download className="mr-1 h-3.5 w-3.5" />Download Cowork plugin</Button>
          {pluginMsg && <p className="text-xs text-muted-foreground">{pluginMsg}</p>}
        </div> : <p className="text-xs text-muted-foreground">In ChatGPT, open Settings → Apps & Connectors → Developer Mode, then add a custom MCP connector.</p>}
        {hostedClient === 'chatgpt' ? <CommandRow label="Remote MCP URL" command={mcpUrl} /> : <details className="rounded-md border border-border p-3 text-xs text-muted-foreground"><summary className="cursor-pointer font-medium text-foreground">Connect manually instead</summary><p className="mt-2 mb-2">In Cowork, open Customize → Connectors → Add custom connector, then use this URL.</p><CommandRow label="Remote MCP URL" command={mcpUrl} /></details>}
        {isLoopbackOrigin && <p className="text-xs text-amber-500">Hosted apps need a public server URL; open Connect on that server instead.</p>}
        <p className="text-xs text-muted-foreground">Choose OAuth when the app asks how to authenticate. {serviceName} will open a sign-in and permission screen.</p>
        {target === 'agentworks' && hostedClient === 'chatgpt' && <details className="rounded-md border border-border p-3 text-xs text-muted-foreground">
          <summary className="cursor-pointer font-medium text-foreground">Optional: give the assistant workflow guidance</summary>
          <p className="mt-2">Upload the skill where Skills are supported, or paste its text into the app&apos;s custom instructions.</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button variant="outline" size="sm" disabled={skillBusy !== null} onClick={() => void downloadSkill()}><Download className="mr-1 h-3.5 w-3.5" />Download skill .zip</Button>
            <Button variant="ghost" size="sm" disabled={skillBusy !== null} onClick={() => void copySkill()}><Copy className="mr-1 h-3.5 w-3.5" />Copy skill text</Button>
          </div>
          {skillMsg && <p className="mt-2">{skillMsg}</p>}
        </details>}
      </div>}
    </SettingsCard>
    {connectionLoadError && <p role="alert" className="text-sm text-amber-600">Connected apps are unavailable. Restart or update the AgentWorks server to use browser sign-in.</p>}
    {connections.length > 0 && <SettingsCard icon={<Plug className="h-4 w-4 text-primary" />} title="Connected apps" description="Revoke access to an AI app when you are done.">
      <div className="space-y-2">{connections.map(item => <div key={item.id} className="flex items-center justify-between gap-2 text-sm"><span className="min-w-0 truncate">{item.client_name}</span><Button variant="outline" size="sm" onClick={() => void revoke(item.id)}>Revoke</Button></div>)}</div>
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </SettingsCard>}
  </div>
}
