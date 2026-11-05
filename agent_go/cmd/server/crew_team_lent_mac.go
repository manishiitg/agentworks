package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// What only the owner's Mac has (owner, 5 Nov, approved mockup "Team tab: what teammates' agents use from you"): in
// the owner's Mac app, the Team card adds the Vault secrets and apps this Crew uses on the Mac that its server does not
// have yet, so teammates' agents can't use them: a secret can be copied to the server, a signed-in app moved there
// (vault_move.go), and an app that runs on the Mac stays (teammates' agents ask the owner's agent). The server's own
// list (crewTeamLent) is what is ready for the team; this Mac decides only what it holds.

type crewTeamMacOnlyItem struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Action string `json:"action"` // "copy", "move" or ""
	Note   string `json:"note,omitempty"`
}

const crewTeamMacLocalNote = "Runs on your Mac. Teammates' agents can ask your agent to use it."

// crewTeamServerLent is the Team card's list as the Crew's server writes it, read through the owner's saved sign-in.
func crewTeamServerLent(ctx context.Context, owner string, c desktopConnection, peer string) (*crewTeamLent, error) {
	var view struct {
		Team struct {
			Lent *crewTeamLent `json:"lent"`
		} `json:"team"`
	}
	if err := crewTeamRemoteJSON(ctx, owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(peer)+"/tab", nil, &view); err != nil {
		return nil, err
	}
	return view.Team.Lent, nil
}

// crewTeamMacOnly lists what this Crew uses on the Mac that the server's list lacks.
func (api *StreamingAPI) crewTeamMacOnly(ctx context.Context, owner string, roots crewroot.Roots, c desktopConnection, binding *crewroot.SyncBinding) ([]crewTeamMacOnlyItem, error) {
	onServer := map[string]bool{}
	lent, err := crewTeamServerLent(ctx, owner, c, binding.PeerProjectID)
	if err != nil {
		return nil, err
	}
	if lent != nil {
		for _, item := range lent.Items {
			onServer[item.Kind+"\x00"+strings.ToLower(item.Name)] = true
		}
	}
	out := []crewTeamMacOnlyItem{}
	// Secrets the Crew uses from this Mac's Vault.
	if selection, err := projectSecretSelection(ctx, roots.Virtual); err == nil {
		values, _ := ownVaultSecretValues(ctx, owner, selection)
		for _, full := range selection {
			name := vaultSecretShortName(full)
			if onServer["secret\x00"+strings.ToLower(name)] {
				continue
			}
			item := crewTeamMacOnlyItem{Key: "secret:" + name, Name: name, Kind: "secret", Action: "copy"}
			if values[name] == "" {
				item.Action, item.Note = "", "Add it in your Vault on the server."
			}
			out = append(out, item)
		}
	}
	selected, chosenOnly, _ := projectAppSelection(ctx, roots.Virtual)
	// Apps in this Mac's Vault the Crew uses.
	if servers, err := vaultServersFor(ctx, owner); err == nil {
		for _, server := range servers {
			label := firstNonEmptyTrimmed(server.Label, server.ID)
			if !projectVaultAppInUse(selected, chosenOnly, server.ID) || onServer["app\x00"+strings.ToLower(label)] {
				continue
			}
			item := crewTeamMacOnlyItem{Key: "app:" + server.ID, Name: label, Kind: "app, signed in", Action: "move"}
			if conn, err := vaultSavedConnection(ctx, server.ID); err != nil || conn.OAuthServer == "" || conn.OAuthCredentialID != server.ID {
				item.Kind, item.Action, item.Note = "app", "", "Add it in your Vault on the server."
			}
			out = append(out, item)
		}
	}
	// Apps that run on this Mac (its own MCP programs): they can't move.
	for _, name := range selected {
		if governedVaultName(name) || strings.TrimSpace(name) == "" {
			continue
		}
		out = append(out, crewTeamMacOnlyItem{Key: "local:" + name, Name: name, Kind: "app on your Mac", Note: crewTeamMacLocalNote})
	}
	return out, nil
}

// desktopCrewTeamLent answers the owner's Mac app: "lent" lists what only this Mac has; "lent_copy" copies one secret
// to the owner's Vault on the server (and the Crew there uses it); "lent_move" moves one signed-in app there.
func (api *StreamingAPI) desktopCrewTeamLent(w http.ResponseWriter, r *http.Request, claims *UserClaims, roots crewroot.Roots, binding *crewroot.SyncBinding, c desktopConnection, action, key string) {
	if binding == nil || binding.PeerProjectID == "" || binding.ConnectionID != c.ID || roots.Owner != claims.UserID {
		externalError(w, 409, "team_unavailable", "Only the Crew's owner can share what their Mac has, from the Crew's own server.")
		return
	}
	owner := claims.UserID
	switch action {
	case "lent":
		items, err := api.crewTeamMacOnly(r.Context(), owner, roots, c, binding)
		if err != nil {
			externalError(w, 502, "team_unavailable", crewTeamPlainSyncError(err))
			return
		}
		externalJSON(w, map[string]any{"items": items})
	case "lent_copy":
		name, ok := strings.CutPrefix(key, "secret:")
		selection, _ := projectSecretSelection(r.Context(), roots.Virtual)
		values, _ := ownVaultSecretValues(r.Context(), owner, selection)
		if !ok || values[name] == "" {
			externalError(w, 400, "invalid_request", "Choose one of this Crew's secrets on your Mac.")
			return
		}
		body, _ := json.Marshal(map[string]string{"name": name, "value": values[name], "crew": binding.PeerProjectID})
		resp, err := crewTeamRemoteDo(r.Context(), owner, c, http.MethodPost, "/api/vault/me/secrets", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("[CREW_TEAM_LENT] project=%s secret=%s result=copy_failed err_type=%T", roots.ID, name, err)
			externalJSON(w, map[string]any{"line": err.Error()})
			return
		}
		resp.Body.Close()
		log.Printf("[CREW_TEAM_LENT] project=%s secret=%s result=copied", roots.ID, name)
		externalJSON(w, map[string]any{"done": "Copied"})
	case "lent_move":
		id, ok := strings.CutPrefix(key, "app:")
		if !ok {
			externalError(w, 400, "invalid_request", "Choose one of this Crew's apps on your Mac.")
			return
		}
		status, out := api.moveVaultAppToServer(r.Context(), owner, c, id)
		if moved, _ := out["moved"].(bool); moved {
			externalJSON(w, map[string]any{"done": "Moved"})
			return
		}
		reply := map[string]any{"line": out["error"]}
		if link, _ := out["open_vault_url"].(string); link != "" {
			reply["vault_url"] = link
		}
		log.Printf("[CREW_TEAM_LENT] project=%s app=%s result=move_refused status=%d", roots.ID, id, status)
		externalJSON(w, reply)
	}
}
