package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Company keys (#4148, owner 26 Nov): keys live in four places, each with one job: personal (only you), Team (the
// Team's people), project (everyone in an online project) and company (everyone on the server). A company key is a
// platform-managed key with a plain name (not a VLT_ vault name). It reaches every active account on the server, with
// no per-group lists; only Vault administrators add, replace or remove one. An online project reads company keys
// directly beside its own project vault, and its own key wins on a name clash.
//
// One-off move: before the switch, plain keys were granted to groups. The switch waits until an administrator has
// chosen which of those become company keys (the rest move into that administrator's own vault), so a key that was
// granted to one group never reaches everyone by surprise. A server with no plain keys switches at once. Delete the
// move and its mark once every server has moved.

const companyKeysMovedMark = "company-keys-moved"

func companyKeysMarkPath() string {
	state, err := workflowCLIStateRoot()
	if err != nil || strings.TrimSpace(state) == "" {
		return ""
	}
	return filepath.Join(state, companyKeysMovedMark)
}

func isCompanyKeyName(name string) bool { return name != "" && !isVaultSecretName(name) }

// companyKeyNames are the plain-named managed keys, sorted.
func companyKeyNames() []string {
	out := []string{}
	for _, s := range getGlobalSecrets() {
		if isCompanyKeyName(s.Name) {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// companyKeysOn says company keys reach everyone: after the move, or on a server that had no plain keys to move.
func companyKeysOn() bool {
	mark := companyKeysMarkPath()
	if mark == "" {
		return false
	}
	if _, err := os.Stat(mark); err == nil {
		return true
	}
	if managedGlobalsLoaded.Load() && len(companyKeyNames()) == 0 {
		markCompanyKeysMoved("nothing to move")
		return true
	}
	return false
}

func markCompanyKeysMoved(note string) {
	if mark := companyKeysMarkPath(); mark != "" {
		if os.MkdirAll(filepath.Dir(mark), 0o700) == nil {
			_ = os.WriteFile(mark, []byte(time.Now().UTC().Format(time.RFC3339)+" "+note+"\n"), 0o600)
		}
	}
}

// companyKeyReader is anyone with an active account on this server.
func companyKeyReader(person string) bool {
	return strings.TrimSpace(person) != "" && !userAccessForClaims(&UserClaims{UserID: person}).Disabled
}

// withCompanyKeys adds every company key to a person's usable rows once company keys are on.
func withCompanyKeys(person string, rows []globalSecretEntry, all []globalSecretEntry) []globalSecretEntry {
	if !companyKeyReader(person) || !companyKeysOn() {
		return rows
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.Name] = true
	}
	for _, s := range all {
		if isCompanyKeyName(s.Name) && !have[s.Name] {
			rows = append(rows, s)
		}
	}
	return rows
}

// withProjectCompanyKeys is an online project's key selection: its project vault's keys plus every company key whose
// name the project's own vault does not already use (the project's key wins).
func withProjectCompanyKeys(vaultNames []string) []string {
	if !companyKeysOn() {
		return vaultNames
	}
	own := map[string]bool{}
	for _, n := range vaultNames {
		own[vaultSecretShortName(n)] = true
	}
	out := append([]string{}, vaultNames...)
	for _, n := range companyKeyNames() {
		if !own[n] {
			out = append(out, n)
		}
	}
	return out
}

var errCompanyKeysGroups = errors.New("Company keys reach everyone on this server; there are no per-group keys. Keep a key for one person in their own vault, or for one online project in its own keys")

type companyKeyMoveRow struct {
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

// companyKeyGroups is which groups each plain key is granted to today (for the move screen), read from Vault.
func companyKeyGroups(ctx context.Context, actor string) (map[string][]string, error) {
	data, err := vaultSecretAdminCall(ctx, actor, http.MethodGet, "/api/admin/groups", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Groups []struct{ ID, Name string } `json:"groups"`
	}
	if json.Unmarshal(data, &list) != nil {
		return nil, errors.New("Vault groups could not be read")
	}
	out := map[string][]string{}
	for _, g := range list.Groups {
		raw, err := vaultSecretAdminCall(ctx, actor, http.MethodGet, "/api/admin/groups/"+url.PathEscape(g.ID)+"/secrets", nil)
		if err != nil {
			return nil, err
		}
		var secrets struct {
			Secrets []struct{ Name string } `json:"secrets"`
		}
		if json.Unmarshal(raw, &secrets) != nil {
			return nil, errors.New("Vault group keys could not be read")
		}
		name := g.Name
		if name == "" {
			name = g.ID
		}
		for _, s := range secrets.Secrets {
			if isCompanyKeyName(s.Name) {
				out[s.Name] = append(out[s.Name], name)
			}
		}
	}
	return out, nil
}

type companyKeysView struct {
	On     bool                `json:"on"`
	Admin  bool                `json:"admin"`
	Keys   []string            `json:"keys"`
	ToMove []companyKeyMoveRow `json:"to_move,omitempty"`
	Note   string              `json:"note,omitempty"`
}

func (api *StreamingAPI) companyKeysFor(ctx context.Context, person string) (companyKeysView, error) {
	view := companyKeysView{On: companyKeysOn(), Admin: canManageGlobalSecrets(person), Keys: []string{}}
	switch {
	case view.On || view.Admin:
		view.Keys = companyKeyNames()
	default:
		rows, err := permittedGlobalSecrets(ctx, person)
		if err != nil {
			return view, err
		}
		for _, r := range rows {
			if isCompanyKeyName(r.Name) {
				view.Keys = append(view.Keys, r.Name)
			}
		}
		sort.Strings(view.Keys)
	}
	if !view.On && view.Admin {
		groups, err := companyKeyGroups(ctx, person)
		if err != nil {
			view.Note = "Group lists could not be read from Vault; try again."
		}
		for _, name := range view.Keys {
			view.ToMove = append(view.ToMove, companyKeyMoveRow{Name: name, Groups: groups[name]})
		}
	}
	return view, nil
}

// moveCompanyKeys makes today's plain keys company keys, except leaveOut, which move into the administrator's own vault
// (copied first, then removed). A backup of the names and group lists is written before anything changes.
func (api *StreamingAPI) moveCompanyKeys(ctx context.Context, actor string, leaveOut []string) (int, int, error) {
	if !canManageGlobalSecrets(actor) {
		return 0, 0, errGlobalAdmin
	}
	if companyKeysOn() {
		return 0, 0, errors.New("Company keys are already on for this server")
	}
	names := companyKeyNames()
	groups, err := companyKeyGroups(ctx, actor)
	if err != nil {
		return 0, 0, err
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return 0, 0, err
	}
	backup := filepath.Join(state, "migration-backups", "company-keys-"+time.Now().UTC().Format("20060102T150405Z")+".json")
	raw, _ := json.MarshalIndent(map[string]any{"actor": actor, "keys": names, "groups": groups, "leave_out": leaveOut}, "", "  ")
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return 0, 0, err
	}
	left := 0
	if len(leaveOut) > 0 {
		vaultID, err := ensurePersonalVault(ctx, actor)
		if err != nil {
			return 0, 0, err
		}
		values := map[string]string{}
		for _, s := range getGlobalSecrets() {
			values[s.Name] = s.Value
		}
		for _, name := range leaveOut {
			if !slices.Contains(names, name) || values[name] == "" {
				continue
			}
			if err := api.setVaultSecret(ctx, actor, vaultID, name, values[name], true); err != nil {
				return 0, left, err
			}
			if err := api.deleteManagedGlobalSecret(ctx, actor, name); err != nil {
				return 0, left, err
			}
			left++
		}
	}
	markCompanyKeysMoved("moved by " + actor)
	log.Printf("[COMPANY_KEYS] moved by=%s company=%d left_out=%d backup=%s", actor, len(names)-left, left, backup)
	return len(names) - left, left, nil
}

// handleCompanyKeys: GET lists company keys (everyone; administrators also see what the move would change); POST
// /move runs the one-off move (administrators). Adding, replacing and removing use /api/secrets/global.
func (api *StreamingAPI) handleCompanyKeys(w http.ResponseWriter, r *http.Request) {
	person := GetUserIDFromContext(r.Context())
	if r.Method == http.MethodPost {
		var in struct {
			LeaveOut []string `json:"leave_out"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
			writeUsersError(w, http.StatusBadRequest, "Send the keys to leave out.")
			return
		}
		moved, left, err := api.moveCompanyKeys(r.Context(), person, in.LeaveOut)
		if errors.Is(err, errGlobalAdmin) {
			writeUsersError(w, http.StatusForbidden, "Only a Vault admin can do this move. Ask an admin.")
			return
		}
		if err != nil {
			writeUsersError(w, http.StatusConflict, err.Error())
			return
		}
		writeUsersJSON(w, http.StatusOK, map[string]int{"moved": moved, "left_out": left})
		return
	}
	view, err := api.companyKeysFor(r.Context(), person)
	if err != nil {
		writeUsersError(w, http.StatusServiceUnavailable, "Company keys could not be loaded: Vault isn't answering.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeUsersJSON(w, http.StatusOK, view)
}
