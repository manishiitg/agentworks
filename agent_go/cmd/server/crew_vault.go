package server

// A Crew made on the server keeps its keys and apps in its own Crew vault (decisions log, 22 Nov; #3501): one Vault
// project vault whose owners are the Crew's main owner and co-owners, all equal. Every agent, chat, schedule and
// workflow of that Crew uses only what is in it; nobody's personal Vault items are added on top. Because the owners
// really own it in the Vault service, they change it with the ordinary Vault tools (manage_my_vaults with crew_id, the
// Crew vault panel); Run people never see it. Mac Crews keep each person's own Vault plus their Team.
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
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/mcpclient"
)

var errNotServerCrew = errors.New("Only a Crew made on the server has a Crew vault; a Crew on a Mac uses each person's own Vault and its Team.")

// crewVaultCache remembers each Crew's vault and the owners last sent, so a turn does not call the Vault service again
// until the owners change.
var crewVaultCache sync.Map // project -> crewVaultCached

type crewVaultCached struct{ ID, Owners string }

var crewVaultLocks sync.Map

// crewVaultOwners are the people who own a server Crew's vault: its main owner and its co-owners, read now.
func crewVaultOwners(project string) []string {
	owner := ownerOfCrewProjectID(project)
	if owner == "" {
		return nil
	}
	owners := []string{owner}
	for _, id := range crewCoOwnerIDs(crewProjectRootByID(project)) {
		if !slices.Contains(owners, id) {
			owners = append(owners, id)
		}
	}
	return owners
}

// ensureCrewVault returns a server Crew's vault, making it the first time and keeping its owners exactly the Crew's
// main owner and co-owners. A new vault starts with copies of what the Crew already used from the main owner's Vault.
func (api *StreamingAPI) ensureCrewVault(ctx context.Context, project string) (string, error) {
	if !vaultConfigured() {
		return "", errors.New("Vault is not set up on this server")
	}
	if !crewLivesOnServer(project) {
		return "", errNotServerCrew
	}
	owners := crewVaultOwners(project)
	if len(owners) == 0 {
		return "", errNotServerCrew
	}
	key := strings.Join(owners, ",")
	if cached, ok := crewVaultCache.Load(project); ok && cached.(crewVaultCached).Owners == key {
		return cached.(crewVaultCached).ID, nil
	}
	lock, _ := crewVaultLocks.LoadOrStore(project, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	for _, person := range owners {
		// The Vault service learns a person the first time they ask for something; do that on their behalf.
		if _, err := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults", nil); err != nil {
			return "", err
		}
	}
	_, title := crewOwnerAndTitle(project)
	name := "Crew vault"
	if title != "" {
		name = "Crew vault · " + title
	}
	body, _ := json.Marshal(map[string]any{"project_id": project, "name": name, "owners": owners})
	data, err := vaultServiceRequest(ctx, "", http.MethodPost, "/api/admin/crew-vaults", body)
	if err != nil {
		return "", err
	}
	var made struct {
		ID      string `json:"id"`
		Created bool   `json:"created"`
	}
	if json.Unmarshal(data, &made) != nil || made.ID == "" {
		return "", errors.New("The Crew vault could not be made.")
	}
	if made.Created || !crewVaultSeeded(project) {
		api.seedCrewVault(ctx, project, owners[0], made.ID)
		markCrewVaultSeeded(project)
		forgetCrewVaultContents(project)
	}
	crewVaultCache.Store(project, crewVaultCached{ID: made.ID, Owners: key})
	log.Printf("[CREW_VAULT] project=%s vault=%s owners=%d created=%t", project, made.ID, len(owners), made.Created)
	return made.ID, nil
}

type crewVaultContents struct {
	VaultID string `json:"vault_id"`
	Apps    []struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Provider string `json:"provider"`
		Status   string `json:"status"`
		SignIn   bool   `json:"sign_in,omitempty"`
	} `json:"connections"`
	// SecretNames are the Vault names (VLT_<vault>__NAME); Secrets are the names people use.
	SecretNames []string `json:"secret_names"`
}

// crewVaultContentsTTL keeps a Crew vault's contents briefly: the runtime reads a Crew's app list very often (every
// chat binding), and asking the Vault service each time floods it. A change through the Crew vault tools forgets it at
// once; anything else shows within this long.
const crewVaultContentsTTL = 20 * time.Second

type crewVaultContentsCached struct {
	at       time.Time
	contents crewVaultContents
}

var crewVaultContentsCache sync.Map // project -> crewVaultContentsCached

func forgetCrewVaultContents(project string) { crewVaultContentsCache.Delete(project) }

