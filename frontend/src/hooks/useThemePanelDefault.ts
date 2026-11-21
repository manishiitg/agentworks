import { useCallback, useState } from 'react'

/** A personal theme supplies a starting layout, never a constraint on navigation.
 * An explicit toggle, link or restored workspace choice wins for this page/scope. */
export function useThemePanelDefault(defaultOpen: boolean | undefined, scope: string): [boolean, (open: boolean) => void] {
  const [choice, setChoice] = useState<{ scope: string; open: boolean } | null>(null)
  const choose = useCallback((open: boolean) => setChoice({ scope, open }), [scope])
  return [choice?.scope === scope ? choice.open : defaultOpen ?? true, choose]
}
