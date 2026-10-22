import { createContext, useContext, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

type HeaderSlots = {
  heading: HTMLDivElement | null
  actions: HTMLDivElement | null
  setHeading: (element: HTMLDivElement | null) => void
  setActions: (element: HTMLDivElement | null) => void
}

const HeaderSlotsContext = createContext<HeaderSlots | null>(null)

/** DOM slots keep controls in their original React tree, with their existing
 * state, handlers and permission gates, while presenting one panel header. */
export function WorkspacePanelHeaderSlotsProvider({ children }: { children: ReactNode }) {
  const [heading, setHeading] = useState<HTMLDivElement | null>(null)
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return <HeaderSlotsContext.Provider value={{ heading, actions, setHeading, setActions }}>{children}</HeaderSlotsContext.Provider>
}

export function useWorkspacePanelHeaderSlots() {
  return useContext(HeaderSlotsContext)
}

/** Mounted but hidden views must not publish controls into the visible panel. */
export function WorkspacePanelHeaderBoundary({ active, children }: { active: boolean; children: ReactNode }) {
  const slots = useWorkspacePanelHeaderSlots()
  return <HeaderSlotsContext.Provider value={active ? slots : null}>{children}</HeaderSlotsContext.Provider>
}

/** For panels with custom controls rather than a WorkspaceViewHeader. Without
 * a workspace header (dialogs and full-screen tools), controls stay local. */
export function WorkspacePanelActions({ children, local = false }: { children: ReactNode; local?: boolean }) {
  const slots = useWorkspacePanelHeaderSlots()
  const actions = <div className="flex max-w-full flex-wrap items-center justify-end gap-1.5">{children}</div>
  return slots?.actions && !local ? createPortal(actions, slots.actions) : actions
}
