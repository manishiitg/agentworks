import { Children, isValidElement, type ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'

export type WorkspaceShortcut = { id: string; label: string; icon: LucideIcon; onClick?: () => void; content?: ReactNode }
export type ContextShortcutProps = Pick<WorkspaceShortcut, 'id' | 'label' | 'icon'> & { children: ReactNode }

/** Explicit metadata over existing permission-gated content; adds no DOM node. */
export function WorkspaceContextShortcut({ children }: ContextShortcutProps) { return <>{children}</> }

export function collectContextShortcuts(children: ReactNode): WorkspaceShortcut[] {
  const result: WorkspaceShortcut[] = []
  Children.forEach(children, child => {
    if (!isValidElement<{ children?: ReactNode }>(child)) return
    if (child.type === WorkspaceContextShortcut) {
      const { id, label, icon, children: content } = child.props as ContextShortcutProps
      result.push({ id, label, icon, content })
    } else if (child.props.children) result.push(...collectContextShortcuts(child.props.children))
  })
  return result
}

export function contextShortcutOptions(children: ReactNode, available: readonly WorkspaceShortcut[] = []) {
  const declared = collectContextShortcuts(children)
  // Named section shortcuts already cover their parent panel. Avoid offering
  // a second indistinguishable Schedules & triggers row beside those links.
  const sectionParents = new Set(declared.filter(item => item.id.includes(':')).map(item => item.id.split(':')[0]))
  const options = new Map(available.filter(item => !sectionParents.has(item.id)).map(item => [item.id, item]))
  for (const item of declared) options.set(item.id, item)
  return [...options.values()]
}
