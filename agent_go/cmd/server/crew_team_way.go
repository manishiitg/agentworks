package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// PLAT-746-1026, the Team's way of working: what the Team Crew uses, shared by any member with "Add to Team". The Team
// Crew's own settings are the store (selected apps and secrets); this file adds only the pieces they cannot say:
// which Vault vault holds the Team's keys, which personal copy each Team app came from, and the apps each person signs
// in to themselves.
//
// The login type decides sharing, never the person: an app with no login or an API key is copied into the Team's
// vault inside the Vault service (the member keeps theirs; the value never reaches the agent or a chat); a personal
// sign-in (OAuth) is never copied, it is listed as a requirement and each member signs in themselves. The Team vault
// is an ordinary Vault group owned by the Team's virtual user, with the Team's people as its members.

const teamWayFile = "team-way.json"

type teamWayNeed struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	AddedBy  string `json:"added_by,omitempty"`
}

type teamWayRecord struct {
	// Vault is the Team's vault (a Vault group the virtual user owns), once something has been shared.
	Vault string `json:"vault,omitempty"`
	// Apps maps a member's connection to the Team's copy, so adding the same app again changes nothing.
	Apps map[string]string `json:"apps,omitempty"`
	// AddedBy says who put an app (key "app:<Team connection id>") or a secret (key "secret:<name>") on the Team, so
	// teammates know who to ask.
	AddedBy map[string]string `json:"added_by,omitempty"`
	// Needs are the apps the Team uses that each person signs in to themselves.
	Needs []teamWayNeed `json:"needs,omitempty"`
}

type teamWayItem struct {
	Kind  string `json:"kind"`            // app, secret or needs_sign_in
	ID    string `json:"id,omitempty"`    // the Team's connection id; a secret's name
	Name  string `json:"name"`            // what people see
	Login string `json:"login,omitempty"` // shared or sign_in
	Note  string `json:"note,omitempty"`
	// AddedBy is the name of the teammate who added it.
	AddedBy string `json:"added_by,omitempty"`
	// Connected (sign-in apps only) is whether the person asking already has that app connected.
	Connected *bool `json:"connected,omitempty"`
}

type teamWayView struct {
	Items []teamWayItem `json:"items"`
	// OnTeam lists the personal apps (connection ids) and secrets (names) already shared, so the Vault panel can show
	// "On the Team" instead of the button.
	OnTeam []string `json:"on_team"`
	Notice string   `json:"notice,omitempty"`
}

func teamWayPath(project string) (string, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, teamWayFile), nil
}

func readTeamWay(project string) (teamWayRecord, error) {
	var rec teamWayRecord
	path, err := teamWayPath(project)
	if err != nil {
		return rec, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rec, nil
	}
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(raw, &rec)
}

func writeTeamWay(project string, rec teamWayRecord) error {
	path, err := teamWayPath(project)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, rec, 0600)
}

// teamWayShared reports whether a connection can be shared: one that needs no personal sign-in.
func teamWayShared(c vaultOAuthConnection) bool {
	return c.OAuthServer == "" && c.OAuthCredentialID == ""
}

