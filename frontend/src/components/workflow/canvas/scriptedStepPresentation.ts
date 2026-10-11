export function planStepTypeLabel(type: string): string {
  if (type === 'regular') return 'scripted step'
  if (type === 'agent' || type === 'orchestrator' || type === 'todo_task') return 'Agent'
  if (type === 'crew') return 'Crew'
  return type
}
export function scriptedStepFilePath(stepId: string, codeLayoutVersion: number): string {
  return codeLayoutVersion >= 1
    ? `code/${stepId}/main.py`
    : `learnings/${stepId}/main.py`
}
