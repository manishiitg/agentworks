import { isValidElement, type ReactNode } from 'react'
import { AskAIButton } from './AskAIButton'

/** Contextual Ask AI delivery config used by shared panel headers. */
export interface PanelAsk {
  workspacePath: string | null
  message: string
  onAsk?: (message: string) => void | Promise<void>
  label?: string
}

/**
 * Reads the contextual question off an icon-only header action. Inline labeled
 * actions (such as Notify's "Change") keep their own delivery behavior.
 */
export function panelAskFromNode(node: ReactNode): PanelAsk | undefined {
  if (!isValidElement<{ workspacePath?: string | null; message?: unknown; onAsk?: PanelAsk['onAsk']; label?: string; iconOnly?: boolean }>(node)) return undefined
  if (node.type !== AskAIButton || !node.props.iconOnly) return undefined
  if (typeof node.props.message !== 'string') return undefined
  return {
    workspacePath: node.props.workspacePath ?? null,
    message: node.props.message,
    onAsk: node.props.onAsk,
    label: node.props.label,
  }
}
