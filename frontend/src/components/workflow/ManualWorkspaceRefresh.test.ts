import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('manual workflow workspace refresh', () => {
  it('does not refresh Plan from chat or plan mutation events', () => {
    const chat = readFileSync('src/components/ChatArea.tsx', 'utf8')
    const layout = readFileSync('src/components/workflow/WorkflowLayout.tsx', 'utf8')
    const planData = readFileSync('src/components/workflow/hooks/usePlanData.ts', 'utf8')

    expect(chat).not.toContain('signalPlanModified')
    expect(layout).not.toContain('processPlanUpdateEvents')
    expect(layout).not.toContain("event.type === 'todo_steps_extracted'")
    expect(planData).not.toContain("addEventListener('plan-modified'")
  })

  it('keeps explicit Plan and Report refresh controls', () => {
    const toolbar = readFileSync('src/components/workflow/canvas/WorkflowToolbar.tsx', 'utf8')
    const canvas = readFileSync('src/components/workflow/canvas/WorkflowCanvas.tsx', 'utf8')
    const report = readFileSync('src/components/workflow/ReportViewer.tsx', 'utf8')

    expect(toolbar).not.toContain('data-testid="refresh-report"')
    expect(canvas).toContain('data-testid="refresh-plan"')
    expect(canvas).toContain('isRefreshingPlan')
    expect(canvas).toContain('loadPlanRefresh()')
    expect(canvas).not.toContain('refreshEvaluationPlan')
    expect(canvas).toContain("addToast('Plan reloaded', 'success')")
    expect(report).toContain('aria-label="Refresh dashboard"')
  })
})