// readCrewVault is what a server Crew's vault holds now, read as its main owner.
func (api *StreamingAPI) readCrewVault(ctx context.Context, project string) (crewVaultContents, error) {
	var out crewVaultContents
	id, err := api.ensureCrewVault(ctx, project)
	if err != nil {
		return out, err
	}
	if cached, ok := crewVaultContentsCache.Load(project); ok {
		if c := cached.(crewVaultContentsCached); c.contents.VaultID == id && time.Since(c.at) < crewVaultContentsTTL {
			return c.contents, nil
		}
	}
	if out, err = readCrewVaultNow(ctx, ownerOfCrewProjectID(project), id); err != nil {
		return out, err
	}
	crewVaultContentsCache.Store(project, crewVaultContentsCached{at: time.Now(), contents: out})
	return out, nil
}

func readCrewVaultNow(ctx context.Context, owner, id string) (crewVaultContents, error) {
	var out crewVaultContents
	data, err := vaultPersonRequest(ctx, owner, http.MethodGet, "/api/vaults/"+url.PathEscape(id), nil)
	if err != nil {
		return out, err
	}
	if json.Unmarshal(data, &out) != nil {
		return out, errors.New("The Crew vault could not be read.")
	}
	out.VaultID = id
	return out, nil
}

// A Crew vault is filled from the owner once; the mark lets a fill that failed part-way (or ran before a fix) run
// again until it has finished. Filling skips what is already there, so running it twice copies nothing twice.
func crewVaultSeedMark(project string) string {
	state, err := workflowCLIStateRoot()
	if err != nil || strings.TrimSpace(state) == "" {
		return ""
	}
	return filepath.Join(state, "crew-vault-seeded", sanitizeUserIDForPath(project))
}

func crewVaultSeeded(project string) bool {
	mark := crewVaultSeedMark(project)
	if mark == "" {
		return true
	}
	_, err := os.Stat(mark)
	return err == nil
}

