import { CheckCircle2, CircleSlash } from 'lucide-react'
import { useLLMStore } from '../../stores'
import { getProviderIntegrationKind } from '../../utils/llmDisplay'

type CapabilityStatus = 'available' | 'unavailable'

type CapabilityItem = {
  label: string
  detail: string
  status: CapabilityStatus
}

interface CodingAgentCapabilitiesProps {
  provider: string
  modelId?: string
  compact?: boolean
}

const CATALOG_CAPABILITIES: Array<{
  label: string
  aliases: string[]
}> = [
  { label: 'Web search', aliases: ['search_web', 'search'] },
]

function normalizeCapabilitySet(capabilities?: string[]): Set<string> {
  return new Set((capabilities || []).map(capability => capability.trim().toLowerCase()).filter(Boolean))
}

function hasAnyCapability(capabilities: Set<string>, aliases: string[]): boolean {
  return aliases.some(alias => capabilities.has(alias))
}

function buildCapabilityItems(provider: string, modelId: string | undefined, providerCapabilities?: string[]): CapabilityItem[] {
  const normalizedProvider = provider.trim().toLowerCase()
  if (getProviderIntegrationKind(normalizedProvider, modelId) !== 'coding_agent') {
    return []
  }

  const capabilitySet = normalizeCapabilitySet(providerCapabilities)
  return CATALOG_CAPABILITIES.map(capability => {
    const available = hasAnyCapability(capabilitySet, capability.aliases)
    return {
      label: capability.label,
      detail: available ? 'Available for this provider' : 'Not listed for this provider',
      status: available ? 'available' : 'unavailable',
    }
  })
}

function capabilityClasses(status: CapabilityStatus): string {
  switch (status) {
    case 'available':
      return 'border-green-200 bg-green-50 text-green-700 dark:border-green-900/60 dark:bg-green-950/30 dark:text-green-300'
    case 'unavailable':
      return 'border-border bg-muted/40 text-muted-foreground'
  }
}

export function CodingAgentCapabilities({ provider, modelId, compact = false }: CodingAgentCapabilitiesProps) {
  const providerCapabilities = useLLMStore(state => state.providerCapabilities)
  const capabilitiesByProvider = providerCapabilities as Record<string, string[] | undefined>
  const items = buildCapabilityItems(provider, modelId, capabilitiesByProvider[provider])

  if (items.length === 0) {
    return null
  }

  if (compact) {
    return (
      <div className="mt-2 flex flex-wrap gap-1.5">
        {items.map(item => (
          <span
            key={item.label}
            title={item.detail}
            className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] font-medium ${capabilityClasses(item.status)}`}
          >
            {item.status === 'unavailable' ? <CircleSlash className="h-3 w-3" /> : <CheckCircle2 className="h-3 w-3" />}
            {item.label}
          </span>
        ))}
      </div>
    )
  }

  return (
    <div className="rounded-md border border-border bg-muted/20 p-3">
      <div className="mb-2 text-sm font-medium text-foreground">Provider capabilities</div>
      <div className="grid gap-2 sm:grid-cols-2">
        {items.map(item => (
          <div
            key={item.label}
            className={`rounded border px-2.5 py-2 ${capabilityClasses(item.status)}`}
          >
            <div className="flex items-center gap-1.5 text-xs font-semibold">
              {item.status === 'unavailable' ? <CircleSlash className="h-3.5 w-3.5" /> : <CheckCircle2 className="h-3.5 w-3.5" />}
              {item.label}
            </div>
            <div className="mt-1 text-[11px] opacity-80">{item.detail}</div>
          </div>
        ))}
      </div>
    </div>
  )
}
