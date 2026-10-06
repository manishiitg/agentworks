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
  used_by?: string[];
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