// syncCrewTeamVault makes the Team vault's members exactly the Team's people (the virtual user stays its owner). It
// runs after any roster change, and only once something has been shared.
func syncCrewTeamVault(ctx context.Context, project string, team crewTeam) error {
	rec, err := readTeamWay(project)
	if err != nil || rec.Vault == "" {
		return err
	}
	want := map[string]bool{}
	for _, p := range team.People {
		if p.UserID != "" && !p.Invited && crewTeamAccountActive(p.UserID) {
			want[p.UserID] = true
		}
	}
	if team.OwnerID != "" && crewTeamAccountActive(team.OwnerID) {
		want[team.OwnerID] = true
	}
	if !team.On {
		want = map[string]bool{}
	}
	path := "/api/admin/groups/" + url.PathEscape(rec.Vault) + "/members"
	data, err := vaultServiceRequest(ctx, "", http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var have struct {
		Members []string `json:"members"`
	}
	if json.Unmarshal(data, &have) != nil {
		return errors.New("the Team's vault members could not be read")
	}
	owner := "team-" + project
	for member := range want {
		if slices.Contains(have.Members, member) {
			continue
		}
		// The Vault service learns a person the first time they ask for something; do that on their behalf.
		if _, err := vaultPersonRequest(ctx, member, http.MethodGet, "/api/vaults", nil); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"user_id": member})
		if _, err := vaultServiceRequest(ctx, "", http.MethodPost, path, body); err != nil {
			return err
		}
	}
	for _, member := range have.Members {
		if member != owner && !want[member] {
			if _, err := vaultServiceRequest(ctx, "", http.MethodDelete, path+"/"+url.PathEscape(member), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureTeamVault returns the Team's vault, making the virtual user, its Crew and the vault the first time.
func (api *StreamingAPI) ensureTeamVault(ctx context.Context, project string) (home, vault string, err error) {
	if home, err = api.ensureCrewTeamOnlineHome(ctx, project); err != nil {
		return "", "", err
	}
	rec, err := readTeamWay(project)
	if err != nil {
		return "", "", err
	}
	owner := "team-" + project
	if vault, err = ensurePersonalVault(ctx, owner); err != nil {
		return "", "", err
	}
	if rec.Vault != vault {
		rec.Vault = vault
		if err = writeTeamWay(project, rec); err != nil {
			return "", "", err
		}
	}
	team, err := readCrewTeam(project)
	if err != nil {
		return "", "", err
	}
	return home, vault, syncCrewTeamVault(ctx, project, team)
}

// handleCrewTeamWay is GET /crew-teams/{project}/way (what the Team uses) and POST (add or remove one item). Every
// member is equal: any current member may add or remove.
func (api *StreamingAPI) handleCrewTeamWay(w http.ResponseWriter, r *http.Request) {
	project := mux.Vars(r)["project"]
	claims := GetUserFromContext(r.Context())
	if claims == nil || !crewTeamClaims(claims, project, r.Method != http.MethodGet) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	team, err := readCrewTeam(project)
	if err != nil || !team.On || !team.member(claims.UserID) || ownerOfCrewProjectID(project) != team.OwnerID || !crewTeamAccountActive(team.OwnerID) {
		externalError(w, 403, "team_member_required", "Join this Crew's Team to see or change how it works.")
		return
	}
	if IsLocalMode() {
		externalError(w, 400, "team_on_server", "The Team's shared tools live on its server. Do this from your company's server.")
		return
	}
	if r.Method == http.MethodGet {
		view, err := api.teamWayView(r.Context(), project, claims.UserID)
		if err != nil {
			externalError(w, 500, "team_way_failed", "The Team's tools could not be read. Try again.")
			return
		}
		externalJSON(w, view)
		return
	}
	var in struct {
		Op      string `json:"op"`
		Kind    string `json:"kind"`
		ID      string `json:"id"`
		Replace bool   `json:"replace"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil {
		externalError(w, 400, "invalid_team_way", "Choose what to add or remove.")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		externalError(w, 400, "invalid_team_way", err.Error())
		return
	}
	mu := crewTeamRecordMutex(filepath.Join(dir, teamWayFile))
	mu.Lock()
	defer mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var notice string
	switch in.Op + ":" + in.Kind {
	case "add:app":
		notice, err = api.teamWayAddApp(ctx, claims.UserID, project, in.ID)
	case "add:secret":
		notice, err = api.teamWayAddSecret(ctx, claims.UserID, project, in.ID, in.Replace)
	case "remove:app":
		notice, err = api.teamWayRemoveApp(ctx, claims.UserID, project, in.ID)
	case "remove:secret":
		notice, err = api.teamWayRemoveSecret(ctx, claims.UserID, project, in.ID)
	case "remove:needs_sign_in":
		notice, err = teamWayRemoveNeed(project, in.ID)
	default:
		err = errors.New("Choose an app or a secret to add to the Team, or something to remove.")
	}
	if err != nil {
		log.Printf("[TEAM_WAY] project=%s actor=%s op=%s kind=%s item=%q result=refused", project, claims.UserID, in.Op, in.Kind, in.ID)
		externalError(w, 400, "team_way_refused", err.Error())
		return
	}
	log.Printf("[TEAM_WAY] project=%s actor=%s op=%s kind=%s item=%q result=ok", project, claims.UserID, in.Op, in.Kind, in.ID)
	view, err := api.teamWayView(ctx, project, claims.UserID)
	if err != nil {
		externalError(w, 500, "team_way_failed", "It was saved, but the Team's tools could not be read back. Try again.")
		return
	}
	view.Notice = notice
	publishCrewPanelChanged(project)
	externalJSON(w, view)
}

func (api *StreamingAPI) teamWayView(ctx context.Context, project, person string) (teamWayView, error) {
	view := teamWayView{Items: []teamWayItem{}, OnTeam: []string{}}
	rec, err := readTeamWay(project)
	if err != nil {
		return view, err
	}
	for source := range rec.Apps {
		view.OnTeam = append(view.OnTeam, source)
	}
	for key := range rec.AddedBy {
		if name, ok := strings.CutPrefix(key, "secret:"); ok {
			view.OnTeam = append(view.OnTeam, name)
		}
	}
	slices.Sort(view.OnTeam)
	if rec.Vault != "" {
		owner := "team-" + project
		data, err := vaultPersonRequest(ctx, owner, http.MethodGet, "/api/vaults/"+url.PathEscape(rec.Vault), nil)
		if err != nil {
			return view, err
		}
		var vault struct {
			Connections []struct {
				ID, Label, Provider string
			} `json:"connections"`
			SecretNames []string `json:"secret_names"`
		}
		if json.Unmarshal(data, &vault) != nil {
			return view, errors.New("the Team's vault could not be read")
		}
		for _, c := range vault.Connections {
			view.Items = append(view.Items, teamWayItem{Kind: "app", ID: c.ID, Name: c.Label, Login: "shared", AddedBy: teamWayAdder(rec.AddedBy["app:"+c.ID])})
		}
		for _, name := range vault.SecretNames {
			short := vaultSecretShortName(name)
			view.Items = append(view.Items, teamWayItem{Kind: "secret", ID: short, Name: short, Login: "shared", AddedBy: teamWayAdder(rec.AddedBy["secret:"+short])})
		}
	}
	if len(rec.Needs) > 0 {
		mine, _ := personalVaults(ctx, person) // an unreadable vault only means "not connected" on the screen
		for _, need := range rec.Needs {
			connected := teamWayConnected(mine, need.Provider)
			view.Items = append(view.Items, teamWayItem{Kind: "needs_sign_in", ID: need.Provider, Name: need.Label, Login: "sign_in", Note: "Team uses " + need.Label + ": each person signs in themselves.", AddedBy: teamWayAdder(need.AddedBy), Connected: &connected})
		}
	}
	return view, nil
}

func teamWayAdder(user string) string {
	if user == "" {
		return ""
	}
	return crewOwnerDisplayName(user)
}

// teamWayConnected reports whether the person already has a working connection to the app a Team need names.
func teamWayConnected(mine personalVaultList, provider string) bool {
	want := strings.TrimPrefix(provider, "custom:")
	for _, v := range mine.Vaults {
		for _, c := range v.Connections {
			if strings.EqualFold(c.Provider, want) && c.Status == "active" {
				return true
			}
		}
	}
	return false
}

// teamWayProviderName is the app's own name (for example "GitHub"), never the member's connection label: the label can be
// personal, and the Team need is shown to everyone on the Team.
func teamWayProviderName(c vaultOAuthConnection) string {
	name := strings.TrimPrefix(c.OAuthServer, "custom:")
	if name == "" {
		return "An app"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// teamWayAddApp shares one of the person's own connections with the Team, by its login type.
func (api *StreamingAPI) teamWayAddApp(ctx context.Context, person, project, connection string) (string, error) {
	c, err := vaultSavedConnection(ctx, connection)
	if err != nil || c.VaultID == "" || !vaultOwnerOf(ctx, person, c.VaultID) {
		return "", errors.New("Choose one of your own apps from your Vault.")
	}
	if !teamWayShared(c) {
		rec, err := readTeamWay(project)
		if err != nil {
			return "", err
		}
		if !slices.ContainsFunc(rec.Needs, func(n teamWayNeed) bool { return n.Provider == c.OAuthServer }) {
			rec.Needs = append(rec.Needs, teamWayNeed{Provider: c.OAuthServer, Label: teamWayProviderName(c), AddedBy: person})
			if err := writeTeamWay(project, rec); err != nil {
				return "", err
			}
		}
		return "Team uses " + teamWayProviderName(c) + ": each person signs in themselves. Nothing of yours was shared.", nil
	}
	home, vault, err := api.ensureTeamVault(ctx, project)
	if err != nil {
		return "", err
	}
	rec, err := readTeamWay(project)
	if err != nil {
		return "", err
	}
	if _, ok := rec.Apps[connection]; ok {
		return c.Label + " is already on the Team.", nil
	}
	body, _ := json.Marshal(map[string]string{"to_vault": vault, "to_owner": "team-" + project})
	data, err := vaultPersonRequest(ctx, person, http.MethodPost, "/api/vaults/"+url.PathEscape(c.VaultID)+"/connectors/"+url.PathEscape(connection)+"/copy", body)
	if err != nil {
		return "", err
	}
	var copied struct{ ID string }
	if json.Unmarshal(data, &copied) != nil || copied.ID == "" {
		return "", errors.New("The Vault returned no connection for the Team.")
	}
	if rec.Apps == nil {
		rec.Apps = map[string]string{}
	}
	rec.Apps[connection] = copied.ID
	if rec.AddedBy == nil {
		rec.AddedBy = map[string]string{}
	}
	rec.AddedBy["app:"+copied.ID] = person
	if err := writeTeamWay(project, rec); err != nil {
		return "", err
	}
	if err := api.selectVaultServer(ctx, person, crewProjectRootByID(home), copied.ID, true); err != nil {
		return "", err
	}
	return c.Label + " is on the Team now. You keep your own copy.", nil
}

func (api *StreamingAPI) teamWayAddSecret(ctx context.Context, person, project, name string, replace bool) (string, error) {
	mine, err := ensurePersonalVault(ctx, person)
	if err != nil {
		return "", err
	}
	source, err := vaultSecretName(mine, name)
	if err != nil {
		return "", err
	}
	managedGlobalsMu.RLock()
	value, ok := managedGlobals[source]
	managedGlobalsMu.RUnlock()
	if !ok || !vaultOwnerOf(ctx, person, mine) {
		return "", errors.New("You have no secret with that name in your Vault.")
	}
	home, vault, err := api.ensureTeamVault(ctx, project)
	if err != nil {
		return "", err
	}
	owner := "team-" + project
	full, err := vaultSecretName(vault, name)
	if err != nil {
		return "", err
	}
	managedGlobalsMu.RLock()
	_, existed := managedGlobals[full]
	managedGlobalsMu.RUnlock()
	if existed && !replace {
		return "", errors.New(name + " is already on the Team. Ask to replace it to change the stored value.")
	}
	if err := api.storeManagedGlobalSecret(ctx, full, value, false); err != nil {
		return "", err
	}
	if _, err := vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(vault)+"/secrets", vaultPromoteJSON(map[string]string{"Name": full})); err != nil {
		if !existed {
			_ = api.removeManagedGlobalSecretValue(ctx, full)
		}
		return "", err
	}
	if err := updateProductSelectedGlobalSecrets(ctx, crewProfileID, crewProjectRootByID(home), func(current []string) []string { return settingsWith(current, full) }); err != nil {
		return "", err
	}
	rec, err := readTeamWay(project)
	if err != nil {
		return "", err
	}
	if rec.AddedBy == nil {
		rec.AddedBy = map[string]string{}
	}
	rec.AddedBy["secret:"+name] = person
	if err := writeTeamWay(project, rec); err != nil {
		return "", err
	}
	return name + " is on the Team now. You keep your own copy; its value is never shown.", nil
}

func (api *StreamingAPI) teamWayRemoveApp(ctx context.Context, person, project, id string) (string, error) {
	rec, err := readTeamWay(project)
	if err != nil || rec.Vault == "" {
		return "", errors.New("The Team does not use that app.")
	}
	home := ""
	if team, e := readCrewTeam(project); e == nil {
		home = team.OnlineHome
	}
	found := false
	for source, team := range rec.Apps {
		if team == id {
			delete(rec.Apps, source)
			delete(rec.AddedBy, "app:"+id)
			found = true
		}
	}
	if !found {
		return "", errors.New("The Team does not use that app.")
	}
	owner := "team-" + project
	if home != "" {
		if err := api.selectVaultServer(ctx, person, crewProjectRootByID(home), id, false); err != nil {
			return "", err
		}
	}
	if _, err := vaultPersonRequest(ctx, owner, http.MethodDelete, "/api/vaults/"+url.PathEscape(rec.Vault)+"/connectors/"+url.PathEscape(id), nil); err != nil {
		return "", err
	}
	removeVaultCredentialFiles(id)
	return "Removed from the Team. Everyone's own copy stays.", writeTeamWay(project, rec)
}

func (api *StreamingAPI) teamWayRemoveSecret(ctx context.Context, person, project, name string) (string, error) {
	rec, err := readTeamWay(project)
	if err != nil || rec.Vault == "" {
		return "", errors.New("The Team does not use that secret.")
	}
	full, err := vaultSecretName(rec.Vault, name)
	if err != nil {
		return "", err
	}
	if team, e := readCrewTeam(project); e == nil && team.OnlineHome != "" {
		if err := updateProductSelectedGlobalSecrets(ctx, crewProfileID, crewProjectRootByID(team.OnlineHome), func(current []string) []string { return settingsWithout(current, []string{full}) }); err != nil {
			return "", err
		}
	}
	if err := api.removeVaultSecret(ctx, "team-"+project, rec.Vault, name); err != nil {
		return "", err
	}
	delete(rec.AddedBy, "secret:"+name)
	return "Removed from the Team. Everyone's own copy stays.", writeTeamWay(project, rec)
}

func teamWayRemoveNeed(project, provider string) (string, error) {
	rec, err := readTeamWay(project)
	if err != nil {
		return "", err
	}
	before := len(rec.Needs)
	rec.Needs = slices.DeleteFunc(rec.Needs, func(n teamWayNeed) bool { return n.Provider == provider })
	if len(rec.Needs) == before {
		return "", errors.New("The Team does not list that app.")
	}
	return "Removed from the Team's list.", writeTeamWay(project, rec)
}

// crewTeamWayChat is the chat and MCP entry: the same handler as the screen.
func (api *StreamingAPI) crewTeamWayChat(ctx context.Context, caller *UserClaims, project, action string, args map[string]any) (string, error) {
	method, body := http.MethodGet, []byte(nil)
	if action == "way_add" || action == "way_remove" {
		method = http.MethodPost
		in := map[string]any{"op": strings.TrimPrefix(action, "way_"), "kind": externalArg(args, "item"), "id": externalArg(args, "id")}
		if args["replace"] == true {
			in["replace"] = true
		}
		body, _ = json.Marshal(in)
	}
	r := httptest.NewRequest(method, "/api/crew-teams/"+url.PathEscape(project)+"/way", bytes.NewReader(body)).WithContext(context.WithValue(ctx, UserContextKey, caller))
	w := httptest.NewRecorder()
	api.handleCrewTeamWay(w, mux.SetURLVars(r, map[string]string{"project": project, "op": "way"}))
	if w.Code >= 300 {
		return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
	}
	return w.Body.String(), nil
}

// crewTeamWayRemote is the same call from a Mac: the Team lives on the server the person added.
func (api *StreamingAPI) crewTeamWayRemote(ctx context.Context, person string, c desktopConnection, project, action string, args map[string]any) (string, error) {
	route, method, payload := "/api/crew-teams/"+url.PathEscape(project)+"/way", http.MethodGet, any(nil)
	if action == "way_add" || action == "way_remove" {
		method = http.MethodPost
		in := map[string]any{"op": strings.TrimPrefix(action, "way_"), "kind": externalArg(args, "item"), "id": externalArg(args, "id")}
		if args["replace"] == true {
			in["replace"] = true
		}
		payload = in
	}
	var out json.RawMessage
	if err := crewTeamRemoteJSON(ctx, person, c, method, route, payload, &out); err != nil {
		return "", err
	}
	return string(out), nil
}
