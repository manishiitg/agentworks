package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// Vault management uses the same authenticated identity and role checks as
// the rest of the product. The gateway service credential stays on the server.
func (api *StreamingAPI) handleCapLayerAdmin(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		writeUsersError(w, http.StatusUnauthorized, "sign in to the product")
		return
	}
	if !currentUserIsAdmin(r) || !userAllowedProduct(claims, "mcp-gateway") {
		writeUsersError(w, http.StatusForbidden, "Vault management requires an administrator account")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/caplayer")
	if !strings.HasPrefix(path, "/api/admin/") || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}
	// User accounts and roles belong to the existing directory. Gateway user
	// records only bind these identities to tool access groups.
	if path == "/api/admin/users" {
		if r.Method != http.MethodGet {
			writeUsersError(w, http.StatusMethodNotAllowed, "manage accounts in Users & access")
			return
		}
		dir, err := readUserDirectoryFile()
		if err != nil {
			writeUsersError(w, http.StatusServiceUnavailable, "user directory unavailable")
			return
		}
		users := make([]map[string]string, 0, len(dir.Users))
		for _, user := range dir.Users {
			if !user.Disabled {
				users = append(users, map[string]string{"ID": user.ID, "Email": user.Email, "WorkspaceID": ""})
			}
		}
		if !IsMultiUserMode() && len(users) == 0 {
			users = append(users, map[string]string{"ID": claims.UserID, "Email": claims.Email, "WorkspaceID": ""})
		}
		writeUsersJSON(w, http.StatusOK, map[string]any{"users": users})
		return
	}
	if path == "/api/admin/users/sync" {
		http.NotFound(w, r)
		return
	}
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		writeUsersError(w, http.StatusServiceUnavailable, "Vault service is not configured")
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(path, "/api/admin/groups/") && strings.HasSuffix(path, "/members") {
		data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil || len(data) > 65536 {
			writeUsersError(w, http.StatusBadRequest, "invalid membership request")
			return
		}
		var in struct {
			UserID string `json:"user_id"`
		}
		if json.Unmarshal(data, &in) != nil || in.UserID == "" {
			writeUsersError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		dir, err := readUserDirectoryFile()
		if err != nil {
			writeUsersError(w, http.StatusServiceUnavailable, "user directory unavailable")
			return
		}
		user := dir.byID(in.UserID)
		if user == nil && !IsMultiUserMode() && in.UserID == claims.UserID {
			user = &UserRecord{ID: claims.UserID, Email: claims.Email}
		}
		if user == nil || user.Disabled {
			writeUsersError(w, http.StatusBadRequest, "select an active product user")
			return
		}
		payload, _ := json.Marshal(map[string]string{"ID": user.ID, "Email": user.Email})
		syncURL := *target
		syncURL.Path = strings.TrimRight(target.Path, "/") + "/api/admin/users/sync"
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, syncURL.String(), bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			writeUsersError(w, http.StatusBadGateway, "Vault service unavailable")
			return
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			writeUsersError(w, http.StatusBadGateway, "could not bind the product identity")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		r.ContentLength = int64(len(data))
	}
	deletingConnectionID := ""
	if r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/admin/connectors/") {
		id := strings.TrimPrefix(path, "/api/admin/connectors/")
		if vaultConnectionID.MatchString(id) {
			deletingConnectionID = id
		}
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimRight(target.Path, "/") + path
			pr.Out.URL.RawPath = ""
			query := pr.Out.URL.Query()
			query.Del("token")
			query.Del("access_token")
			pr.Out.URL.RawQuery = query.Encode()
			// Never forward browser cookies, its JWT, or client-supplied identity headers.
			headers := make(http.Header)
			headers.Set("Accept", r.Header.Get("Accept"))
			headers.Set("Content-Type", r.Header.Get("Content-Type"))
			headers.Set("Authorization", "Bearer "+secret)
			headers.Set("X-CapLayer-Actor", claims.UserID)
			pr.Out.Header = headers
		},
		Transport: capLayerTransport,
		ModifyResponse: func(resp *http.Response) error {
			if deletingConnectionID != "" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				mutex := platformMCPOAuthMutex("vault:" + deletingConnectionID)
				mutex.Lock()
				advanceVaultOAuthGeneration(deletingConnectionID)
				removeVaultCredentialFiles(deletingConnectionID)
				mutex.Unlock()
			}
			resp.Header.Del("Set-Cookie")
			// A service credential failure is a deployment error, not a failed
			// product login. Avoid sending the browser into an auth retry loop.
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				resp.Body.Close()
				body := `{"error":"Vault service authentication is not configured correctly"}`
				resp.Body = io.NopCloser(strings.NewReader(body))
				resp.StatusCode = http.StatusBadGateway
				resp.ContentLength = int64(len(body))
				resp.Header.Del("Content-Length")
				resp.Header.Set("Content-Type", "application/json")
			}
			// Administrative service redirects must not send the browser elsewhere.
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errors.New("unexpected gateway redirect")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeUsersError(w, http.StatusBadGateway, "Vault service unavailable")
		},
	}
	proxy.ServeHTTP(w, r)
}

