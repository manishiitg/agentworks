import type { LLMProvider } from '../services/api-types';

// AgentLLMConfig represents LLM configuration for an agent
export interface AgentLLMConfig {
  connection_id?: string;
  published_llm_id?: string;
  provider?: LLMProvider;
  model_id?: string;
  options?: Record<string, unknown>;

}

// AgentConfigs represents per-agent configuration for a step
export interface AgentConfigs {
  execution_llm?: AgentLLMConfig;
  execution_tier?: 'high' | 'medium' | 'low';
  lock_code?: boolean;                        // Freeze main.py against LLM rewrites
  selected_servers?: string[];
  selected_tools?: string[];
  enabled_custom_tools?: string[];
  enabled_skills?: string[];
  use_code_execution_mode?: boolean;
  disable_parallel_tool_execution?: boolean;  // Disable parallel tool execution (default: enabled)
  knowledgebase_access?: 'read' | 'write' | 'read-write' | 'none';
  knowledgebase_contribution?: string;
  db_access?: 'read' | 'write' | 'read-write' | 'none';
}

// Extended TodoStep with agent_configs
export interface TodoStepWithConfigs {
  id?: string;                        // Stable step ID (from backend, always present for new steps)
  title?: string;
  description?: string;
  success_criteria?: string;
  context_dependencies?: string[];
  context_output?: string;
  success_patterns?: string[];
  failure_patterns?: string[];
  next_step_id?: string;
  // Human input step fields (asks question to human and blocks for input)
  has_human_input?: boolean;
  question?: string;
  variable_name?: string;
  response_type?: string; // "text" (default), "yesno", "multiple_choice"
  options?: string[];
  if_yes_next_step_id?: string; // For yesno type when response is "yes"
  if_no_next_step_id?: string; // For yesno type when response is "no"
  option_routes?: Record<string, string>; // For multiple_choice type - maps option index or value to next_step_id
  agent_configs?: AgentConfigs;
  validation_schema?: ValidationSchema; // Optional structured validation schema for step outputs
}

// StepConfig represents a single step's configuration in step_config.json
export interface StepConfig {
  id: string;                 // Stable step ID (generated from title) - required identifier
  title?: string;             // Step title (optional, for reference/display only)
  agent_configs?: AgentConfigs;
}

// Validation schema types
export interface ValidationSchema {
  files?: FileValidationRule[];
}

export interface FileValidationRule {
  file_name: string;
  must_exist: boolean;
  json_checks?: JSONValidationCheck[];
}

export interface JSONValidationCheck {
  path: string;                      // JSONPath, e.g., "$.status", "$.databases[0].name"
  must_exist: boolean;               // Key/path must exist
  value_type?: string;                // "string", "number", "boolean", "array", "object"
  min_length?: number;               // For arrays/strings
  max_length?: number;               // For arrays/strings
  pattern?: string;                   // Regex for format validation
  min_value?: number;                // For numbers
  max_value?: number;                // For numbers
  consistency_check?: ConsistencyRule; // Compare with other fields
}

export interface ConsistencyRule {
  type: string;                      // "equals", "greater_than", "less_than", "array_length", "in_array"
  compare_with_path: string;         // JSONPath to compare with
}

export interface StepSharing {
  agent_ids?: string[];
}

// Common fields shared by all step types
interface CommonStepFields {
  required_variables?: string[]
  id: string;                        // Stable step ID (required, always provided by backend)
  title: string;
  description?: string;
  success_criteria?: string;
  context_dependencies?: string[];
  context_output?: string | string[];
  validation_schema?: ValidationSchema; // Optional structured validation schema for step outputs
  agent_configs?: AgentConfigs;       // Merged from step_config.json
  shared_with?: StepSharing;
}

// Scripted step
export interface ScriptedPlanStep extends CommonStepFields {
  type: 'scripted' | 'regular'; // regular is a saved-plan compatibility alias.
  script_only?: boolean;
  next_step_id?: string;                    // Explicit successor for scripted route chains
}

export interface AgentWriteAccess {
  knowledgebase?: boolean;
  db?: boolean;
  learnings?: boolean;
}

export interface AgentItem {
  id: string;
  type: 'user_message' | 'prevalidation' | 'foreach' | 'scripted' | string;
  kind?: 'execution' | 'learning' | 'knowledgebase' | 'db' | 'check' | 'critique' | 'self_validation' | 'reference_check' | 'hallucination_check' | 'code_review' | string;
  title?: string;
  message?: string;
  write_access?: AgentWriteAccess;
  retained_reason?: string;
  validation_schema?: ValidationSchema;
  prevalidation?: ValidationSchema;
  source_sql?: string;        // read-only query against db/db.sqlite
  source?: string;            // legacy display only
  source_path?: string;       // legacy display only
  scripted_steps?: { id: string; step_id: string; parameters?: Record<string, unknown> }[];
  max_parallel?: number;      // scripted batch concurrency, 0/1 = sequential
  max_iterations?: number;    // foreach items: optional cap on rows (0 = all)
}

export interface AgentPlanStep extends CommonStepFields {
  crew_agent?: string;
  type: 'agent';
  prompt_contract?: 'description-first-v1';
  system_rules?: string;
  initial_write_access?: AgentWriteAccess;
  authored_prompt?: boolean;
  system_prompt?: string;
  items?: AgentItem[];
  predefined_routes?: PlanRoutingRoute[]; // Optional bounded specialists selected by the sequence agent at runtime
  next_step_id?: string;
}

