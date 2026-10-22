import type { HTMLAttributes } from 'react'
import { cn } from '../../lib/utils'

/** One horizontal gutter for product headers, tabs and page content. */
export const WORKSPACE_PAGE_GUTTER = 'px-3'

/** Page padding belongs to this wrapper; cards keep their own internal spacing. */
export function WorkspacePageContent({ as: Tag = 'div', width = 'full', className, ...props }: HTMLAttributes<HTMLElement> & {
  as?: 'div' | 'section'
  width?: 'full' | 'form' | 'wide'
}) {
  return <Tag {...props} data-workspace-page-content className={cn('min-w-0', WORKSPACE_PAGE_GUTTER, 'py-4', width === 'form' && 'max-w-3xl', width === 'wide' && 'max-w-6xl', className)} />
}
