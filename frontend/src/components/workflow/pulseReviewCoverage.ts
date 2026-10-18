import type { PulseReviewFocus } from '../../services/api-types'
import { normalizePulseWorkspaceModule } from './pulseWorkspaceUtils'

export const TECHNICAL_REVIEW_AREAS = [
  { key: 'execution_health', label: 'Execution health' },
  { key: 'validation_contract_health', label: 'Validation and contracts' },
  { key: 'store_integrity', label: 'Data and stored knowledge' },
  { key: 'learnings', label: 'Skills', scope: /(?:^|[\s/_.-])(?:skills?|learnings?)(?:$|[\s/_.-])/i },
  { key: 'knowledge_base', label: 'Knowledge base', scope: /(?:^|[\s/_.-])(?:kb|knowledge[_ -]?base)(?:$|[\s/_.-])/i },
  { key: 'report_quality_truth', label: 'Dashboard accuracy' },
  { key: 'evaluation_quality_truth', label: 'Evaluation quality' },
]

export const ARCHITECTURE_REVIEW_AREAS: typeof TECHNICAL_REVIEW_AREAS = [
  { key: 'prompt_design', label: 'Prompts' },
  { key: 'orchestration_design', label: 'Orchestration' },
  { key: 'scripted_execution', label: 'Scripts and repeatable work' },
  { key: 'learning_quality', label: 'Skill quality' },
  { key: 'knowledgebase_design', label: 'Knowledge base' },
  { key: 'database_design', label: 'Data design' },
  { key: 'report_design', label: 'Reports' },
  { key: 'model_cost_fitness', label: 'Cost and efficiency' },
]

export function reviewCoverageForArea(area: typeof TECHNICAL_REVIEW_AREAS[number], coverage: PulseReviewFocus[], module = 'technical_review') {
  return coverage.filter(item => normalizePulseWorkspaceModule(item.module) === module
    && !!item.last_reviewed_at
    && (area.scope
      // A generic store review is not evidence that learnings or KB were checked.
      ? item.focus_key === 'store_integrity' && area.scope.test(item.route_scope || '')
      : item.focus_key === area.key))
    .sort((a, b) => (b.last_reviewed_at || '').localeCompare(a.last_reviewed_at || ''))
}

export function pulseReviewDate(value?: string) {
  if (!value) return 'Not recorded'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

// Prefer the enriched coverage row on timestamp ties; recent selections and
// agenda state are fallback data for servers that have not been upgraded yet.
export function mergePulseReviewCoverage(...sources: PulseReviewFocus[][]): PulseReviewFocus[] {
  const rows = new Map<string, PulseReviewFocus>()
  sources.flat().forEach(item => {
    const key = `${normalizePulseWorkspaceModule(item.module)}:${item.focus_key}:${item.route_scope || ''}`
    const existing = rows.get(key)
    if (!existing || (item.last_reviewed_at || '') > (existing.last_reviewed_at || '')) rows.set(key, item)
  })
  return [...rows.values()]
}
