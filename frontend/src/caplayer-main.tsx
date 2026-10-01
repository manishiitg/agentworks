import { useProductSurfaceStore } from './stores/useProductSurfaceStore'

// Independent entry point, shared application boot, auth, theme, capabilities
// and product router. The deployment allowlist controls the picker contents.
useProductSurfaceStore.getState().setProductSurface('mcp-gateway')
void import('./main')
