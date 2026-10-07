export type PulseCommandDefinition = {
  id: string
  label: string
  description: string
}

// Backup, publish and notify are each schedule's after-run options (PLAT-697
// phase 0), not Pulse statuses. Workflow Review (plan drift) runs before every
// run; it stays listed here as the module registry entry for its results.
export const PULSE_MODULE_COMMANDS: PulseCommandDefinition[] = [
  { id: 'technical_review', label: 'Health', description: 'Investigates correctness failures, invalid outputs and regressions' },
  { id: 'architecture_review', label: 'Architecture', description: 'Improves prompts, orchestration, scripts, learning, knowledge, data, reports and efficiency' },
  { id: 'strategic_review', label: 'Strategy', description: 'Audits hidden strategic mechanisms and conditionally explores materially different approaches' },
  { id: 'plan_drift_review', label: 'Workflow Review', description: 'Runs before each run when the plan changed: checks steps for DB, report, learnings, KB, and validation_schema drift' },
]

export const PULSE_FIXED_COMMANDS: PulseCommandDefinition[] = [
  { id: 'dashboard', label: 'Dashboard + questions', description: 'Updates the Pulse narrative and records decisions that need your input' },
]
