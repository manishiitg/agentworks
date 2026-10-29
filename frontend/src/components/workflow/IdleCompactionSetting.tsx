import { useState } from 'react'
import { SettingsCard } from '../ui/SettingsCard'
import { ToggleRow } from '../ui/ToggleRow'

/**
 * The project's idle compaction switch (capabilities.llm_config.idle_compaction,
 * PLAT-872-71). Unset means on; the server's idle sweep reads it.
 */
export function IdleCompactionSetting({ enabled, onChange, disabledTitle = 'Only the owner can change this.' }: {
  enabled: boolean
  onChange?: (enabled: boolean) => Promise<unknown> | void
  disabledTitle?: string
}) {
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  return (
    <SettingsCard title="Long chats" ariaLabel="Tidy long chats while idle">
      <ToggleRow
        label="Tidy long chats while idle"
        description="After about 50 minutes without a message, long chats are summarised so your next message costs less."
        checked={enabled}
        disabled={!onChange || saving}
        disabledTitle={onChange ? 'Saving…' : disabledTitle}
        onCheckedChange={async checked => {
          if (!onChange) return
          setSaving(true)
          setError('')
          try {
            await onChange(checked)
          } catch (err) {
            setError(err instanceof Error ? err.message : 'Could not save this setting.')
          } finally {
            setSaving(false)
          }
        }}
      />
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
    </SettingsCard>
  )
}
