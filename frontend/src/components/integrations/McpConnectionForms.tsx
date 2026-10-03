import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { parseOAuthClientJson } from '../../products/work/oauthClientJson'
import type { PlaceMcpCustomServer } from '../../api/placeMcp'
import { useState } from 'react'

export function McpOAuthClientForm({ server, redirectUri, busy, cancel, submit, reportError }: {
  server: string; redirectUri?: string; busy: boolean; cancel: () => void
  submit: (client: { clientId: string; clientSecret?: string }) => void; reportError: (error: string) => void
}) {
  const [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  return <div className="space-y-2 rounded-md border border-border p-3" data-testid="mcp-client-prompt">
    <p className="font-medium">Sign in to {server} with your own OAuth app</p>
    <p className="text-muted-foreground">Create an OAuth app, then enter its client ID and secret. They are kept encrypted and used only for this sign-in.</p>
    {redirectUri && <p className="break-all text-muted-foreground">Callback URL to register: <code>{redirectUri}</code></p>}
    <label className="inline-flex cursor-pointer text-primary">Upload client_secret.json
      <input type="file" accept="application/json,.json" className="hidden" aria-label="Upload the client JSON" onChange={event => { const file = event.target.files?.[0]; event.target.value = ''; if (file) void file.text().then(text => { const result = parseOAuthClientJson(text); if (result) { setClientId(result.clientId); setClientSecret(result.clientSecret) } else reportError('That file is not an OAuth client file (client_secret_….json).') }) }} />
    </label>
    <div className="grid gap-2 sm:grid-cols-2"><Input aria-label="OAuth client ID" placeholder="Client ID" value={clientId} onChange={event => setClientId(event.target.value)} /><Input type="password" autoComplete="off" aria-label="OAuth client secret" placeholder="Client secret" value={clientSecret} onChange={event => setClientSecret(event.target.value)} /></div>
    <div className="flex justify-end gap-2"><Button variant="ghost" size="sm" onClick={cancel}>Cancel</Button><Button size="sm" disabled={busy || !clientId.trim()} onClick={() => submit({ clientId: clientId.trim(), clientSecret: clientSecret.trim() })}>Sign in</Button></div>
  </div>
}

export function McpCustomServerForm({ secrets, busy, cancel, submit }: {
  secrets: string[]; busy: boolean; cancel: () => void
  submit: (server: PlaceMcpCustomServer, key: { name: string; value: string } | null) => Promise<unknown>
}) {
  const [name, setName] = useState(''); const [url, setUrl] = useState('')
  const [header, setHeader] = useState(''); const [secret, setSecret] = useState(''); const [key, setKey] = useState('')
  return <div className="grid gap-2 rounded-md border border-border p-3 sm:grid-cols-2">
    <Input value={name} onChange={event => setName(event.target.value)} placeholder="Server name" aria-label="Server name" />
    <Input value={url} onChange={event => setUrl(event.target.value)} placeholder="https://… MCP URL" aria-label="Server URL" />
    <Input value={header} onChange={event => setHeader(event.target.value)} placeholder="API key header (optional)" aria-label="API key header" />
    <select value={secret} onChange={event => setSecret(event.target.value)} className="h-9 rounded-md border border-border bg-background px-2 text-sm" aria-label="Secret for the header"><option value="">Use an existing private key…</option>{secrets.map(item => <option key={item}>{item}</option>)}</select>
    <Input type="password" autoComplete="off" value={key} onChange={event => setKey(event.target.value)} disabled={!header.trim()} aria-label="Private API key" placeholder="Or enter a new private API key" />
    <div className="flex justify-end gap-2 sm:col-span-2"><Button size="sm" variant="ghost" onClick={cancel}>Cancel</Button><Button size="sm" disabled={busy || !name.trim() || !url.trim() || (!!header.trim() && !secret && !key)} onClick={() => {
      const keyName = `MCP_${name.trim().toUpperCase().replace(/[^A-Z0-9_]/g, '_').slice(0, 54)}_KEY`
      void submit({ name: name.trim(), url: url.trim(), headers: header.trim() && (key || secret) ? { [header.trim()]: { secret: key ? keyName : secret, format: header.toLowerCase().trim() === 'authorization' ? 'Bearer {}' : '{}' } } : undefined }, key ? { name: keyName, value: key } : null)
    }}>Add server</Button></div>
  </div>
}

export function McpCredentialSettings({ oauthControl, value, change, save, close, busy, name }: {
  oauthControl?: React.ReactNode; value: string; change: (value: string) => void; save: () => void; close: () => void; busy: boolean; name: string
}) {
  return <div className="mt-3 space-y-2 rounded-md border border-border p-3">
    <h5 className="font-semibold">Connection settings</h5>
    {oauthControl ? <><p className="text-muted-foreground">Sign in again to reconnect. Group permissions stay unchanged.</p>{oauthControl}<Button variant="ghost" size="sm" onClick={close}>Close</Button></> : <>
      <p className="text-muted-foreground">Update this server’s connection token.</p><label className="block font-medium" htmlFor={`credential-${name}`}>Server access token</label>
      <Input id={`credential-${name}`} type="password" autoComplete="off" aria-label={`New bearer token for ${name}`} placeholder="Enter a replacement token" value={value} onChange={event => change(event.target.value)} />
      <div className="flex gap-2"><Button size="sm" disabled={busy || !value.trim()} onClick={save}>Update token</Button><Button variant="ghost" size="sm" onClick={close}>Cancel</Button></div>
    </>}
  </div>
}
