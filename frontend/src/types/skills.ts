// Skill types matching backend structures

export interface SkillFrontmatter {
  name: string;
  description: string;
  argument_hint?: string;
  allowed_tools?: string[];
  model?: string;
}

export interface Skill {
  frontmatter: SkillFrontmatter;
  content: string;
  folder_name: string;
  file_path: string;
  source_url?: string;
  managed?: boolean;
  file_managed?: boolean;
  builtin?: boolean;
  used_by?: string[];
  source?: string;
  source_type?: string;
  brain_source?: string;
  brain_edited?: boolean;
  validation_error?: string;
}

export interface ImportSkillRequest {
  github_url: string;
  github_token?: string;
}

export interface ImportSkillResponse {
  success: boolean;
  skill_name?: string;
  error?: string;
}

export interface ValidateSkillRequest {
  github_url: string;
  github_token?: string;
}

export interface ValidateSkillResponse {
  valid: boolean;
  frontmatter?: SkillFrontmatter;
  error?: string;
  files?: string[];
  exists?: boolean;
}

export interface ListSkillsResponse {
  skills: Skill[];
  total: number;
  usage?: Record<string, string[]>;
}

export interface RuntimeSkill extends Skill {
  files: { path: string; size: number; modified_at: string; error?: string }[];
}

export interface RuntimeSkillsResponse {
  skills: RuntimeSkill[];
  folders: string[];
  warnings: string[];
  session_id: string;
  provider?: string;
  directory?: string;
  prepared: boolean;
  message?: string;
  read_at: string;
}
