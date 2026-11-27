package server

// A Crew made on the server keeps its keys and apps in its own Crew vault (decisions log, 22 Nov; #3501): one Vault
// project vault whose owners are the Crew's main owner and co-owners, all equal. Every agent, chat, schedule and
// workflow of that Crew uses only what is in it; nobody's personal Vault items are added on top. Because the owners
// really own it in the Vault service, they change it with the ordinary Vault tools (manage_my_vaults with crew_id, the
// Crew vault panel); Run people never see it. Mac Crews keep each person's own Vault plus their Team.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// Nothing here writes to the Vault service on startup or on a read (#3596: creating every Crew's vault while the
// server started kept it from coming up, and each Vault write takes seconds). Reads use the vault's fixed ID and treat
// a vault that does not exist yet as empty. A vault is made only by a Crew vault action (synchronously, for the person
// who asked), or in the background, one Crew at a time, after a real turn of that Crew or a co-owner change.

var errNotServerCrew = errors.New("Only a project made on the server has a project vault; a project on a Mac uses each person's own Vault and its Team.")

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
	// Co-owners only: a User works with the project's keys but does not manage them (crew_setup_lock.go).
	for _, id := range crewCoOwnersOnly(crewProjectRootByID(project)) {
		if !slices.Contains(owners, id) {
			owners = append(owners, id)
		}
	}
	return owners
}

// crewVaultID is a server Crew's vault ID, the same one the Vault service gives it (/api/admin/crew-vaults).
func crewVaultID(project string) string {
	sum := sha256.Sum256([]byte("crew:" + project))
	return "v-" + hex.EncodeToString(sum[:12])
}

// crewVaultQueue makes Crew vaults in the background, one at a time with a pause between them, so a burst of first
// turns never holds the Vault service (and the reads waiting on it) for long.
var crewVaultQueue = struct {
	once    sync.Once
	mu      sync.Mutex
	pending map[string]bool
	work    chan string
}{pending: map[string]bool{}, work: make(chan string, 1024)}

const crewVaultQueueGap = 3 * time.Second

// crewVaultQueueStartDelay keeps the queue idle right after the server starts, while everything else is coming up.
const crewVaultQueueStartDelay = 2 * time.Minute

var crewVaultProcessStart = time.Now()

// requestCrewVault asks for a server Crew's vault to be made (or its owners updated) in the background. It never waits.
func (api *StreamingAPI) requestCrewVault(project string) {
	if !vaultConfigured() || serverCrewProject(project) == "" {
		return
	}
	if cached, ok := crewVaultCache.Load(project); ok && cached.(crewVaultCached).Owners == strings.Join(crewVaultOwners(project), ",") {
		return
	}
	crewVaultQueue.once.Do(func() { go api.runCrewVaultQueue() })
	crewVaultQueue.mu.Lock()
	defer crewVaultQueue.mu.Unlock()
	if crewVaultQueue.pending[project] {
		return
	}
	select {
	case crewVaultQueue.work <- project:
		crewVaultQueue.pending[project] = true
	default:
		log.Printf("[CREW_VAULT] project=%s queue full; made on its next turn", project)
	}
}

func (api *StreamingAPI) runCrewVaultQueue() {
	if wait := time.Until(crewVaultProcessStart.Add(crewVaultQueueStartDelay)); wait > 0 {
		time.Sleep(wait)
	}
	for project := range crewVaultQueue.work {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		if _, err := api.ensureCrewVault(ctx, project); err != nil {
			log.Printf("[CREW_VAULT] project=%s not made yet: %v", project, err)
		}
		cancel()
		crewVaultQueue.mu.Lock()
		delete(crewVaultQueue.pending, project)
		crewVaultQueue.mu.Unlock()
		time.Sleep(crewVaultQueueGap)
	}
}

// crewVaultTurnLimit bounds making one Crew's vault on its own turn (normally well under a second).
const crewVaultTurnLimit = 8 * time.Second

// ensureCrewVaultForTurn makes a server Crew's vault on that Crew's real turn, so its first turn already has its keys
// and apps. Only this one Crew, with a short limit; on a timeout, or right after the server starts, the background
// queue does it instead. It never runs for every Crew at once.
func (api *StreamingAPI) ensureCrewVaultForTurn(ctx context.Context, workspacePath string) {
	project := serverCrewForPath(workspacePath)
	if project == "" || !vaultConfigured() {
		return
	}
	if cached, ok := crewVaultCache.Load(project); ok && cached.(crewVaultCached).Owners == strings.Join(crewVaultOwners(project), ",") {
		return
	}
	if time.Since(crewVaultProcessStart) < crewVaultQueueStartDelay {
		api.requestCrewVault(project)
		return
	}
	turnCtx, cancel := context.WithTimeout(ctx, crewVaultTurnLimit)
	defer cancel()
	if _, err := api.ensureCrewVault(turnCtx, project); err != nil {
		log.Printf("[CREW_VAULT] project=%s not made on its turn, queued: %v", project, err)
		api.requestCrewVault(project)
	}
}

