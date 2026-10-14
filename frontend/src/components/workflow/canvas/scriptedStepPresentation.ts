export function planStepTypeLabel(type: string): string {
  if ((type === 'scripted' || type === 'regular')) return 'Script'
  if (type === 'agent' || type === 'orchestrator' || type === 'todo_task' || type === 'message_sequence') return 'Agent'
  if (type === 'crew') return 'Crew'
  return type
}
export function scriptedStepFilePath(stepId: string, codeLayoutVersion: number): string {
  return codeLayoutVersion >= 1
    ? `code/${stepId}/main.py`
    : `learnings/${stepId}/main.py`
}
