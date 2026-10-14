import { useRareWorkingTip } from './useRareWorkingTip'

// The second line under the working line, "└ Tip: ...". The text and when it appears come from the server's product tips
// (YAML) and the 10 s delay and viewer gap in useRareWorkingTip; nothing is decided here. WorkingStatus mounts this lazily
// so the tip code loads only while an agent is working.
export default function WorkingTip({ turnKey }: { turnKey?: string | number }) {
  const tip = useRareWorkingTip(true, turnKey)
  return tip ? <span data-testid="working-tip" className="block max-w-full truncate text-[11px] text-muted-foreground/70 animate-in fade-in duration-700">└ Tip: {tip}</span> : null
}
