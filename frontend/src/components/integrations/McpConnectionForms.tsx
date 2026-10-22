import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { useState } from 'react'

/** Shared named-instance entry: labels identify accounts without exposing credentials. */
export function McpNamedConnectionForm({ provider, busy, cancel, submit }: {
  provider: string; busy: boolean; cancel: () => void; submit: (name: string) => Promise<unknown>
}) {
  const [name, setName] = useState('')
  return <form className="flex flex-wrap items-center gap-2" onSubmit={event => { event.preventDefault(); if (name.trim()) void submit(name.trim()) }}>
    <Input className="min-w-40 flex-1" aria-label={`${provider} connection name`} placeholder={`${provider} · Engineering`} maxLength={100} autoFocus value={name} onChange={event => setName(event.target.value)} />
    <Button size="sm" type="submit" disabled={busy || !name.trim()}>{busy ? 'Adding…' : 'Add'}</Button>
    <Button size="sm" type="button" variant="ghost" disabled={busy} onClick={cancel}>Cancel</Button>
  </form>
}
