import type { TodoStep } from '../generated/event-types';
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
  // DEPRECATED: backend removed these fields (replaced by learnings_access).
  // Kept on the interface only so legacy node-display code compiles; the backend
  // never reads them. New frontend code should consult learnings_access directly.
  disable_learning?: boolean;
  learning_detail_level?: string;
  learning_after_loop_iteration?: boolean;
  keep_learning_full?: boolean;
  // Primary gate for learnings_/_global/ access. "read" (default when unset) —
  // step sees global SKILL.md in its prompt; "read-write" — also contributes
  // (requires non-empty learning_objective); "none" — no read, no write.
  // Mirrors knowledgebase_access. Auto-inferred to "read-write" on save when
  // learning_objective is non-empty and this field is unset (backend migration).
  learnings_access?: 'read' | 'read-write' | 'none';
  learning_objective?: string;                // Extraction instruction for the post-step learning agent; required when learnings_access="read-write"
  selected_servers?: string[];
  selected_tools?: string[];
  enabled_custom_tools?: string[];
  enabled_custom_tool_categories?: string[];  // Legacy format kept for transitional frontend compatibility
  enabled_skills?: string[];
  enable_context_offloading?: boolean;
  use_code_execution_mode?: boolean;
  todo_task_orchestrator_tier?: number;       // 1/2/3 - tier for orchestrator agent in tiered mode
  orchestrator_llm?: AgentLLMConfig;          // Direct LLM override for orchestrator (works in both tiered and manual modes)
  sub_agent_llm?: AgentLLMConfig;             // Direct LLM override for ALL sub-agents spawned by this step (works in both tiered and manual modes)
  disable_parallel_tool_execution?: boolean;  // Disable parallel tool execution (default: enabled)
  disable_tier_optimization?: boolean;        // If true, execution agents always use Tier 1 (high reasoning)
  knowledgebase_access?: 'read' | 'write' | 'read-write' | 'none';
  knowledgebase_contribution?: string;
  db_access?: 'read' | 'write' | 'read-write' | 'none';
  description_reviewed?: boolean;
  review_notes?: string;
  // LEGACY, read-only (PLAT-287). A step's execution model is decided by its
  // plan `type` now — `regular` is scripted, `message_sequence` is agentic.
  // These two keys may still be present in a workflow's step_config.json until its
  // v1.0.39 contract migration strips them; a `regular` step still carrying
  // "agentic" is a legacy agentic step the runtime runs as a sequence. Nothing
  // writes them any more. Use effectiveExecutionMode() instead of reading them.
  declared_execution_mode?: string;
  declared_execution_mode_reason?: string;
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
  has_loop?: boolean;
  loop_condition?: string;
  max_iterations?: number;
  loop_description?: string;
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

/**
 * Matches new plan steps with existing configs by ID only
 * Returns a map of step index -> matched AgentConfigs
 */
export function matchStepConfigs(
  newSteps: TodoStep[] | TodoStepWithConfigs[],
  oldConfigs: StepConfig[]
): Map<number, AgentConfigs> {
  const result = new Map<number, AgentConfigs>();

  // Create lookup map: ID -> config
  const idConfigMap = new Map<string, AgentConfigs>();
  
  for (const stepConfig of oldConfigs) {
    if (stepConfig.agent_configs !== undefined && stepConfig.id) {
      idConfigMap.set(stepConfig.id, stepConfig.agent_configs);
    }
  }

  // Match new steps to old configs by ID only
  // Steps always have IDs from backend - throw error if missing
  for (let i = 0; i < newSteps.length; i++) {
    const step = newSteps[i];
    
    // Match by ID - step.id is required (always provided by backend)
    const stepId = (step as TodoStepWithConfigs).id;
    if (!stepId) {
      throw new Error(`Step at index ${i} is missing required ID field. Step title: "${step.title || 'unknown'}"`);
    }
    
    const config = idConfigMap.get(stepId);
    if (config) {
      result.set(i, config);
    }
    // If not found, step won't have config (use defaults)
  }

  return result;
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
  orchestrator_ids?: string[];
}

