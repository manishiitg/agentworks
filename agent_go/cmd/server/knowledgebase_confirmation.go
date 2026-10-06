package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

var knowledgeAccessApprovalMu sync.Mutex

// Only an interactive UI request can execute these frozen arguments.
// Approval is deliberately absent from the agent's MCP schema.
type knowledgeAccessProposal struct {
	ID           string         `json:"id"`
	Owner        string         `json:"owner"`
	Arguments    map[string]any `json:"arguments"`
	Expires      time.Time      `json:"expires_at"`
	EncryptedPAT string         `json:"encrypted_pat,omitempty"`
}

func knowledgeInteractiveAccess(claims *UserClaims) bool {
	return claims != nil && claims.AccessToken == nil && claims.ExecutionPrincipal == nil && claims.ExternalBuilderOperationID == "" && claims.Scope == "" && claims.BotRouteGrant == "" && claims.Provider != "bot_owner" && claims.Provider != "bot_route" && claims.Provider != slackDMProvider
}

func knowledgeProposeAccess(ctx context.Context, userID string, args map[string]any) (string, error) {
	if err := knowledgebase.ValidateToolArguments(knowledgebase.ToolAccess, args); err != nil {
		return "", err
	}
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != userID || !knowledgeInteractiveAccess(claims) || !knowledgebaseProductAllowed(claims) {
		return "", fmt.Errorf("interactive Brain access required")
	}
	root, err := knowledgeIntegrationRoot()
	if err != nil {
		return "", err
	}
	id := knowledgeHash(map[string]any{"owner": userID, "arguments": args})
	publicArgs := map[string]any{}
	for key, value := range args {
		if key != "pat" {
			publicArgs[key] = value
		}
	}
	proposal := knowledgeAccessProposal{ID: id, Owner: userID, Arguments: publicArgs, Expires: time.Now().UTC().Add(15 * time.Minute)}
	if pat, ok := args["pat"].(string); ok {
		proposal.EncryptedPAT, err = encryptSecretValueWithAAD(pat, []byte("knowledgebase:backup-proposal:"+id))
		if err != nil {
			return "", fmt.Errorf("could not protect backup credential")
		}
		publicArgs["pat_configured"] = pat != ""
	}
	b, err := json.Marshal(proposal)
	if err != nil {
		return "", err
	}
	knowledgeAccessApprovalMu.Lock()
	defer knowledgeAccessApprovalMu.Unlock()
	path := filepath.Join(root, "approval_"+id+".json")
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", readErr
	}
	var previous knowledgeAccessProposal
	if readErr != nil || json.Unmarshal(existing, &previous) != nil || !time.Now().Before(previous.Expires) {
		temp, err := os.CreateTemp(root, ".approval-*")
		if err != nil {
			return "", err
		}
		defer os.Remove(temp.Name())
		if _, err = temp.Write(b); err != nil {
			temp.Close()
			return "", err
		}
		if err = temp.Sync(); err != nil {
			temp.Close()
			return "", err
		}
		if err = temp.Close(); err != nil {
			return "", err
		}
		if err = os.Rename(temp.Name(), path); err != nil {
			return "", err
		}
	}
	result, err := json.Marshal(map[string]any{"status": "awaiting_user_confirmation", "proposal_id": id, "arguments": publicArgs})
	return string(result), err
}
func (api *StreamingAPI) handleKnowledgebaseAccessProposals(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !knowledgebaseProductAllowed(claims) || !knowledgeInteractiveAccess(claims) {
		externalError(w, 403, "FORBIDDEN", "Interactive Brain access required.")
		return
	}
	root, err := knowledgeIntegrationRoot()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		proposals := []knowledgeAccessProposal{}
		paths, err := filepath.Glob(filepath.Join(root, "approval_*.json"))
		if err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		for _, path := range paths {
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var p knowledgeAccessProposal
			if json.Unmarshal(b, &p) == nil && p.Owner == claims.UserID && time.Now().Before(p.Expires) {
				p.EncryptedPAT = ""
				proposals = append(proposals, p)
			}
		}
		knowledgebaseWriteJSON(w, map[string]any{"proposals": proposals})
		return
	}
	var request struct {
		ID      string  `json:"id"`
		Approve bool    `json:"approve"`
		PAT     *string `json:"pat,omitempty"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&request) != nil || len(request.ID) != 64 || strings.Trim(request.ID, "0123456789abcdef") != "" {
		externalError(w, 400, "INVALID_ARGUMENT", "Invalid proposal.")
		return
	}
	knowledgeAccessApprovalMu.Lock()
	defer knowledgeAccessApprovalMu.Unlock()
	path := filepath.Join(root, "approval_"+request.ID+".json")
	b, err := os.ReadFile(path)
	var proposal knowledgeAccessProposal
	if err != nil || json.Unmarshal(b, &proposal) != nil || proposal.Owner != claims.UserID || !time.Now().Before(proposal.Expires) {
		externalError(w, 404, "NOT_FOUND", "Proposal unavailable or expired.")
		return
	}
	if !request.Approve {
		if err := os.Remove(path); err != nil {
			knowledgebaseHTTPError(w, err)
			return
		}
		knowledgebaseWriteJSON(w, map[string]any{"status": "cancelled"})
		return
	}
	service, err := knowledgebaseService()
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = knowledgebaseSyncIdentities(r.Context(), service); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	p := knowledgebasePrincipal(r, claims)
	p.AccessOnly = true
	delete(proposal.Arguments, "pat_configured")
	if proposal.EncryptedPAT != "" {
		pat, err := decryptSecretValueWithAAD(proposal.EncryptedPAT, []byte("knowledgebase:backup-proposal:"+proposal.ID))
		if err != nil {
			externalError(w, 503, "STORAGE_UNAVAILABLE", "Could not decrypt backup credential.")
			return
		}
		proposal.Arguments["pat"] = pat
	}
	if request.PAT != nil {
		if proposal.Arguments["action"] != "configure_backup" {
			externalError(w, 400, "INVALID_ARGUMENT", "A PAT is only allowed for backup setup.")
			return
		}
		proposal.Arguments["pat"] = *request.PAT
	}
	result, err := knowledgebaseDispatch(r.Context(), service, p, claims.UserID, knowledgebase.ToolAccess, proposal.Arguments)
	if err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	if err = os.Remove(path); err != nil {
		knowledgebaseHTTPError(w, err)
		return
	}
	knowledgebaseWriteJSON(w, map[string]any{"result": result})
}