func markCrewVaultSeeded(project string) {
	if mark := crewVaultSeedMark(project); mark != "" {
		if os.MkdirAll(filepath.Dir(mark), 0o700) == nil {
			_ = os.WriteFile(mark, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
		}
	}
}

// serverCrewForPath is the project ID of the server Crew at workspacePath, or "" for anything else (a Mac Crew too).
func serverCrewForPath(workspacePath string) string {
	if IsLocalMode() {
		return ""
	}
	if project := crewPanelProjectForPath(workspacePath); project != "" && crewLivesOnServer(project) {
		return project
	}
	return ""
}

// crewVaultServers is a server Crew's app list as the runtime takes it: the Crew vault's apps and the non-Vault servers
// the Crew chose, and nothing else (the NoServers mark stops the runtime adding anyone's other Vault apps). If the vault
// cannot be read, the agent gets no Vault apps rather than someone's personal ones.
func (api *StreamingAPI) crewVaultServers(ctx context.Context, project string, saved []string) []string {
	out := []string{}
	for _, name := range runtimeAppSelection(saved, true) {
		if name != mcpclient.NoServers && !governedVaultName(name) {
			out = append(out, name)
		}
	}
	contents, err := api.readCrewVault(ctx, project)
	if err != nil {
		log.Printf("[CREW_VAULT] project=%s apps unavailable: %v", project, err)
	}
	for _, app := range contents.Apps {
		out = append(out, vaultServerName(app.ID))
	}
	return append(out, mcpclient.NoServers)
}

// crewVaultSecretNames are the Vault names of a server Crew's secrets: everything in its vault, nothing else.
func (api *StreamingAPI) crewVaultSecretNames(ctx context.Context, project string) []string {
	contents, err := api.readCrewVault(ctx, project)
	if err != nil {
		log.Printf("[CREW_VAULT] project=%s secrets unavailable: %v", project, err)
		return []string{}
	}
	names := append([]string{}, contents.SecretNames...)
	slices.Sort(names)
	return names
}

// crewVaultFor is the vault a Crew builder's manage_my_vaults call changes when it names crew_id: the server Crew's
// vault, for its main owner and co-owners only.
func (api *StreamingAPI) crewVaultFor(ctx context.Context, person, project string) (string, error) {
	project = strings.TrimSpace(project)
	if !crewLivesOnServer(project) {
		return "", errNotServerCrew
	}
	if !slices.Contains(crewVaultOwners(project), person) {
		return "", errors.New("Only the Crew's owner and co-owners can change its Crew vault. Ask one of them.")
	}
	return api.ensureCrewVault(ctx, project)
}

// seedCrewVault fills a new Crew vault with copies of what the Crew already used from the main owner's own Vault: the
// selected keys (values stay inside the server) and the apps in use. An API-key or no-login app is copied with its key;
// a sign-in app is added for one person to sign in for the Crew. The owner keeps their own copies. Each item that
// cannot be copied is logged and left for the owners to add.
func (api *StreamingAPI) seedCrewVault(ctx context.Context, project, owner, crewVault string) {
	root := crewProjectRootByID(project)
	personal, err := ensurePersonalVault(ctx, owner)
	if err != nil || root == "" {
		log.Printf("[CREW_VAULT] project=%s seed skipped: %v", project, err)
		return
	}
	keys, apps := 0, 0
	have := map[string]bool{}
	if current, err := readCrewVaultNow(ctx, owner, crewVault); err == nil {
		for _, name := range current.SecretNames {
			have["secret:"+vaultSecretShortName(name)] = true
		}
		for _, app := range current.Apps {
			have["app:"+strings.ToLower(app.Provider+"\x00"+app.Label)] = true
		}
	}
	if selection, err := projectSecretSelection(ctx, root); err == nil {
		values, _ := ownVaultSecretValues(ctx, owner, selection)
		for short, value := range values { // only the owner's own keys, by the name they gave them
			if have["secret:"+short] {
				continue
			}
			if err := api.setVaultSecret(ctx, owner, crewVault, short, value, false); err != nil {
				log.Printf("[CREW_VAULT] project=%s seed secret=%s not copied: %v", project, short, err)
				continue
			}
			log.Printf("[CREW_VAULT] project=%s seed secret=%s copied", project, short)
			keys++
		}
	}
	selected, chosenOnly, _ := projectAppSelection(ctx, root)
	if list, err := personalVaults(ctx, owner); err == nil {
		for _, v := range list.Vaults {
			if v.Role != "owner" || v.Group.ID != personal {
				continue
			}
			for _, c := range v.Connections {
				if !projectVaultAppInUse(ctx, selected, chosenOnly, c.ID) || have["app:"+strings.ToLower(c.Provider+"\x00"+c.Label)] {
					continue
				}
				saved, err := vaultSavedConnection(ctx, c.ID)
				if err == nil && teamWayShared(saved) {
					body, _ := json.Marshal(map[string]string{"to_vault": crewVault, "to_owner": owner})
					_, err = vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(personal)+"/connectors/"+url.PathEscape(c.ID)+"/copy", body)
				} else if err == nil {
					body, _ := json.Marshal(map[string]string{"Provider": c.Provider, "Label": c.Label})
					_, err = vaultPersonRequest(ctx, owner, http.MethodPost, "/api/vaults/"+url.PathEscape(crewVault)+"/connectors", body)
				}
				if err != nil {
					log.Printf("[CREW_VAULT] project=%s seed app=%s not copied: %v", project, c.ID, err)
					continue
				}
				log.Printf("[CREW_VAULT] project=%s seed app=%s provider=%s copied", project, c.ID, c.Provider)
				apps++
			}
		}
	}
	log.Printf("[CREW_VAULT] project=%s seeded from owner=%s keys=%d apps=%d", project, logUsernameForUserID(owner), keys, apps)
}

// crewVaultScopeKey carries the one Crew vault a request may see. The Vault service leaves every Crew vault out of
// runtime lists unless the request names it, so a Crew vault's apps and keys reach only that Crew's own turns and
// runs, never "use all apps", a personal project or a picker.
type crewVaultScopeKey struct{}

func withCrewVaultScope(ctx context.Context, vaultID string) context.Context {
	return context.WithValue(ctx, crewVaultScopeKey{}, vaultID)
}

func crewVaultScope(ctx context.Context) string {
	scope, _ := ctx.Value(crewVaultScopeKey{}).(string)
	return scope
}

// withCrewVaultScopeForNames scopes a key lookup to the Crew vault its selected names come from: a server Crew's
// selection is its Crew vault's names (crewVaultSecretNames), each starting with that vault's prefix.
func withCrewVaultScopeForNames(ctx context.Context, names []string) context.Context {
	if crewVaultScope(ctx) != "" {
		return ctx
	}
	scope := ""
	crewVaultCache.Range(func(_, value any) bool {
		id := value.(crewVaultCached).ID
		prefix, err := vaultSecretName(id, "x")
		if err != nil {
			return true
		}
		prefix = strings.TrimSuffix(prefix, "x")
		for _, name := range names {
			if strings.HasPrefix(name, prefix) {
				scope = id
				return false
			}
		}
		return true
	})
	if scope == "" {
		return ctx
	}
	return withCrewVaultScope(ctx, scope)
}

// isCrewVaultGroup reports whether a vault listed by the Vault service is a server Crew's vault.
func isCrewVaultGroup(projectRoot string) bool {
	return strings.HasPrefix(projectRoot, "crew:")
}
