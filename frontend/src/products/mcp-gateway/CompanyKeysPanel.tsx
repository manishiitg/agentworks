import { useCallback, useEffect, useState } from 'react'
import { KeyRound } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { Checkbox } from '../../components/ui/checkbox'
import { Input } from '../../components/ui/Input'
import { SecretField } from '../../components/ui/SecretField'
import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { secretsApi, type CompanyKeys } from '../../api/secrets'

/** Vault › Company keys: one list for everyone on this server (#4148). The server decides who is an admin, what the
 * one-time move would change, and every refusal; this screen shows it and sends what the person typed. */
export function CompanyKeysPanel() {
  const [view, setView] = useState<CompanyKeys | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [leaveOut, setLeaveOut] = useState<string[]>([])
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const load = useCallback(async () => {
    try { setView(await secretsApi.getCompanyKeys()); setError('') } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Company keys could not be loaded: Vault isn't answering.")
    }
  }, [])
  useEffect(() => { void load() }, [load])
  const run = async (work: () => Promise<string>) => {
    setBusy(true); setError(''); setNotice('')
    try { setNotice(await work()); await load(); setRevision(r => r + 1) } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'That did not work. Try again.')
    } finally { setBusy(false) }
  }
  const add = () => run(async () => {
    const key = name.trim()
    await secretsApi.saveGlobalSecret(key, value)
    setName(''); setValue('')
    return `Saved ${key}. Everyone on this server can use it now as $SECRET_${key}.`
  })
  const move = () => run(async () => {
    const result = await secretsApi.moveCompanyKeys(leaveOut)
    setLeaveOut([])
    return `Moved ${result.moved} keys to Company keys.${result.left_out ? ` ${result.left_out} left out are in your own vault.` : ''}`
  })
  if (!view) return error
    ? <div role="alert" className="rounded-md border border-destructive p-3 text-sm text-destructive">{error} <Button size="xs" variant="ghost" onClick={() => void load()}>Try again</Button></div>
    : <p className="text-xs text-muted-foreground">Loading company keys…</p>
  const toMove = !view.on && view.admin ? view.to_move ?? [] : []
  return <section className="space-y-4" aria-label="Company keys">
    <p className="text-sm text-muted-foreground">Everyone on this server can use these keys in their work. Values are never shown.</p>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    {notice && <p role="status" className="text-xs">{notice}</p>}
    {!view.admin && <p className="rounded-md bg-muted p-3 text-sm">Only Vault admins add or change company keys. Ask an admin, keep a key for yourself in My accounts, or add it to one online project's own keys.</p>}
    {toMove.length > 0 && <div className="space-y-3 rounded-md border border-border p-3" aria-label="Move group keys">
      <h3 className="text-sm font-medium">Move group keys to Company keys</h3>
      <p className="text-xs text-muted-foreground">Keys now belong to everyone on this server, not to groups. Untick a key to keep it only for you; it moves into your own vault. A backup is made first.</p>
      {view.note && <p className="text-xs text-muted-foreground">{view.note}</p>}
      <div className="divide-y divide-border rounded-md border border-border">
        {toMove.map(row => <label key={row.name} className="flex flex-wrap items-center gap-2 px-3 py-2 text-xs">
          <Checkbox aria-label={`Make ${row.name} a company key`} checked={!leaveOut.includes(row.name)}
            onCheckedChange={() => setLeaveOut(current => current.includes(row.name) ? current.filter(n => n !== row.name) : [...current, row.name])} />
          <span className="min-w-0 flex-1 break-all font-mono">{row.name}</span>
          <span className="text-muted-foreground">Today only for {row.groups?.length ? row.groups.join(', ') : 'no group'}</span>
          <span>{leaveOut.includes(row.name) ? 'Your own vault' : 'Everyone'}</span>
        </label>)}
      </div>
      <p className="text-xs text-muted-foreground">A company key reaches everyone, including people outside its old group.</p>
      <Button size="sm" disabled={busy} onClick={() => void move()}>{busy ? 'Moving…' : 'Move keys'}</Button>
    </div>}
    {view.admin
      ? <SecretSelectionSection key={revision} mode="vault" onRemove={async key => { await secretsApi.deleteGlobalSecret(key); setNotice(`${key} removed. Projects using it stop getting it on their next message.`) }} />
      : <div className="divide-y divide-border rounded-md border border-border">
        {view.keys.map(key => <div key={key} className="flex items-center gap-2 px-3 py-2.5"><KeyRound className="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span className="break-all font-mono text-xs">{key}</span></div>)}
        {!view.keys.length && <p className="p-3 text-xs text-muted-foreground">No company keys yet.</p>}
      </div>}
    {view.admin && <form aria-label="Add a company key" className="space-y-2 rounded-md border border-border p-3" onSubmit={event => { event.preventDefault(); void add() }}>
      <h3 className="text-sm font-medium">Add a company key</h3>
      <label className="block text-xs">Name<Input aria-label="Company key name" placeholder="e.g. OPENAI_API_KEY" value={name} disabled={busy} onChange={event => setName(event.target.value)} /></label>
      <SecretField label="Value" value={value} onChange={setValue} disabled={busy} />
      <p className="text-xs text-muted-foreground">Scripts read it as $SECRET_&lt;NAME&gt;. Saving a name that is already here replaces its value.</p>
      <Button size="sm" type="submit" disabled={busy || !name.trim() || !value}>{busy ? 'Saving…' : 'Add key'}</Button>
    </form>}
  </section>
}
