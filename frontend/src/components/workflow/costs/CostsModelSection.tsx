import { totalTokens } from '../../../utils/costTokens'
import type { CostAggregate, CostSummary } from '../../../services/api-types'

export type ModelCostRow = {
  modelId: string
  provider: string
  agentLabel: string
  usage: CostAggregate
}

export function costAgentLabel(provider: string, modelId: string): string {
  const normalized = provider.trim().toLowerCase().replace(/[_\s]+/g, '-')
  if (normalized === 'muse-cli' || normalized === 'musecli' || modelId.toLowerCase().startsWith('muse-')) return 'Muse'
  if (normalized === 'claude-code' || normalized === 'claudecode') return 'Claude Code'
  if (normalized === 'codex-cli' || normalized === 'codexcli') return 'OpenAI Codex'
  if (normalized === 'cursor-cli' || normalized === 'cursorcli') return 'Cursor'
  if (normalized === 'pi-cli' || normalized === 'picli') return 'Pi'
  if (normalized === 'anthropic') return 'Anthropic'
  if (normalized === 'openai') return 'OpenAI'
  return provider.trim() || 'Unknown agent'
}

export function buildModelCostRows(summary?: Pick<CostSummary, 'by_model'> | null): ModelCostRow[] {
  return Object.entries(summary?.by_model || {})
    .map(([modelId, usage]) => ({
      modelId,
      provider: usage.provider?.trim() || '',
      agentLabel: costAgentLabel(usage.provider || '', modelId),
      usage,
    }))
    .filter(row => row.usage.call_count > 0 || totalTokens(row.usage) > 0 || row.usage.total_cost_usd > 0)
    .sort((left, right) => right.usage.total_cost_usd - left.usage.total_cost_usd ||
      totalTokens(right.usage) - totalTokens(left.usage) ||
      left.modelId.localeCompare(right.modelId))
}
