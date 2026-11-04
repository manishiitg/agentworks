import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import type { ProductSurface } from '../../products/productSurfaceConfig'

export const OPEN_TEAM_BOARD_EVENT = 'agentworks:open-team-board'

/** Project identity stays visible while people switch between its chats. Switching Crews is done from the sidebar
 * (owner, 4 Nov); in a Crew, clicking the name opens its team board (every agent and what it is doing now). */
export function ProjectWorkspaceTitle({ name, surface, projectId }: { name: string; surface: ProductSurface; projectId?: string }) {
  const className = 'mr-0 md:mr-2 inline-flex h-10 min-w-0 max-w-[50vw] md:h-8 md:shrink-0 items-center gap-1 md:gap-2 px-1 text-sm font-semibold md:max-w-56'
  const content = <>
    <ProductSurfaceIcon surface={surface} className="h-4 w-4 shrink-0" />
    <span className="truncate">{name}</span>
  </>
  if (!projectId) return <span data-project-title title={name} className={className}>{content}</span>
  return <button type="button" data-project-title title={name} aria-label={`${name}: see all agents`}
    onClick={() => window.dispatchEvent(new CustomEvent(OPEN_TEAM_BOARD_EVENT, { detail: { projectId } }))}
    className={`${className} rounded-md hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring`}>
    {content}
  </button>
}
