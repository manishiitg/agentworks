import axios from 'axios';
import { getApiBaseUrl, getAuthToken } from '../services/api';
import type {
  Skill,
  ImportSkillRequest,
  ImportSkillResponse,
  ValidateSkillRequest,
  ValidateSkillResponse,
  ListSkillsResponse,
  RuntimeSkillsResponse,
} from '../types/skills';

const API_BASE_URL = getApiBaseUrl();

const api = axios.create({
  baseURL: API_BASE_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Add auth token interceptor
api.interceptors.request.use((config) => {
  config.baseURL = getApiBaseUrl()
  const authToken = getAuthToken()
  if (authToken && config.headers) {
    config.headers['Authorization'] = `Bearer ${authToken}`
  }
  return config
})

export const skillsApi = {
  folder: async (workspacePath: string, files: File[], importing: boolean): Promise<{ document?: string; name?: string; files?: string[]; success?: boolean; skill_name?: string }> => {
    const data = new FormData()
    data.append('paths', JSON.stringify(files.map(file => file.webkitRelativePath)))
    files.forEach((file,index) => data.append(`file${index}`,file))
    return (await api.post(`/api/skills/${importing ? 'import-folder' : 'validate-folder'}`,data,{ params: { workspace_path: workspacePath }, headers: { 'Content-Type': 'multipart/form-data' } })).data
  },
  discover: async (workspacePath: string, provider: string, id?: string): Promise<NativeSkillDiscovery | DiscoveredSkill> => {
    return (await api.get('/api/skills/discover', { params: { workspace_path: workspacePath, provider, id } })).data
  },
  importDiscovered: async (workspacePath: string, provider: string, id: string): Promise<ImportSkillResponse> => {
    return (await api.post('/api/skills/discover', { id }, { params: { workspace_path: workspacePath, provider } })).data
  },
  workspaceSummary: async (workspacePath: string): Promise<{ custom_count: number; total: number }> => {
    return (await api.get('/api/skills', { params: { workspace_path: workspacePath, summary: true } })).data;
  },
  runtimeSkills: async (workspacePath: string, sessionId?: string | null): Promise<RuntimeSkillsResponse> => {
    const response = await api.get('/api/skills/runtime', { params: { workspace_path: workspacePath, session_id: sessionId || undefined } });
    return response.data;
  },

  runtimeSkillFile: async (workspacePath: string, sessionId: string, file: string): Promise<{ path: string; content: string; session_id: string }> => {
    const response = await api.get('/api/skills/runtime', { params: { workspace_path: workspacePath, session_id: sessionId, file } });
    return response.data;
  },
  // List all skills
  listSkills: async (workspacePath?: string | null): Promise<ListSkillsResponse> => {
    if (!workspacePath) return { skills: [], total: 0, usage: {} };
    const response = await api.get('/api/skills', { params: { workspace_path: workspacePath } });
    return response.data;
  },

  // Get a specific skill by name
  getSkill: async (name: string, workspacePath: string): Promise<Skill> => {
    const response = await api.get(`/api/skills/${encodeURIComponent(name)}`, { params: { workspace_path: workspacePath } });
    return response.data;
  },

  // Import a skill from GitHub
  importSkill: async (request: ImportSkillRequest, workspacePath: string): Promise<ImportSkillResponse> => {
    const response = await api.post('/api/skills/import', request, { params: { workspace_path: workspacePath } });
    return response.data;
  },

  // Validate a GitHub URL before importing
  validateSkill: async (request: ValidateSkillRequest, workspacePath: string): Promise<ValidateSkillResponse> => {
    const response = await api.post('/api/skills/validate', request, { params: { workspace_path: workspacePath } });
    return response.data;
  },

  // Validate a skill from uploaded zip file
  validateSkillZip: async (file: File, workspacePath: string): Promise<ValidateSkillResponse> => {
    const formData = new FormData();
    formData.append('file', file);
    const response = await api.post('/api/skills/validate-zip', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }, params: { workspace_path: workspacePath }
    });
    return response.data;
  },

  // Import a skill from uploaded zip file
  importSkillZip: async (file: File, workspacePath: string): Promise<ImportSkillResponse> => {
    const formData = new FormData();
    formData.append('file', file);
    const response = await api.post('/api/skills/import-zip', formData, {
      headers: { 'Content-Type': 'multipart/form-data' }, params: { workspace_path: workspacePath }
    });
    return response.data;
  },

  // Delete a skill
  deleteSkill: async (name: string, workspacePath: string): Promise<void> => {
    await api.delete(`/api/skills/${encodeURIComponent(name)}`, { params: { workspace_path: workspacePath } });
  },
};

export interface DiscoveredSkill {
  id: string; name: string; description: string; source: string; user_invocable: boolean
  document?: string; files?: string[]
}
export interface NativeSkillDiscovery { skills: DiscoveredSkill[]; message: string }

export default skillsApi;