// serverCrewProject is project when it is a Crew made on the server, else "".
func serverCrewProject(project string) string {
	if IsLocalMode() || project == "" || !crewLivesOnServer(project) {
		return ""
	}
	return project
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
	if cached, ok := crewVaultCache.Load(project); ok && cached.(crewVaultCached).Owners == key {
		return cached.(crewVaultCached).ID, nil
	}
	for _, person := range owners {
		// The Vault service learns a person the first time they ask for something; do that on their behalf.
		if _, err := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults", nil); err != nil {
			return "", err
		}
	}
	_, title := crewOwnerAndTitle(project)
	name := "Project vault"
	if title != "" {
		name = "Project vault · " + title
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
		return "", errors.New("The project vault could not be made.")
	}
	seeded := true
	if made.Created || !crewVaultSeeded(project) {
		seeded = api.seedCrewVault(ctx, project, owners[0], made.ID) && ctx.Err() == nil
		if seeded {
			markCrewVaultSeeded(project)
		}
		forgetCrewVaultContents(project)
	}
	if !seeded {
		log.Printf("[CREW_VAULT] project=%s vault=%s seeding incomplete (ctx_err=%v); not caching so next turn/queue retries", project, made.ID, ctx.Err())
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return made.ID, nil
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

// readCrewVault is what a server Crew's vault holds now, read as its main owner. It never makes the vault: one that
// does not exist yet is empty.
func (api *StreamingAPI) readCrewVault(ctx context.Context, project string) (crewVaultContents, error) {
	id := crewVaultID(project)
	if cached, ok := crewVaultContentsCache.Load(project); ok {
		if c := cached.(crewVaultContentsCached); time.Since(c.at) < crewVaultContentsTTL {
			return c.contents, nil
		}
	}
	out, err := readCrewVaultNow(ctx, ownerOfCrewProjectID(project), id)
	if err != nil && strings.Contains(err.Error(), "(404)") {
		out, err = crewVaultContents{VaultID: id}, nil // not made yet
	}
	if err != nil {
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
		return out, errors.New("The project vault could not be read.")
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
	// v2: the first fill copied only personal-vault keys and left out shared and older server keys; every Crew fills
	// once more (what is already there is skipped).
	return filepath.Join(state, "crew-vault-seeded-v2", sanitizeUserIDForPath(project))
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

// crewVaultSecretNames are the Vault names of a server Crew's secrets: everything in its vault, plus company keys whose
// names its vault does not use.
func (api *StreamingAPI) crewVaultSecretNames(ctx context.Context, project string) []string {
	contents, err := api.readCrewVault(ctx, project)
	if err != nil {
		log.Printf("[CREW_VAULT] project=%s secrets unavailable: %v", project, err)
		return []string{}
	}
	names := withProjectCompanyKeys(contents.SecretNames) // company keys reach it directly (#4148)
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
		return "", errors.New("Only the project's owner and co-owners can change its project vault. Ask one of them.")
	}
	return api.ensureCrewVault(ctx, project)
}

// seedCrewVault fills a new Crew vault with copies of what the Crew already used from the main owner's Vault: every
// selected key its turns received (values stay inside the server) and the apps in use. An API-key or no-login app is
// copied with its key; a sign-in app is added for one person to sign in for the Crew. The owner keeps their own copies.
// Each item that cannot be copied is logged and left for the owners to add.
func (api *StreamingAPI) seedCrewVault(ctx context.Context, project, owner, crewVault string) bool {
	root := crewProjectRootByID(project)
	personal, err := ensurePersonalVault(ctx, owner)
	if err != nil || root == "" {
		log.Printf("[CREW_VAULT] project=%s seed skipped: %v", project, err)
		return err == nil && ctx.Err() == nil
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
	} else if ctx.Err() != nil {
		return false
	}
	if selection, err := projectSecretSelection(ctx, root); err == nil {
		// Every key the Crew's turns and runs received before it had a vault, resolved for the owner exactly as a turn
		// resolved them: personal-vault keys, shared-vault keys and older server keys picked by plain name alike.
		for _, s := range api.mergeGlobalSecretsFor(ctx, owner, &selection) {
			if ctx.Err() != nil {
				return false
			}
			if have["secret:"+s.Name] || strings.TrimSpace(s.Value) == "" {
				continue
			}
			if err := api.setVaultSecret(ctx, owner, crewVault, s.Name, s.Value, false); err != nil {
				log.Printf("[CREW_VAULT] project=%s seed secret=%s not copied: %v", project, s.Name, err)
				if ctx.Err() != nil {
					return false
				}
				continue
			}
			log.Printf("[CREW_VAULT] project=%s seed secret=%s copied", project, s.Name)
			have["secret:"+s.Name] = true
			keys++
		}
	}
	if ctx.Err() != nil {
		return false
	}
	selected, chosenOnly, _ := projectAppSelection(ctx, root)
	if list, err := personalVaults(ctx, owner); err == nil {
		for _, v := range list.Vaults {
			if v.Role != "owner" || v.Group.ID != personal {
				continue
			}
			for _, c := range v.Connections {
				if ctx.Err() != nil {
					return false
				}
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
					if ctx.Err() != nil {
						return false
					}
					continue
				}
				log.Printf("[CREW_VAULT] project=%s seed app=%s provider=%s copied", project, c.ID, c.Provider)
				apps++
			}
		}
	} else if ctx.Err() != nil {
		return false
	}
	log.Printf("[CREW_VAULT] project=%s seeded from owner=%s keys=%d apps=%d", project, logUsernameForUserID(owner), keys, apps)
	return ctx.Err() == nil
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

// crewVaultProjectIDs returns all known Crew project IDs authoritatively from
// the project-owners registry (plus any cached entries as a fallback), so
// vaultID -> project resolution does not depend on the evictable 20s contents
// cache.
func crewVaultProjectIDs() []string {
	seen := map[string]bool{}
	var ids []string
	if records, err := defaultProjectOwners().All(); err == nil {
		for _, rec := range records {
			if rec.Product == crewProfileID && rec.ProjectID != "" && !seen[rec.ProjectID] {
				seen[rec.ProjectID] = true
				ids = append(ids, rec.ProjectID)
			}
		}
	}
	crewVaultContentsCache.Range(func(key, _ any) bool {
		if id, ok := key.(string); ok && id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
		return true
	})
	return ids
}

// crewVaultReader is whose access a turn reads its server Crew vault with. A User of that Crew works with its keys and
// apps but is not one of the vault's owners (crewVaultOwners), so their turn reads it as the Crew's main owner; the
// scope still limits the read to this one Crew vault and the turn takes only the Crew's own selection from it. Everyone
// else reads as themselves.
func crewVaultReader(ctx context.Context, person string) string {
	scope := crewVaultScope(ctx)
	if scope == "" || person == "" {
		return person
	}
	project := ""
	for _, id := range crewVaultProjectIDs() {
		if crewVaultID(id) == scope {
			project = id
			break
		}
	}
	if project == "" {
		return person
	}
	root := crewProjectRootByID(project)
	if root == "" || !crewIsProjectUser(root, person) {
		return person
	}
	if owner := ownerOfCrewProjectID(project); owner != "" {
		return owner
	}
	return person
}

// crewVaultRowsOnly keeps the rows that are in the scoped Crew vault itself, and company keys, so a User's turn that
// reads as the main owner never takes one of the owner's own keys (not even as a same-named stand-in).
func crewVaultRowsOnly(ctx context.Context, rows []globalSecretEntry) []globalSecretEntry {
	prefix, err := vaultSecretName(crewVaultScope(ctx), "x")
	if err != nil {
		return nil
	}
	prefix = strings.TrimSuffix(prefix, "x")
	out := rows[:0:0]
	for _, row := range rows {
		// Company keys reach everyone, a User included (#4148).
		if strings.HasPrefix(row.Name, prefix) || (isCompanyKeyName(row.Name) && companyKeysOn()) {
			out = append(out, row)
		}
	}
	return out
}

// crewVaultAppRowsOnly keeps the apps a server Crew's turn picked from its Crew vault (crewVaultServers), so a User's
// turn that reads as the main owner never gets one of the owner's own apps.
func crewVaultAppRowsOnly(rows []vaultRuntimeServer, picks []string) []vaultRuntimeServer {
	out := rows[:0:0]
	for _, row := range rows {
		if slices.Contains(picks, vaultServerName(row.ID)) {
			out = append(out, row)
		}
	}
	return out
}

// withCrewVaultScopeForNames scopes a key lookup to the Crew vault its selected names come from: a server Crew's
// selection is its Crew vault's names (crewVaultSecretNames), each starting with that vault's prefix.
func withCrewVaultScopeForNames(ctx context.Context, names []string) context.Context {
	if crewVaultScope(ctx) != "" {
		return ctx
	}
	scope := ""
	for _, project := range crewVaultProjectIDs() {
		id := crewVaultID(project)
		prefix, err := vaultSecretName(id, "x")
		if err != nil {
			continue
		}
		prefix = strings.TrimSuffix(prefix, "x")
		for _, name := range names {
			if strings.HasPrefix(name, prefix) {
				scope = id
				break
			}
		}
		if scope != "" {
			break
		}
	}
	if scope == "" {
		return ctx
	}
	return withCrewVaultScope(ctx, scope)
}

// isCrewVaultGroup reports whether a vault listed by the Vault service is a server Crew's vault.
func isCrewVaultGroup(projectRoot string) bool {
	return strings.HasPrefix(projectRoot, "crew:")
}
