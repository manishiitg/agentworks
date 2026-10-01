package server

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
)

var platformMCPOAuthLocks sync.Map

// Serialize refresh, sign-in and logout for the shared platform identity.
func platformMCPOAuthMutex(name string) *sync.Mutex {
	lock, _ := platformMCPOAuthLocks.LoadOrStore(strings.ToLower(name), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// Service-only broker: tokens never pass through a browser, chat or gateway
// admin inventory. Reuse the same platform identity, sealed store and refresh
// manager as every other product. An exact configured URL prevents sending a
// platform credential to a different/custom endpoint with a similar name.
func (api *StreamingAPI) handleCapLayerOAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_, secret, err := capLayerServiceConfig()
	supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err != nil || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(supplied), []byte(secret)) != 1 {
		writeUsersError(w, http.StatusUnauthorized, "service authentication required")
		return
	}
	var in struct {
		ServerName string `json:"server_name"`
		URL        string `json:"url"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || dec.Decode(&struct{}{}) != io.EOF || in.ServerName == "" || in.URL == "" {
		writeUsersError(w, http.StatusBadRequest, "server name and URL required")
		return
	}
	config, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		writeUsersError(w, http.StatusServiceUnavailable, "MCP configuration unavailable")
		return
	}
	name, cfg, err := config.ResolveServer(in.ServerName)
	if err != nil || cfg.OAuth == nil || cfg.URL != in.URL {
		writeUsersError(w, http.StatusForbidden, "OAuth connection does not match the configured server")
		return
	}
	copied := *cfg.OAuth
	if copied.TokenFile == "" {
		copied.TokenFile = getUserTokenFilePath(platformMCPTokenUserID, name)
	}
	mutex := platformMCPOAuthMutex(name)
	mutex.Lock()
	defer mutex.Unlock()
	token, err := oauth.NewManager(&copied, api.logger).GetAccessToken(r.Context())
	if err != nil || token == "" {
		writeUsersError(w, http.StatusUnauthorized, "administrator sign-in required")
		return
	}
	writeUsersJSON(w, http.StatusOK, map[string]string{"access_token": token})
}
