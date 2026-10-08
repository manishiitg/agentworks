// Package agentworksclient connects terminal and MCP clients to a hosted AgentWorks server.
// Workflow validation and mutations always remain on the server.
package agentworksclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

const MaxBodyBytes = 16 << 20

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type APIError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("AgentWorks %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

type Client struct {
	baseURL       string
	token         string
	tokenProvider func(context.Context) (string, error)
	http          *http.Client
	// executorTrace, when set, is told about every request the local executor served (agentworks start --debug).
	executorTrace func(request localfiles.Request, response localfiles.Response, took time.Duration)
}

// WithExecutorTrace reports each served file or shell request, for debugging a local connection.
func (c *Client) WithExecutorTrace(trace func(request localfiles.Request, response localfiles.Response, took time.Duration)) *Client {
	c.executorTrace = trace
	return c
}

// WithTokenProvider resolves a browser-login credential before each request.
// Long-running MCP bridges can refresh without restarting.
func (c *Client) WithTokenProvider(provider func(context.Context) (string, error)) *Client {
	c.tokenProvider = provider
	return c
}

func (c *Client) authToken(ctx context.Context) (string, error) {
	if c.tokenProvider != nil {
		return c.tokenProvider(ctx)
	}
	if c.token == "" {
		return "", errors.New("not logged in: use agentworks login or AGENTWORKS_TOKEN")
	}
	return c.token, nil
}

// ValidateServer requires TLS except for explicitly local development servers.
func ValidateServer(serverURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("server must be an HTTPS URL without credentials, query, or fragment")
	}
	local := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		local = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", errors.New("HTTPS is required (HTTP is allowed only for loopback servers)")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func New(serverURL, token string) (*Client, error) {
	base, err := ValidateServer(serverURL)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("invalid token")
	}
	return &Client{baseURL: base, token: token, http: &http.Client{
		Timeout: 90 * time.Second,
		// Never replay credentials or mutations to a redirected location.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body any, auth bool) (json.RawMessage, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		if len(data) > MaxBodyBytes {
			return nil, errors.New("request exceeds 16 MiB limit")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if auth {
		token, err := c.authToken(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("AgentWorks request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(data) > MaxBodyBytes {
		return nil, errors.New("response exceeds 16 MiB limit; narrow the request")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := &APIError{Status: resp.StatusCode, Code: "http_error", Message: http.StatusText(resp.StatusCode)}
		var envelope struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && len(envelope.Error) > 0 {
			var detail struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(envelope.Error, &detail) == nil && detail.Message != "" {
				e.Message = detail.Message
				if detail.Code != "" {
					e.Code = detail.Code
				}
			} else {
				var message string
				if json.Unmarshal(envelope.Error, &message) == nil {
					e.Message = message
				}
			}
		}
		if len(e.Message) > 2048 {
			e.Message = e.Message[:2048] + "…"
		}
		return nil, e
	}
	// Existing lifecycle handlers use 204 for successful cancellation. Keep
	// the client result valid JSON for CLI and MCP consumers.
	if resp.StatusCode == http.StatusNoContent && len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("server returned an invalid JSON response")
	}
	return json.RawMessage(data), nil
}

func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	data, err := c.request(ctx, http.MethodGet, "/api/external/v1/tools", nil, true)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode tools: %w", err)
	}
	seen := make(map[string]bool)
	for _, tool := range envelope.Tools {
		var schema map[string]any
		if tool.Name == "" || seen[tool.Name] || json.Unmarshal(tool.InputSchema, &schema) != nil || schema["type"] != "object" {
			return nil, errors.New("server returned an invalid or duplicate tool definition")
		}
		seen[tool.Name] = true
	}
	return envelope.Tools, nil
}

func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (json.RawMessage, error) {
	if name == "" {
		return nil, errors.New("tool name is required")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return c.request(ctx, http.MethodPost, "/api/external/v1/call", struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}{name, arguments}, true)
}

func (c *Client) Login(ctx context.Context, provider, username, password string) (string, error) {
	data, err := c.request(ctx, http.MethodPost, "/api/auth/login", struct {
		Provider string `json:"provider,omitempty"`
		Username string `json:"username"`
		Password string `json:"password"`
	}{provider, username, password}, false)
	if err != nil {
		return "", err
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", fmt.Errorf("decode login: %w", err)
	}
	if response.Token == "" || strings.ContainsAny(response.Token, "\r\n") {
		return "", errors.New("login did not return a valid token")
	}
	return response.Token, nil
}
