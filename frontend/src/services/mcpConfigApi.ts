import { getApiBaseUrl, getAuthToken } from './api';

// Helper to get auth headers
function getAuthHeaders(): HeadersInit {
  const headers: HeadersInit = { 'Content-Type': 'application/json' };
  const token = getAuthToken();
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  return headers;
}

export interface MCPConfigResponse {
  status: string;
  message?: string;
  servers?: number;
}

export interface ServerLogEntry {
  timestamp: string;
  level: string;  // "info" | "error" | "warn" | "debug"
  message: string;
}

export interface ServerLogsResponse {
  logs: Record<string, ServerLogEntry[]>;
}

export interface MCPConfigStatus {
  config_path: string;
  total_servers: number;
  base_servers: number;
  base_server_names?: string[];
  discovered_servers: number;
  discovery_running: boolean;
  last_discovery: string;
  cache_stats: {
    total_entries: number;
    hit_rate: number;
  };
  mcp_config_locked?: boolean;
}

export class MCPConfigApi {
  private configuredBaseUrl?: string;

  constructor(baseUrl?: string) {
    this.configuredBaseUrl = baseUrl;
  }

  // Resolve the active workspace at request time. Eager initialization creates
  // an api/store import cycle and holds the previous workspace after switching.
  private get baseUrl(): string {
    return this.configuredBaseUrl ?? getApiBaseUrl();
  }

  /**
   * Get current MCP configuration
   */
  async getConfig(): Promise<unknown> {
    const response = await fetch(`${this.baseUrl}/api/mcp-config`, {
      headers: getAuthHeaders()
    });
    if (!response.ok) {
      throw new Error(`Failed to get config: ${response.statusText}`);
    }
    return response.json();
  }

  /**
   * Save MCP configuration
   */
  async saveConfig(config: unknown): Promise<MCPConfigResponse> {
    const response = await fetch(`${this.baseUrl}/api/mcp-config`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify({ config }),
    });

    if (!response.ok) {
      // The server may answer with JSON or plain text depending on which layer
      // rejected the request, so read it as text and only then try to parse.
      // Assuming JSON here used to throw, and the editor reported every server
      // rejection as "Invalid JSON format".
      const body = await response.text();
      let message = body;
      try {
        const parsed = JSON.parse(body);
        message = parsed.error || parsed.message || body;
      } catch {
        // not JSON; the raw text is the message
      }
      throw new Error(message.trim() || `Failed to save config: ${response.statusText}`);
    }

    return response.json();
  }

  /**
   * Trigger server discovery
   */
  async discoverServers(): Promise<MCPConfigResponse> {
    const response = await fetch(`${this.baseUrl}/api/mcp-config/discover`, {
      method: 'POST',
      headers: getAuthHeaders(),
    });

    if (!response.ok) {
      const errorData = await response.json();
      throw new Error(errorData.message || `Failed to start discovery: ${response.statusText}`);
    }

    return response.json();
  }

  /**
   * Get server install/connection logs
   */
  async getServerLogs(serverName?: string): Promise<ServerLogsResponse> {
    const params = serverName ? `?server_name=${encodeURIComponent(serverName)}` : '';
    const response = await fetch(`${this.baseUrl}/api/mcp-config/logs${params}`, {
      headers: getAuthHeaders()
    });
    if (!response.ok) {
      throw new Error(`Failed to get server logs: ${response.statusText}`);
    }
    return response.json();
  }

  /**
   * Get configuration status
   */
  async getStatus(): Promise<MCPConfigStatus> {
    const response = await fetch(`${this.baseUrl}/api/mcp-config/status`, {
      headers: getAuthHeaders()
    });
    if (!response.ok) {
      throw new Error(`Failed to get status: ${response.statusText}`);
    }
    return response.json();
  }

}

// Export a default instance
export const mcpConfigApi = new MCPConfigApi();
