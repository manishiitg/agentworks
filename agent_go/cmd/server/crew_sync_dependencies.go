package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// This is a separate authenticated settings request, never a file/native sync
// packet or a saved pending transfer. Only this request can contain values.
type crewDependencyCheck struct {
	Version [32]byte
	At      time.Time
}

var crewDependencyChecks sync.Map

type crewDependencyRequest struct {
	PairID            string                     `json:"pair_id"`
	Names             []string                   `json:"project_secret_names"`
	Values            map[string]string          `json:"project_secret_values,omitempty"`
	Connections       []crewDependencyConnection `json:"connections,omitempty"`
	SourceUnavailable bool                       `json:"source_unavailable,omitempty"`
}
type crewDependencyConnection struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Provider     string `json:"provider,omitempty"`
	AccountEmail string `json:"account_email,omitempty"`
}
type crewDependencyStatus struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type crewConfiguredDependency struct {
	Kind      string
	ID        string
	SourceKey string
}
type crewDependencyReport struct {
	configured []crewConfiguredDependency
	Ready      bool                   `json:"ready"`
	Message    string                 `json:"message"`
	CheckedAt  string                 `json:"checked_at"`
	Items      []crewDependencyStatus `json:"items"`
}

func crewDependencyPath(id string) (string, error) {
	p, e := crewSyncManifestPath(id)
	return p + ".dependencies", e
}
func readCrewDependencies(id string) *crewDependencyReport {
	p, e := crewDependencyPath(id)
	if e != nil {
		return nil
	}
	raw, e := os.ReadFile(p)
	if e != nil {
		return nil
	}
	var state struct {
		Report     crewDependencyReport       `json:"report"`
		Configured []crewConfiguredDependency `json:"configured"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil
	}
	state.Report.configured = state.Configured
	return &state.Report
}
func saveCrewDependencies(id string, report *crewDependencyReport) error {
	p, e := crewDependencyPath(id)
	if e != nil {
		return e
	}
	return fsutil.WriteJSONAtomic(p, struct {
		Report     *crewDependencyReport      `json:"report"`
		Configured []crewConfiguredDependency `json:"configured,omitempty"`
	}{report, report.configured}, 0600)
}
func crewDependencyKey(item crewDependencyConnection) string {
	return strings.Join([]string{item.Kind, item.Name, item.Provider, item.AccountEmail}, "\x00")
}
func (out *crewDependencyReport) remember(kind, id, key string) {
	for _, item := range out.configured {
		if item.Kind == kind && item.ID == id && item.SourceKey == key {
			return
		}
	}
	out.configured = append(out.configured, crewConfiguredDependency{kind, id, key})
}

func (out *crewDependencyReport) add(kind, name, state, message string) {
	out.Items = append(out.Items, crewDependencyStatus{kind, name, state, message})
	if state == "missing" || state == "sign_in" {
		out.Ready = false
	}
}

// Only an exact identity can be selected automatically. A unique provider-only
// candidate is a suggestion; the person must choose it in the server Crew.
func matchCrewConnection(want crewDependencyConnection, inventory projectVaultInventory) (*projectVaultItem, bool) {
	var exact, provider []projectVaultItem
	seen := map[string]bool{}
	for _, group := range inventory.Vaults {
		for _, item := range group.Items {
			if item.Kind != want.Kind || (!item.CanSelect && !item.Selected) || seen[item.Kind+":"+item.ID] {
				continue
			}
			seen[item.Kind+":"+item.ID] = true
			if want.AccountEmail != "" && !strings.EqualFold(want.AccountEmail, item.AccountEmail) {
				continue
			}
			if want.Provider != "" && item.Provider != want.Provider {
				continue
			}
			if item.Name == want.Name {
				exact = append(exact, item)
			}
			if want.Provider != "" {
				provider = append(provider, item)
			}
		}
	}
	if len(exact) == 1 {
		return &exact[0], true
	}
	if len(exact) == 0 && len(provider) == 1 {
		return &provider[0], false
	}
	return nil, false
}

func (api *StreamingAPI) handleCrewDependencies(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	roots, e := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if e != nil {
		externalError(w, 403, "dependencies_unavailable", "Use the paired Crew's owner connection.")
		return
	}
	var input crewDependencyRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || len(input.Names) > maxSecretImportNames || len(input.Connections) > 200 {
		externalError(w, 400, "invalid_request", "Send this Crew's selected connections and secret names.")
		return
	}
	b, e := crewroot.ReadSync(roots.ID)
	if e != nil || b == nil || b.Side != "server" || input.PairID == "" || input.PairID != b.PairID {
		externalError(w, 409, "wrong_pair", "Use this Crew's paired computer.")
		return
	}
	for name := range input.Values {
		if !slices.Contains(input.Names, name) {
			externalError(w, 400, "invalid_request", "Only selected Crew secrets can be prepared.")
			return
		}
	}
	out := &crewDependencyReport{Ready: true, CheckedAt: time.Now().UTC().Format(time.RFC3339), Items: []crewDependencyStatus{}}
	// Only undo selections this pairing added; keep existing server choices.
	wanted := map[string]bool{}
	for _, name := range input.Names {
		wanted["project_secret\x00"+name] = true
	}
	for _, item := range input.Connections {
		wanted[crewDependencyKey(item)] = true
	}
	if old := readCrewDependencies(roots.ID); old != nil {
		for _, item := range old.configured {
			if wanted[item.SourceKey] || input.SourceUnavailable {
				out.configured = append(out.configured, item)
				continue
			}
			if _, e := api.selectProjectVaultItem(r.Context(), claims.UserID, roots.Virtual, item.Kind, item.ID, false); e != nil {
				externalError(w, 502, "dependencies_unavailable", "Open the server Crew's Vault to update its selected connections, then retry sync.")
				return
			}
		}
	}
	if input.SourceUnavailable {
		out.add("vault", "Vault", "missing", "Some connections could not be checked on your Mac. Open its Crew Vault and retry sync.")
	}
	results := map[string]secretValueResult{}
	if len(input.Values) > 0 {
		rows, status, _ := api.importProjectSecrets(r, "crew", roots.ID, input.Values, false, false)
		if status != 200 {
			externalError(w, status, "secrets_unavailable", "Crew secrets could not be prepared. Check this server's Vault and retry sync.")
			return
		}
		for _, row := range rows {
			results[row.Name] = row
		}
	}
	inventory, e := api.projectVaultInventory(r.Context(), claims.UserID, roots.Virtual)
	if e != nil {
		externalError(w, 502, "dependencies_unavailable", "Crew connections could not be checked. Open Vault on this server and retry sync.")
		return
	}
	if len(inventory.Notices) > 0 {
		out.add("vault", "Server Vault", "missing", "Some server Vault connections could not be checked. Open Vault on this server and retry sync.")
	}
	var selected []any
	for _, name := range input.Names {
		available := false
		for _, g := range inventory.Vaults {
			for _, item := range g.Items {
				if item.Kind == "project_secret" && item.ID == name {
					available = true
					if !item.Selected || results[name].Status == "added" {
						out.remember("project_secret", name, "project_secret\x00"+name)
					}
				}
			}
		}
		if !available {
			out.add("project_secret", name, "missing", name+" needs a value on this server.")
			continue
		}
		selected = append(selected, name)
		if result := results[name]; result.Status == "added" {
			out.add("project_secret", name, "ready", name+" is available on this server.")
		} else {
			out.add("project_secret", name, "kept", name+" already set on this server; kept the server's value.")
		}
	}
	if len(selected) > 0 {
		status, _ := api.callExternalToolInternal(r, "settings", map[string]any{"action": "update", "crew_id": roots.ID, "secrets": map[string]any{"select": selected}})
		if status != 200 {
			externalError(w, status, "secrets_unavailable", "Select the Crew's secrets in this server's Vault, then retry sync.")
			return
		}
	}
	for _, want := range input.Connections {
		switch want.Kind {
		case "mcp", "google", "slack", "shared_secret":
		default:
			externalError(w, 400, "invalid_request", "Choose a supported Crew connection.")
			return
		}
		item, exact := matchCrewConnection(want, inventory)
		if item == nil {
			out.add(want.Kind, want.Name, "missing", want.Name+" is not available on this server. Connect or select it in Vault.")
			continue
		}
		if !exact && !item.Selected {
			out.add(want.Kind, want.Name, "missing", "Found "+item.Name+" on this server. Select it in the Crew’s Vault to use it.")
			continue
		}
		if item.Status != "connected" && item.Status != "stored" {
			out.add(want.Kind, want.Name, "sign_in", "Sign in to "+want.Name+" on this server.")
			continue
		}
		if !item.Selected {
			if _, e := api.selectProjectVaultItem(r.Context(), claims.UserID, roots.Virtual, item.Kind, item.ID, true); e != nil {
				out.add(want.Kind, want.Name, "missing", "Select "+want.Name+" in this server's Crew Vault.")
				continue
			}
		}
		if !item.Selected {
			out.remember(item.Kind, item.ID, crewDependencyKey(want))
		}
		if item.Name != want.Name {
			out.add(want.Kind, want.Name, "mapped", "Uses "+item.Name+" on the server for "+want.Name+".")
		} else {
			out.add(want.Kind, want.Name, "ready", want.Name+" is available on this server.")
		}
	}
	out.Message = "Crew connections and secrets are ready on the server."
	if !out.Ready {
		out.Message = "Some Crew connections need setup on the server."
	}
	if e = saveCrewDependencies(roots.ID, out); e != nil {
		externalError(w, 500, "dependencies_unavailable", "Could not save connection readiness. Retry sync.")
		return
	}
	externalJSON(w, out)
}

// The source owner is rechecked by runDesktopCrewSync/crewSyncRoots before this
// runs. No shared Vault value, login token or unselected secret is decrypted.
func (api *StreamingAPI) prepareCrewDependencies(r *http.Request, roots crewroot.Roots, b *crewroot.SyncBinding, connection desktopConnection, force bool) (*crewDependencyReport, error) {
	if b.Editing != "local" {
		return readCrewDependencies(roots.ID), nil
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID != roots.Owner {
		return nil, fmt.Errorf("only this Crew’s owner can prepare its connections")
	}
	if !vaultConfigured() {
		return nil, nil
	}
	rawManifest, found, e := readProjectRuntimeManifest(r.Context(), "work", roots.Virtual)
	if e != nil || !found {
		return nil, fmt.Errorf("Crew settings could not be checked before sync")
	}
	version := sha256.Sum256([]byte(rawManifest))
	if !force {
		if old, ok := crewDependencyChecks.Load(roots.ID); ok {
			check := old.(crewDependencyCheck)
			if check.Version == version && time.Since(check.At) < time.Minute {
				return readCrewDependencies(roots.ID), nil
			}
		}
	}
	inventory, e := api.projectVaultInventory(r.Context(), claims.UserID, roots.Virtual)
	if e != nil {
		return nil, fmt.Errorf("open this Crew's Vault and retry sync")
	}
	input := crewDependencyRequest{PairID: b.PairID, Names: inventory.Secrets, Values: map[string]string{}, SourceUnavailable: len(inventory.Notices) > 0}
	if len(input.Names) > 0 {
		rows, e := api.ensureSharedWorkflowSecrets(r.Context(), roots.Virtual, claims.UserID)
		if e != nil {
			return nil, fmt.Errorf("this Crew's selected secret names could not be checked")
		}
		for _, row := range rows {
			if slices.Contains(input.Names, row.Name) {
				value, e := decryptSharedWorkflowSecret(roots.Virtual, row)
				if e == nil {
					input.Values[row.Name] = value
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, group := range inventory.Vaults {
		for _, item := range group.Items {
			if !item.Selected || seen[item.Kind+":"+item.ID] {
				continue
			}
			seen[item.Kind+":"+item.ID] = true
			switch item.Kind {
			case "mcp", "google", "slack", "shared_secret":
				input.Connections = append(input.Connections, crewDependencyConnection{item.Kind, item.Name, item.Provider, item.AccountEmail})
			}
		}
	}
	raw, e := json.Marshal(input)
	if e != nil {
		return nil, fmt.Errorf("Crew connections could not be prepared")
	}
	request, e := http.NewRequestWithContext(r.Context(), http.MethodPost, connection.Server+"/api/crew-sync/"+url.PathEscape(b.PeerProjectID)+"/dependencies", bytes.NewReader(raw))
	if e != nil {
		return nil, fmt.Errorf("Crew connections could not be prepared")
	}
	request.Header.Set("Authorization", "Bearer "+connection.Token)
	request.Header.Set("Content-Type", "application/json")
	response, e := desktopHTTPClient().Do(request)
	if e != nil {
		return nil, fmt.Errorf("cannot reach the server to prepare this Crew's connections")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Crew connections could not be prepared on the server; check its Vault and server sign-in, then retry sync")
	}
	var out crewDependencyReport
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&out) != nil {
		return nil, fmt.Errorf("the server's connection readiness could not be read")
	}
	if e = saveCrewDependencies(roots.ID, &out); e != nil {
		return nil, e
	}
	crewDependencyChecks.Store(roots.ID, crewDependencyCheck{version, time.Now()})
	return &out, nil
}
