import { describe, expect, it } from 'vitest'
import type { PulseReviewFocus } from '../../services/api-types'
import { mergePulseReviewCoverage, reviewCoverageForArea, TECHNICAL_REVIEW_AREAS } from './pulseReviewCoverage'
const row = (scope: string, at: string): PulseReviewFocus => ({ workspace_path: 'Workflow/latency-report', module: 'technical_review', focus_key: 'store_integrity', route_scope: scope, last_reviewed_at: at, updated_at: at })
describe('technical review coverage', () => {
  it('keeps old learnings evidence when newer store reviews checked another scope', () => {
    const learning = { ...row('workflow/learnings', '2026-08-31'), evidence: ['References verified'] }
    const rows = mergePulseReviewCoverage([row('workflow/database', '2026-09-09'), learning], [{ ...learning, evidence: undefined }])
    const area = TECHNICAL_REVIEW_AREAS.find(item => item.key === 'learnings')!
    expect(reviewCoverageForArea(area, rows)).toEqual([learning])
    expect(rows).toHaveLength(2)
  })
  it('does not count a tick, a generic store review, or another module as a learnings or KB check', () => {
    const rows = [row('', '2026-09-09'), row('workflow/database', '2026-09-09'), row('workflow/learnings', ''), { ...row('workflow/learnings', '2026-09-09'), module: 'strategic_review' }]
    for (const key of ['learnings', 'knowledge_base']) expect(reviewCoverageForArea(TECHNICAL_REVIEW_AREAS.find(item => item.key === key)!, rows)).toEqual([])
    expect(reviewCoverageForArea(TECHNICAL_REVIEW_AREAS.find(item => item.key === 'knowledge_base')!, [row('workflow/knowledgebase', '2026-08-02')])).toHaveLength(1)
  })
})
