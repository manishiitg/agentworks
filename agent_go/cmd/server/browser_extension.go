package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

const browserExtensionConnectPath = "/api/browser/extension/connect"

func (api *StreamingAPI) projectExtensionAccess(r *http.Request, workspace, profile string) (string, error) {
	claims := GetUserFromContext(r.Context())
	normalized := normalizeConversationWorkspace(workspace)
	if (profile == "" || profile == "workflow") && strings.HasPrefix(normalized, "Workflow/") && strings.Count(normalized, "/") == 1 {
		if claims == nil || !userAllowedProduct(claims, "agentworks") {
			return "", fmt.Errorf("Workflow product access required")
		}
		if _, err := api.browserWorkspaceAccess(r, workspace, "workflow", true); err != nil {
			return "", err
		}
		_, manifest := workflowAccessForWorkspacePath(r.Context(), claims, workspace)
		if manifest == nil || manifest.Kind == "relay" {
			return "", fmt.Errorf("Open a workflow browser")
		}
		return workspace, nil
	}
	if !((profile == "code" && isCodeProjectPath(workspace)) || (profile == "work" && isCrewProjectPath(workspace))) {
		return "", fmt.Errorf("Browser extension access requires Code, Crew or a workflow")
	}
	if claims == nil || !userAllowedProduct(claims, profile) {
		return "", fmt.Errorf("Product access required")
	}
	if !crewProjectOwnedByCaller(claims.UserID, workspace) {
		return "", fmt.Errorf("Project ownership required")
	}
	return api.browserWorkspaceAccess(r, workspace, profile, true)
}
func (api *StreamingAPI) handleBrowserExtension(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace_path")
	if _, err := api.projectExtensionAccess(r, workspace, r.URL.Query().Get("profile_id")); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	user := GetUserFromContext(r.Context()).UserID
	profile := r.URL.Query().Get("profile_id")
	if profile == "" {
		profile = "workflow"
	}
	scope := browserSessionForWorkspace(user, workspace)
	if scope == "" {
		http.Error(w, "Open a project or workflow browser", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req) != nil {
			http.Error(w, "Invalid browser request", 400)
			return
		}
		switch req.Action {
		case "connect":
			// The owner explicitly chose the extension for this project. Reuse
			// the account browser without returning or copying its credential.
			if _, err := browserrelay.Default.PairForProfile(user, scope, workspace, profile); err != nil {
				http.Error(w, "Cannot start Chrome bridge", 503)
				return
			}
			browserrelay.Default.RequestProjectConnection(user, scope)
		case "pair", "copy", "reset":
			var token string
			var err error
			if req.Action == "reset" {
				token, err = browserrelay.Default.Reset(user, scope, workspace, profile)
			} else {
				token, err = browserrelay.Default.PairForProfile(user, scope, workspace, profile)
			}
			if err != nil {
				http.Error(w, "Cannot start Chrome bridge", 503)
				return
			}
			if req.Action == "pair" {
				browserrelay.Default.RequestProjectConnection(user, scope)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"token": token, "scope": scope})
			return
		case "disconnect":
			if err := browserrelay.Default.Disconnect(user, scope); err != nil {
				http.Error(w, "Cannot save browser selection", 503)
				return
			}
		default:
			http.Error(w, "Unknown action", 400)
			return
		}
	}
	json.NewEncoder(w).Encode(browserrelay.Default.Status(user, scope))
}
func (api *StreamingAPI) handleBrowserExtensionConnect(w http.ResponseWriter, r *http.Request) {
	browserrelay.Default.ServeExtensionAuthorizedWithNames(w, r, func(user, scope, workspace, profile string) error {
		claims := &UserClaims{UserID: user}
		if rec := directoryUserFor(user, "", ""); rec != nil {
			if rec.Disabled {
				return fmt.Errorf("account disabled")
			}
			claims.Username, claims.Email = rec.Username, rec.Email
		} else if IsMultiUserMode() {
			return fmt.Errorf("account unavailable")
		}
		checked := r.WithContext(context.WithValue(r.Context(), UserContextKey, claims))
		if _, err := api.projectExtensionAccess(checked, workspace, profile); err != nil {
			return err
		}
		if browserSessionForWorkspace(user, workspace) != scope {
			return fmt.Errorf("browser scope changed")
		}
		return nil
	}, func(user, workspace, profile string) string {
		ctx := context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user})
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		filename := "product.json"
		if profile == "workflow" {
			filename = "workflow.json"
		}
		name := path.Base(workspace)
		if raw, found, err := readFileFromWorkspace(ctx, workspace+"/"+filename); err == nil && found {
			var manifest struct {
				Title, Label string
				Identity     struct{ Name string }
			}
			if json.Unmarshal([]byte(raw), &manifest) == nil {
				if profile == "workflow" {
					name = firstNonEmptyTrimmed(manifest.Label, name)
				} else {
					name = firstNonEmptyTrimmed(manifest.Identity.Name, manifest.Title, manifest.Label, name)
				}
			}
		}
		name = strings.TrimSpace(strings.Map(func(c rune) rune {
			if unicode.IsControl(c) {
				return ' '
			}
			return c
		}, name))
		runes := []rune(name)
		if len(runes) > 120 {
			name = string(runes[:120])
		}
		return name
	})
}
func (api *StreamingAPI) handleBrowserExtensionDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="AgentWorks-Chrome-Bridge.zip"`)
	w.Write(browserrelay.ExtensionZip)
}
