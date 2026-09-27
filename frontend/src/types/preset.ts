import type { PlannerFile } from '../services/api-types';
import type { PresetLLMConfig } from '../services/api-types';

export interface CustomPreset {
  id: string;
  label: string;
  icon?: string;
  query?: string;
  createdAt: number;
  selectedServers?: string[];
  selectedTools?: string[]; // NEW: Array of "server:tool" strings
  selectedSkills?: string[]; // Skill folder names for workflow
  selectedSecrets?: string[]; // Secret names for workflow injection
  selectedGlobalSecretNames?: string[] | null; // null=all global secrets, []=none, [...]=specific
  agentMode?: 'multi-agent' | 'workflow';
  workflowKind?: 'relay' | 'workflow';
  selectedFolder?: PlannerFile; // Single folder
  llmConfig?: PresetLLMConfig; // LLM configuration for this preset
  useCodeExecutionMode?: boolean; // MCP code execution mode
  enableBrowserAccess?: boolean; // Deprecated: use browserMode
  browserMode?: 'none' | 'auto' | 'headless' | 'cdp'; // Browser mode (source of truth)
  cdpPorts?: number[]; // Specialized independent Chrome profiles for multi-login testing
  employee_id?: string; // Assigned employee ID
}

export interface PredefinedPreset {
  id: string
  label: string
  icon?: string
  query?: string
  selectedServers?: string[];
  selectedTools?: string[]; // NEW: Array of "server:tool" strings
  selectedSkills?: string[]; // Skill folder names for workflow
  selectedGlobalSecretNames?: string[] | null; // null=all global secrets, []=none, [...]=specific
  agentMode?: 'multi-agent' | 'workflow';
  workflowKind?: 'relay' | 'workflow';
  selectedFolder?: PlannerFile;
  llmConfig?: PresetLLMConfig; // LLM configuration for this preset
  useCodeExecutionMode?: boolean; // MCP code execution mode
  enableBrowserAccess?: boolean; // Deprecated: use browserMode
  browserMode?: 'none' | 'auto' | 'headless' | 'cdp'; // Browser mode (source of truth)
  cdpPorts?: number[];
  employee_id?: string; // Assigned employee ID
}
