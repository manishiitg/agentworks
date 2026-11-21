import { shownTheme, useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import type { AppearanceDecoration } from '../../stores/appearanceTheme'

function DecorationNodes({ nodes }: { nodes: AppearanceDecoration[] }) {
  return <>{nodes.map(node => <div key={node.id} data-theme-decoration={node.id}>{node.children && <DecorationNodes nodes={node.children} />}</div>)}</>
}

/** The theme layer inside [data-app-main]: drawn above main's own background and
 * below its content (#3085), so an opaque main background no longer hides it.
 * Data-only nodes for theme CSS to paint; never HTML, React or active UI. */
export function AppearanceLayer() {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = useAppearanceStore(state => state.scope)
  const decorations = useAppearanceStore(state => shownTheme(state).decorations)
  const own = !!actor && scope === JSON.stringify([server, actor])
  return <div data-app-layer aria-hidden inert>
    {own && !!decorations?.length && <div data-theme-decorations><DecorationNodes nodes={decorations} /></div>}
    {/* Empty theme hook: custom CSS paints ambient motion here. */}
    <div data-app-ambient />
  </div>
}
