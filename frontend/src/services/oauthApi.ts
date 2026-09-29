/**
 * OAuth API Service
 * Handles OAuth authentication flows for MCP servers
 */

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

export interface OAuthStartRequest {
  server_name: string;
  client_id?: string;
  client_secret?: string;
}

export interface OAuthStartResponse {
  server_name: string;
  auth_url: string;
  state: string;
  message: string;
}

export interface OAuthDiscoveryResponse {
  status: 'needs_client_id';
  server_name: string;
  auth_url?: string;
  token_url?: string;
  resource?: string;
  scopes_supported?: string[];
  message: string;
  /** The callback URL to register on the OAuth app. */
  redirect_uri?: string;
  needs_client_secret?: boolean;
}

export interface OAuthStatusResponse {
  server_name: string;
  valid: boolean;
  /** False for servers with no oauth block. Absent on older responses. */
  has_oauth?: boolean;
  expires_in?: string;
  token_path?: string;
}

export interface OAuthLogoutRequest {
  server_name: string;
}

export class OAuthApi {
  private baseUrl: string;

  constructor(baseUrl: string = getApiBaseUrl()) {
    this.baseUrl = baseUrl;
  }

  /**
   * Start OAuth flow for a server
   * Returns OAuthDiscoveryResponse if server needs a client_id, otherwise OAuthStartResponse
   */
  async startOAuthFlow(serverName: string, clientId?: string, clientSecret?: string): Promise<OAuthStartResponse | OAuthDiscoveryResponse> {
    const body: OAuthStartRequest = { server_name: serverName };
    if (clientId) {
      body.client_id = clientId;
      if (clientSecret) body.client_secret = clientSecret;
    }

    const response = await fetch(`${this.baseUrl}/api/oauth/start`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify(body),
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(`OAuth start failed: ${error}`);
    }

    return response.json();
  }

  /**
   * Get OAuth token status for a server
   */
  async getOAuthStatus(serverName: string): Promise<OAuthStatusResponse> {
    const response = await fetch(
      `${this.baseUrl}/api/oauth/status?server_name=${encodeURIComponent(serverName)}`,
      { headers: getAuthHeaders() }
    );

    if (!response.ok) {
      const error = await response.text();
      throw new Error(`Failed to get OAuth status: ${error}`);
    }

    return response.json();
  }

  /**
   * Logout from OAuth (remove token)
   */
  async logout(serverName: string): Promise<void> {
    const response = await fetch(`${this.baseUrl}/api/oauth/logout`, {
      method: 'POST',
      headers: getAuthHeaders(),
      body: JSON.stringify({ server_name: serverName }),
    });

    if (!response.ok) {
      const error = await response.text();
      throw new Error(`OAuth logout failed: ${error}`);
    }
  }
}

// Export a default instance
export const oauthApi = new OAuthApi(getApiBaseUrl());
