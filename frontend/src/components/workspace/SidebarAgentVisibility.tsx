import { createContext, useCallback, useContext, useLayoutEffect, useMemo, useState, type ReactNode } from 'react'

export const sidebarAgentIdentity = (connection: string, conversation: string) => JSON.stringify([connection, conversation])
type Visibility = { visible: ReadonlyMap<string, number>; register: (identity: string) => () => void }
const Context = createContext<Visibility | null>(null)

/** Rendered personal rows register their stable identity. Collapsed groups/Crews
 * unmount rows, so Recents never copies the sidebar's group/visibility rules. */
export function SidebarAgentVisibilityProvider({ children }: { children: ReactNode }) {
  const [visible, setVisible] = useState<ReadonlyMap<string, number>>(new Map())
  const register = useCallback((identity: string) => {
    const change = (delta: number) => setVisible(previous => {
      const next = new Map(previous), count = (next.get(identity) || 0) + delta
      if (count > 0) next.set(identity, count); else next.delete(identity)
      return next
    })
    change(1)
    return () => change(-1)
  }, [])
  const value = useMemo(() => ({ visible, register }), [visible, register])
  return <Context.Provider value={value}>{children}</Context.Provider>
}
export function SidebarAgentPresence({ identity, children }: { identity: string; children: ReactNode }) {
  const register = useContext(Context)?.register
  useLayoutEffect(() => identity ? register?.(identity) : undefined, [identity, register])
  return <>{children}</>
}
export function useVisibleSidebarAgents() { return useContext(Context)?.visible }
