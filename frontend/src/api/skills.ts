import axios from 'axios';
import { getApiBaseUrl, getAuthToken } from '../services/api';
import type {
  Skill,
  ImportSkillRequest,
  ImportSkillResponse,
  ValidateSkillRequest,
  ValidateSkillResponse,
  ListSkillsResponse,
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
  const authToken = getAuthToken()
  if (authToken && config.headers) {
    config.headers['Authorization'] = `Bearer ${authToken}`
  }
  return config
})

export const skillsApi = {
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

export default skillsApi;
