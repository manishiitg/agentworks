import { freeModelErrorHint, openModelPicker } from '../../utils/byok'

/** Under a failed turn: a free model that is busy or gone gets a plain hint and a quick way to pick another (PLAT-717). */
export function FreeModelHint({ error }: { error?: string | null }) {
  const hint = freeModelErrorHint(error)
  if (!hint) return null
  return (
    <div className="mt-1 flex flex-wrap items-center gap-2 rounded-md border border-amber-300/60 bg-amber-50 px-2 py-1.5 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
      <span className="min-w-0 flex-1">{hint}</span>
      <button type="button" onClick={openModelPicker} className="shrink-0 rounded border border-amber-400/70 px-2 py-0.5 font-medium hover:bg-amber-100 dark:hover:bg-amber-500/20">Pick another model</button>
    </div>
  )
}
