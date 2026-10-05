// React.lazy loads a product only after selection. Importing every product
// while the switcher is idle can block the main thread during menu clicks.
export const loadVideoStudioSurface = () => import('./video-studio/VideoStudioSurface')
export const loadDominionSurface = () => import('./dominion/DominionSurface')
export const loadSparkQuillSurface = () => import('./sparkquill/SparkQuillSurface')
export const loadWorkSurface = () => import('./work/WorkSurface')
export const loadGatewaySurface = () => import('./mcp-gateway/GatewaySurface')
export const loadKnowledgebaseSurface = () => import('./knowledgebase/KnowledgebaseSurface')
