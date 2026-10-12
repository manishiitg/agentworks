// React.lazy loads a product only after selection. Importing every product
// while the switcher is idle can block the main thread during menu clicks.
export const loadVideoStudioSurface = () => import.meta.env.VITE_DEPLOYMENT_MODE === 'local' ? Promise.reject(new Error('Video Studio requires a server deployment')) : import('./video-studio/VideoStudioSurface')
export const loadSparkQuillSurface = () => import.meta.env.VITE_DEPLOYMENT_MODE === 'local' ? Promise.reject(new Error('SparkQuill requires a server deployment')) : import('./sparkquill/SparkQuillSurface')
export const loadWorkSurface = () => import('./work/WorkSurface')
export const loadBullpenSurface = () => import('./bullpen/BullpenSurface')
export const loadGatewaySurface = () => import.meta.env.VITE_DEPLOYMENT_MODE === 'local' ? Promise.reject(new Error('Vault requires a server deployment')) : import('./mcp-gateway/GatewaySurface')
export const loadKnowledgebaseSurface = () => import.meta.env.VITE_DEPLOYMENT_MODE === 'local' ? Promise.reject(new Error('Brain requires a server deployment')) : import('./knowledgebase/KnowledgebaseSurface')
