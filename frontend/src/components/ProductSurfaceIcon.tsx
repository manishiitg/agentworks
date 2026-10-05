import type { ComponentType } from 'react'
import { Waypoints } from 'lucide-react'
import { RunloopMark } from './branding/RunloopLogo'
import { WorkMark } from '../products/work/WorkMark'
import { CodeMark } from '../products/work/CodeMark'
import { VaultMark } from '../products/mcp-gateway/VaultMark'
import { BrainMark } from '../products/knowledgebase/BrainMark'
import { VideoStudioMark } from '../products/video-studio/VideoStudioMark'
import { DominionMark } from '../products/dominion/DominionMark'
import { SparkQuillMark } from '../products/sparkquill/SparkQuillMark'
import { PRODUCT_SURFACE_LABELS, type ProductSurface } from '../products/productSurfaceConfig'

const icons: Record<ProductSurface, ComponentType<{ className?: string; title?: string }>> = {
  agentworks: RunloopMark, relays: Waypoints, work: WorkMark, code: CodeMark,
  'mcp-gateway': VaultMark, 'video-studio': VideoStudioMark, dominion: DominionMark, sparkquill: SparkQuillMark,
  knowledgebase: BrainMark,
}

export function ProductSurfaceIcon({ surface, className = 'h-4 w-4' }: { surface: ProductSurface; className?: string }) {
  const Icon = icons[surface]
  return <span data-product-icon={surface} aria-label={PRODUCT_SURFACE_LABELS[surface]} className="inline-flex shrink-0">
    <Icon className={className} title={PRODUCT_SURFACE_LABELS[surface]} />
  </span>
}
