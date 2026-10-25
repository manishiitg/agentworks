import { useChatPreferences } from '../../hooks/useChatPreferences'

export function ChatDisplayPreferences() {
  const preferences = useChatPreferences()
  return <div className="mb-4 rounded-xl border border-border p-3">
    <label className="flex cursor-pointer items-center justify-between gap-4 text-sm">
      <span>Show context usage beside the model<span className="mt-1 block text-xs text-muted-foreground">Applies to your chats when the model reports its context usage.</span></span>
      <input type="checkbox" className="h-4 w-4 accent-primary" checked={preferences.showContextIndicator} disabled={!preferences.ready || preferences.save.isPending} onChange={event => preferences.save.mutate(event.target.checked)} />
    </label>
    {preferences.save.isError && <p role="alert" className="mt-2 text-xs text-destructive">Could not save your display. Try again.</p>}
  </div>
}