// Common fields shared by all step types
interface CommonStepFields {
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

// Regular step (may have loops)
export interface RegularPlanStep extends CommonStepFields {
  type: 'regular';
  script_only?: boolean;
  next_step_id?: string;                    // Explicit successor for scripted route chains
  has_loop?: boolean;
  loop_condition?: string;
  max_iterations?: number;
  loop_description?: string;
}

// Todo task step (orchestrator with todo list management + predefined routes + generic agent)
// Fields from the former inner todo_task_step are now flattened onto this level:
//   description, success_criteria, context_dependencies, context_output, validation_schema
// These are inherited from CommonStepFields already.
export interface TodoTaskPlanStep extends CommonStepFields {
  type: 'todo_task' | 'orchestrator';  // 'orchestrator' since contract v1.0.35; 'todo_task' is the legacy on-disk alias
  todo_task_step?: PlanStep;                // DEPRECATED: kept for backwards compat with old plan.json
  predefined_routes?: PlanRoutingRoute[];   // Predefined sub-agents with learning/prevalidation
  enable_generic_agent?: boolean;           // Allow generic execution agent (no learning/prevalidation)
  next_step_id?: string;                    // ID of step after todo task completes (or "end")
  messages?: TodoTaskMessage[];             // Optional scripted message sequence fed into the orchestrator's own conversation after its first turn
}

export interface TodoTaskMessage {
  id?: string;
  type?: 'message' | 'prevalidation' | 'foreach' | string;
  message?: string;                         // message entries: instruction for one orchestrator turn; foreach entries: the per-row template
  validation_schema?: ValidationSchema;     // prevalidation entries: the gate schema
  max_corrections?: number;                 // prevalidation only: corrective turns allowed on failure (default 1)
  source?: string;                          // foreach entries: workspace-relative JSON array file (e.g. db/tasks.json)
  source_path?: string;                     // foreach entries: optional dot-path to the array field
  max_iterations?: number;                  // foreach entries: optional cap on rows (0 = all)
}

export interface MessageSequenceWriteAccess {
  knowledgebase?: boolean;
  db?: boolean;
  learnings?: boolean;
}

export interface MessageSequenceItem {
  id: string;
  type: 'user_message' | 'prevalidation' | 'foreach' | 'scripted' | string;
  kind?: 'execution' | 'learning' | 'knowledgebase' | 'db' | 'check' | 'critique' | 'self_validation' | 'reference_check' | 'hallucination_check' | 'code_review' | string;
  title?: string;
  message?: string;
  write_access?: MessageSequenceWriteAccess;
  validation_schema?: ValidationSchema;
  prevalidation?: ValidationSchema;
  source_sql?: string;        // read-only query against db/db.sqlite
  source?: string;            // legacy display only
  source_path?: string;       // legacy display only
  scripted_steps?: { id: string; step_id: string; parameters?: Record<string, unknown> }[];
  max_parallel?: number;      // scripted batch concurrency, 0/1 = sequential
  max_iterations?: number;    // foreach items: optional cap on rows (0 = all)
}

export interface MessageSequencePlanStep extends CommonStepFields {
  type: 'message_sequence';
  authored_prompt?: boolean;
  system_prompt?: string;
  items?: MessageSequenceItem[];
  predefined_routes?: PlanRoutingRoute[]; // Optional bounded specialists selected by the sequence agent at runtime
  next_step_id?: string;
}

// Human input step (asks question to human and blocks for input)
export interface HumanInputPlanStep extends CommonStepFields {
  type: 'human_input';
  question: string;                      // Required: question to ask human
  variable_name?: string;                // Optional: store response in variable
  response_type?: string;                // "text" (default), "yesno", "multiple_choice"
  options?: string[];                    // For multiple_choice type
  next_step_id?: string;                 // Default destination after response (or "end")
  if_yes_next_step_id?: string;         // Optional: for yesno type when response is "yes"
  if_no_next_step_id?: string;          // Optional: for yesno type when response is "no"
  option_routes?: Record<string, string>; // Optional: for multiple_choice type - maps option index (as string "0", "1", etc.) or option value to next_step_id
}

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
export type PlanStep = RegularPlanStep | HumanInputPlanStep | TodoTaskPlanStep | MessageSequencePlanStep | RoutingPlanStep | BranchPlanStep | CrewPlanStep;

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
export function isRegularStep(step: PlanStep): step is RegularPlanStep {
  return step.type === 'regular';
}

export function isHumanInputStep(step: PlanStep): step is HumanInputPlanStep {
  return step.type === 'human_input';
}

export function isTodoTaskStep(step: PlanStep): step is TodoTaskPlanStep {
  return step.type === 'todo_task' || step.type === 'orchestrator';
}

// Preferred name; the plan type is `orchestrator` and `todo_task` is its legacy alias.
export const isOrchestratorStep = isTodoTaskStep;

export function isMessageSequenceStep(step: PlanStep): step is MessageSequencePlanStep {
  return step.type === 'message_sequence';
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

// The legacy declared mode, canonicalised the way the backend did
// (learn_code -> scripted, code_exec -> agentic), or undefined when absent.
function legacyDeclaredMode(step: ExecutionModeStepLike): string | undefined {
  const raw = step.agent_configs?.declared_execution_mode?.trim().toLowerCase();
  if (!raw) return undefined;
  if (raw === 'learn_code') return 'scripted';
  if (raw === 'code_exec') return 'agentic';
  return raw;
}

// The execution model a step actually runs with (PLAT-287): decided by the plan
// type, not by a config field. `regular` is scripted — its work is the
// checked-in learnings/<id>/main.py — and `message_sequence` is agentic. The
// one transitional exception: a `regular` step whose config still carries the
// legacy key with "agentic" (not yet stripped by the v1.0.39 migration) is a
// legacy agentic step the runtime still normalises into a sequence.
// Routing/branch/orchestrator/human-input/crew steps have no execution mode.
export function effectiveExecutionMode(
  step: ExecutionModeStepLike | null | undefined,
): EffectiveExecutionMode | undefined {
  if (!step) return undefined;
  const type = step.type || 'regular';
  if (type === 'regular') {
    return legacyDeclaredMode(step) === 'agentic' ? 'agentic' : 'scripted';
  }
  if (type === 'message_sequence') return 'agentic';
  return undefined;
}

// Display-only: the rationale a legacy config recorded next to its declared
// mode. Gone once the migration strips the key; nothing writes it any more.
export function effectiveExecutionModeReason(step: ExecutionModeStepLike | null | undefined): string | undefined {
  const reason = step?.agent_configs?.declared_execution_mode_reason?.trim();
  return reason || undefined;
}

// Which steps the message-sequence runtime executes. A `regular` step is a
// scripted step and runs through the scripted executor — except a legacy
// agentic one (config still carries declared_execution_mode "agentic", not yet
// stripped by v1.0.39), which the runtime normalises into a single-turn
// sequence exactly as it did before PLAT-287. Rendering such a step as a
// sequence keeps the canvas honest about the items it will report completing
// ("execute-and-verify"), which exist nowhere in plan.json.
//
// Mirrors shouldNormalizeRegularStepToMessageSequence / effectiveRuntimeStepType
// in controller_message_sequence.go. The legacy key reaches us from
// step_config.json, merged onto the step by usePlanData.
export function runsAsMessageSequence(step: PlanStep): boolean {
  if (isMessageSequenceStep(step)) return true;
  return isRegularStep(step) && legacyDeclaredMode(step) === 'agentic';
}

// The item list the runtime actually executes. An authored message_sequence
// supplies its own; a normalized regular step gets the single synthesized turn
// from normalizeRegularStepToMessageSequence, plus the validation gate
// appendMessageSequenceFinalValidation adds when the step declares a schema.
//
// Display only — these are never written back to plan.json, exactly as the Go
// side marks them Synthetic and keeps them out of serialization.
export function effectiveMessageSequenceItems(step: PlanStep): MessageSequenceItem[] {
  if (isMessageSequenceStep(step)) return step.items ?? [];
  if (!runsAsMessageSequence(step)) return [];

  const items: MessageSequenceItem[] = [{
    id: 'execute-and-verify',
    type: 'user_message',
    kind: 'execution',
    message: 'Complete the step described above. Re-open the produced evidence, verify the result against every stated requirement, repair any gap you find, and finish only when the step is complete.'
  }];
  if (step.validation_schema) {
    items.push({
      id: '__automatic_final_validation__',
      type: 'prevalidation',
      title: 'Final validation',
      validation_schema: step.validation_schema
    });
  }
  return items;
}

// Convert the former orchestrator `messages` shape into the canonical Agent
// item queue. This is display-only compatibility for plans which have not yet
// been rewritten as `message_sequence` steps.
export function legacyTodoMessagesAsSequenceItems(step: TodoTaskPlanStep): MessageSequenceItem[] {
  const items = (step.messages ?? []).map((message, index): MessageSequenceItem => {
    const id = message.id || `legacy-item-${index + 1}`;
    if (message.type === 'prevalidation') {
      return {
        id,
        type: 'prevalidation',
        validation_schema: message.validation_schema
      };
    }
    if (message.type === 'foreach') {
      return {
        id,
        type: 'foreach',
        message: message.message,
        source: message.source,
        source_path: message.source_path,
        max_iterations: message.max_iterations
      };
    }
    return {
      id,
      type: 'user_message',
      kind: 'execution',
      message: message.message
    };
  });

  if (items.length > 0) return items;
  return [{
    id: 'execute-step-charter',
    type: 'user_message',
    kind: 'execution',
    message: 'Execute the step charter and verify its success criteria.'
  }];
}

// The Plan Design UI has one Agent concept. Canonical message sequences and
// legacy orchestrator/todo_task records differ only in their stored shape.
export function effectiveAgentItems(step: PlanStep): MessageSequenceItem[] {
  return isTodoTaskStep(step)
    ? legacyTodoMessagesAsSequenceItems(step)
    : effectiveMessageSequenceItems(step);
}

export function isRoutingStep(step: PlanStep): step is RoutingPlanStep {
  return step.type === 'routing';
}

export function isBranchStep(step: PlanStep): step is BranchPlanStep {
  return step.type === 'branch';
}

// A deterministic N-way switch -- either flavor. Same shape/mechanics,
// different intent (routing = major fork, branch = small in-flow decision).
export function isRouteSwitchStep(step: PlanStep): step is RoutingPlanStep | BranchPlanStep {
  return isRoutingStep(step) || isBranchStep(step);
}
