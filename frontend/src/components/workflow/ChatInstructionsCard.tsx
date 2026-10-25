import { useEffect, useState } from 'react'
import { MessageCircle } from 'lucide-react'
import api from '../../services/api'
import { Button } from '../ui/Button'
import { SettingsCard } from '../ui/SettingsCard'
import { Textarea } from '../ui/Textarea'

type Instructions = { chat_instructions: string; can_edit: boolean; max_length: number }

export function ChatInstructionsCard({ workspacePath }: { workspacePath: string }) {
  const [saved, setSaved] = useState<Instructions | null>(null)
  const [draft, setDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    let active = true
    setSaved(null); setError('')
    void api.get<Instructions>('/api/project-chat-instructions', { params: { workspace: workspacePath } }).then(({ data }) => {
      if (active) { setSaved(data); setDraft(data.chat_instructions) }
    }).catch(() => { if (active) setError('Could not load Chat instructions.') })
    return () => { active = false }
  }, [workspacePath])
  const save = async () => {
    setSaving(true); setError('')
    try {
      const { data } = await api.put<Instructions>('/api/project-chat-instructions', { workspace: workspacePath, chat_instructions: draft })
      setSaved(data); setDraft(data.chat_instructions)
    } catch { setError('Could not save Chat instructions. Please try again.') }
    finally { setSaving(false) }
  }
  return <SettingsCard icon={<MessageCircle className="h-4 w-4 text-primary" aria-hidden="true" />} title="Chat instructions" description="Optional guidance for how this app talks and helps people. Describe its tone, what to do first, or how to answer. People using the app can read this; keep secrets out.">
    <Textarea aria-label="Chat instructions" rows={6} value={draft} maxLength={saved?.max_length} onChange={event => setDraft(event.target.value)} disabled={!saved?.can_edit || saving} placeholder="e.g. Help people compare the results. Ask which date range they need, and include the source of each figure." />
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {saved?.can_edit && <div className="flex justify-end"><Button onClick={() => void save()} disabled={saving || draft.trim() === saved.chat_instructions}>{saving ? 'Saving…' : 'Save'}</Button></div>}
  </SettingsCard>
}
