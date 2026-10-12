import { useRareWorkingTip } from './useRareWorkingTip'

// The transcript mounts this lazy component only for the running state.
export default function WorkingTip() {
  const tip = useRareWorkingTip(true)
  return tip ? <span data-testid="working-tip" className="truncate text-[11px] text-muted-foreground/70 animate-in fade-in duration-700">· Tip: {tip}</span> : null
}
