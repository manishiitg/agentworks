package mcpclient

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/oauth"
)

// lastRefusals holds the latest error answer of each MCP server URL (host+path), so a failed connect can say what
// the server said (e.g. Slack: "App is not enabled for Slack MCP server access") instead of a generic transport error.
var lastRefusals sync.Map

// serverRefusal is the latest plain reason the server at url gave for refusing a request, or "".
func serverRefusal(url string) string {
	key := strings.TrimPrefix(strings.TrimPrefix(strings.SplitN(url, "?", 2)[0], "https://"), "http://")
	if value, ok := lastRefusals.Load(key); ok {
		return value.(string)
	}
	return ""
}

// refusalReason prefers a JSON-RPC error message; otherwise the body excerpt.
func refusalReason(status int, body string) string {
	var rpc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &rpc) == nil && strings.TrimSpace(rpc.Error.Message) != "" {
		return strings.TrimSpace(rpc.Error.Message)
	}
	if body == "" {
		return http.StatusText(status)
	}
	return body
}

// statusLoggingTransport records an MCP server's error answer (status, WWW-Authenticate and the start of the body).
// The MCP transport turns any 4xx on initialize into one generic "legacy SSE server" error, which hid why a hosted
// server refused the connection. Request headers (tokens) are never logged.
type statusLoggingTransport struct{ base http.RoundTripper }

func (t statusLoggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil || resp == nil || resp.StatusCode < 400 {
		return resp, err
	}
	var excerpt []byte
	if resp.Body != nil {
		excerpt, _ = io.ReadAll(io.LimitReader(resp.Body, 512))
		rest := resp.Body
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(excerpt), rest), rest}
	}
	body := strings.Join(strings.Fields(string(excerpt)), " ")
	if len(body) > 300 {
		body = body[:300]
	}
	log.Printf("[MCP_HTTP] %s %s%s returned %d www-authenticate=%q body=%q", r.Method, r.URL.Host, r.URL.Path, resp.StatusCode, resp.Header.Get("WWW-Authenticate"), body)
	lastRefusals.Store(r.URL.Host+r.URL.Path, refusalReason(resp.StatusCode, body))
	return resp, nil
}

// withStatusLogging returns a copy of client (or a default one) whose transport logs error answers.
func withStatusLogging(client *http.Client) *http.Client {
	out := &http.Client{}
	if client != nil {
		copied := *client
		out = &copied
	}
	base := out.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out.Transport = statusLoggingTransport{base: base}
	return out
}

// oauthBearerTransport fetches the current OAuth access token (refreshing when
// expired) on every request so pooled HTTP/SSE clients never stay pinned to a
// stale initial Bearer header.
type oauthBearerTransport struct {
	base    http.RoundTripper
	manager *oauth.Manager
}

func (t oauthBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	token, err := t.manager.GetAccessToken(r.Context())
	if err != nil {
		return nil, err
	}
	cloned := r.Clone(r.Context())
	cloned.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(cloned)
}

// withOAuthBearer returns a copy of client (or a default one) whose transport
// injects a fresh OAuth Bearer token from mgr on each outgoing request.
func withOAuthBearer(client *http.Client, mgr *oauth.Manager) *http.Client {
	if mgr == nil {
		return client
	}
	out := &http.Client{}
	if client != nil {
		copied := *client
		out = &copied
	}
	base := out.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out.Transport = oauthBearerTransport{base: base, manager: mgr}
	return out
}

// OnConnectResult, when set by the host application, receives each server connect outcome (err nil on success), so
// it can show a person why one of their connections does not work.
var OnConnectResult func(serverName string, err error)

// ReportConnectResult passes one connect outcome to OnConnectResult.
func ReportConnectResult(serverName string, err error) {
	if hook := OnConnectResult; hook != nil {
		hook(serverName, err)
	}
}