// Human input step (asks question to human and blocks for input)

// Routing route for routing steps
export interface RoutingRoute {
  route_id: string;
  route_name: string;
  condition: string;
  next_step_id: string;
}

// Routing step -- now the "route" (major sub-workflow fork) concept.
// Deterministic N-way switch; see BranchPlanStep for the small in-flow
// decision sibling with identical mechanics. See PLAT-259.
export interface RoutingPlanStep extends CommonStepFields {
  type: 'routing';
  routing_question: string;           // Question to evaluate for route selection
  routes: RoutingRoute[];             // Available routes (min 2)
  default_route_id?: string;          // Optional fallback route_id
  selected_route_id?: string;         // runtime: stores selected route
}

// Branch step -- a small in-flow next-step decision, same deterministic
// switch mechanics as RoutingPlanStep (now the "route"/major-fork concept),
// just its own type tag. See PLAT-259.
export interface BranchPlanStep extends CommonStepFields {
  type: 'branch';
  value_path?: string;
  value_cases?: Record<string, string>;
  branch_question: string;            // Question to evaluate for route selection
  routes: RoutingRoute[];             // Available routes (min 2)
  default_route_id?: string;          // Optional fallback route_id
  route_source?: 'human';             // "human": asks the person when no route was preseeded (routes are the options); successor to yesno/multiple_choice human_input
  selected_route_id?: string;         // runtime: stores selected route
}

// Crew step -- invokes one trigger of a persistent Crew project and waits
// for its final response, saved as the step's context output. The trigger
// owns its base instruction and conversation destination; the step supplies
// the workflow-specific instruction and runtime input. Mirrors the backend
// CrewPlanStep; crew steps run only as top-level steps.
export interface CrewPlanStep extends CommonStepFields {
  type: 'crew';
  crew_profile_id: string;        // Product profile of the target Crew; initially "work"
  crew_project_id: string;        // Stable target Crew project ID
  trigger_id: string;             // Crew trigger to invoke
  instruction: string;            // Workflow-specific instruction, rendered with variables at run time
  timeout_seconds?: number;       // Maximum wait for the Crew run; defaults to 1800
  next_step_id?: string;          // Optional explicit successor; empty preserves sequential execution
}

// Discriminated union type for all step types
export type PlanStep = ScriptedPlanStep | AgentPlanStep | RoutingPlanStep | BranchPlanStep | CrewPlanStep;

// PlanRoutingRoute represents a possible route/sub-agent for planning
export interface PlanRoutingRoute {
  route_id: string;                   // Unique ID for this route
  route_name: string;                 // Human-readable name
  condition: string;                  // Condition description
  sub_agent_step?: PlanStep;           // The sub-agent step to execute
  orphan_step_ref?: string;           // Optional reference to a reusable orphan step in the same plan
}

// PlanningResponse interface for plan.json
export interface PlanningResponse {
  steps: PlanStep[];
  orphan_steps?: PlanStep[];
  run_mode?: string;
  [key: string]: unknown;             // Allow other fields for flexibility
}

// Type guard functions for discriminated union
export function isScriptedStep(step: PlanStep): step is ScriptedPlanStep {
  return step.type === 'scripted' || step.type === 'regular';
}


export function isAgentStep(step: PlanStep): step is AgentPlanStep {
  return step.type === 'agent';
}

export function isCrewStep(step: PlanStep): step is CrewPlanStep {
  return step.type === 'crew';
}

export type EffectiveExecutionMode = 'scripted' | 'agentic';

// Anything the canvas can hand us: a plan step.
export interface ExecutionModeStepLike {
  type?: string;
  agent_configs?: AgentConfigs;
  execution_mode?: string;
}

// The execution model a step actually runs with (PLAT-287): decided by the plan
// type. `scripted` is scripted — its work is the checked-in
// learnings/<id>/main.py — and `agent` is agentic.
// Routing/branch/orchestrator/human-input/crew steps have no execution mode.
export function effectiveExecutionMode(
  step: ExecutionModeStepLike | null | undefined,
): EffectiveExecutionMode | undefined {
  if (!step) return undefined;
  const type = step.type || 'scripted';
  if (type === 'scripted' || type === 'regular') return 'scripted';
  if (type === 'agent') return 'agentic';
  return undefined;
}

// Card rows are presentation only; execution keeps the complete item queue.
export function agentCardItems(step: PlanStep, items: AgentItem[]): AgentItem[] {
  return isAgentStep(step) && step.prompt_contract === 'description-first-v1' && !step.authored_prompt && items[0]?.id === '__step_description__'
    ? items.slice(1) : items;
}

// The item list the runtime actually executes: an authored agent supplies its
// own. Display only — never written back to plan.json.
export function configuredAgentItems(step: PlanStep): AgentItem[] {
  if (!isAgentStep(step)) return [];
  const items = step.items ?? [];
  if (step.prompt_contract === 'description-first-v1' && !step.authored_prompt) {
    return [{id: '__step_description__', type: 'user_message', title: 'Step task', kind: 'execution', message: step.description}, ...items];
  }
  return items;
}

export function isRoutingStep(step: PlanStep): step is RoutingPlanStep {
  return step.type === 'routing';
}

export function isBranchStep(step: PlanStep): step is BranchPlanStep {
  return step.type === 'branch';
}

/** Preserve each step type's fields when editing through the plan API. */
export type PlanStepPatch<T = PlanStep> = T extends PlanStep ? Partial<Omit<T, 'agent_configs'>> : never