func capLayerServiceConfig() (*url.URL, string, error) {
	target, err := url.Parse(strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")))
	if err != nil || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return nil, "", errors.New("invalid service URL")
	}
	ip := net.ParseIP(target.Hostname())
	loopback := target.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if target.Scheme != "https" && !(target.Scheme == "http" && loopback) {
		return nil, "", errors.New("service requires HTTPS or loopback")
	}
	secret := strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_TOKEN"))
	if file := strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_TOKEN_FILE")); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, "", err
		}
		secret = strings.TrimSpace(string(data))
	}
	if len(secret) < 32 {
		return nil, "", errors.New("missing service credential")
	}
	return target, secret, nil
}

var capLayerTransport = &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 90 * time.Second}

// capLayerAgentAccess does not trust the model, a browser role, or a stale
// session. Administrator access is checked on every individual tool call.
func capLayerAgentAccess(ctx context.Context, userID, operation string, arguments json.RawMessage) (string, error) {
	claims := &UserClaims{UserID: userID}
	access := userAccessForClaims(claims)
	if userID == "" || !access.Admin || access.Disabled || !userAllowedProduct(claims, "mcp-gateway") {
		return "", errors.New("Vault management requires an administrator account")
	}
	switch operation {
	case "inspect_environment", "inspect_tool", "save_draft", "connect_server":
	default:
		return "", errors.New("unsupported Vault operation")
	}
	payload, err := json.Marshal(map[string]any{"operation": operation, "arguments": arguments})
	if err != nil {
		return "", err
	}
	return capLayerAgentRequest(ctx, userID, "/api/admin/setup/tool", payload)
}

func capLayerAgentRequest(ctx context.Context, userID, path string, payload json.RawMessage) (string, error) {
	claims := &UserClaims{UserID: userID}
	access := userAccessForClaims(claims)
	if userID == "" || !access.Admin || access.Disabled || !userAllowedProduct(claims, "mcp-gateway") {
		return "", errors.New("Vault management requires an administrator account")
	}
	target, secret, err := capLayerServiceConfig()
	if err != nil {
		return "", errors.New("Vault service is not configured")
	}
	target.Path = strings.TrimRight(target.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CapLayer-Actor", userID)
	client := &http.Client{Transport: capLayerTransport, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("Vault service unavailable")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(body) > 2*1024*1024 {
		return "", errors.New("Vault response exceeded limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return "", errors.New("Vault service authentication failed")
		}
		return "", fmt.Errorf("Vault operation failed (%d): %s", resp.StatusCode, body)
	}
	return string(body), nil
}

func canUseCapLayerProfile(ctx context.Context, profileID string) bool {
	if profileID != caplayerproduct.ProfileID {
		return true
	}
	claims := GetUserFromContext(ctx)
	access := userAccessForClaims(claims)
	return claims != nil && access.Admin && !access.Disabled && userAllowedProduct(claims, "mcp-gateway")
}
