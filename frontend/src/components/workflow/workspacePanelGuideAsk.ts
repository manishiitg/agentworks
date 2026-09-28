import { isValidElement, type ReactNode } from 'react'
import { AskAIButton } from './AskAIButton'

/** Ask AI delivery config for the button inside the walkthrough popup. */
export interface PanelGuideAsk {
  workspacePath: string | null
  message: string
  onAsk?: (message: string) => void | Promise<void>
  label?: string
}

/**
 * Reads the Ask AI config off a header-trio Ask AI button element. Only the
 * icon-only row buttons move into the popup; inline labeled buttons (Notify's
 * "Change") stay where they are.
 */
export function panelGuideAskFromNode(node: ReactNode): PanelGuideAsk | undefined {
  if (!isValidElement<{ workspacePath?: string | null; message?: unknown; onAsk?: PanelGuideAsk['onAsk']; label?: string; iconOnly?: boolean }>(node)) return undefined
  if (node.type !== AskAIButton || !node.props.iconOnly) return undefined
  if (typeof node.props.message !== 'string') return undefined
  return {
    workspacePath: node.props.workspacePath ?? null,
    message: node.props.message,
    onAsk: node.props.onAsk,
    label: node.props.label,
  }
}
