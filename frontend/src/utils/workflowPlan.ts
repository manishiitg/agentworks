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

export interface StepInputBinding { value?: unknown; input?: string; output?: string; field?: string }

// Scripted step
export interface ScriptedPlanStep extends CommonStepFields {
  type: 'scripted' | 'regular'; // regular is a saved-plan compatibility alias.
  script?: string;
  script_inputs?: Record<string, { value?: unknown; input?: string; output?: string; field?: string }>;
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
  inputs?: Record<string, StepInputBinding>;
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


// Discriminated union type for all step types
export type PlanStep = ScriptedPlanStep | AgentPlanStep | RoutingPlanStep | BranchPlanStep;

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
/** Narrow the saved read contract when merging recorded agent configuration. */
export function isAgentStep(step: PlanStep): step is AgentPlanStep {
  return step.type === 'agent';
}
