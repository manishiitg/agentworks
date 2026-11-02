package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Names and selected resource identities only. Credentials never enter project sync.
type crewSyncProject struct {
	Name         string                     `json:"name"`
	Purpose      string                     `json:"purpose,omitempty"`
	Icon         string                     `json:"icon,omitempty"`
	Mode         string                     `json:"mode,omitempty"`
	Dependencies []crewDependencyConnection `json:"dependencies,omitempty"`
}

func readCrewSourceProject(id string) *crewSyncProject {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p + ".source-project")
	if err != nil {
		return nil
	}
	var out crewSyncProject
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	return &out
}

func crewAlreadyOnMac(binding *crewroot.SyncBinding) error {
	name := strings.TrimSpace(binding.MacName)
	if name == "" {
		name = "another Mac"
	}
	return fmt.Errorf("this project is already open on %s; use that Mac's existing copy or open the project on the server. Moving its pairing to another Mac is not available yet", name)
}

func (api *StreamingAPI) crewSyncProject(ctx context.Context, claims *UserClaims, roots crewroot.Roots) (*crewSyncProject, error) {
	raw, found, err := readFileFromWorkspace(ctx, roots.Virtual+"/product.json")
	if err != nil || !found {
		return nil, fmt.Errorf("this project's settings are unavailable")
	}
	var metadata struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Mode        string `json:"mode"`
		Identity    struct {
			Icon string `json:"icon"`
		} `json:"identity"`
	}
	if json.Unmarshal([]byte(raw), &metadata) != nil {
		return nil, fmt.Errorf("this project's settings are unavailable")
	}
	out := &crewSyncProject{Name: metadata.Title, Purpose: metadata.Description, Mode: metadata.Mode, Icon: metadata.Identity.Icon}
	if !vaultConfigured() {
		return out, nil
	}
	inventory, err := api.projectVaultInventory(ctx, claims.UserID, roots.Virtual)
	if err != nil {
		return nil, fmt.Errorf("this project's selected connections could not be checked; retry from Project sync")
	}
	for _, name := range inventory.Secrets {
		out.Dependencies = append(out.Dependencies, crewDependencyConnection{Kind: "project_secret", Name: name})
	}
	for _, group := range inventory.Vaults {
		for _, item := range group.Items {
			if item.Selected {
				out.Dependencies = append(out.Dependencies, crewDependencyConnection{Kind: item.Kind, Name: item.Name, Provider: item.Provider, AccountEmail: item.AccountEmail})
			}
		}
	}
	return out, nil
}

type crewLocalOpenReply struct {
	ProjectID string                `json:"project_id"`
	Folder    string                `json:"folder"`
	Project   *crewSyncProject      `json:"project"`
	Binding   *crewroot.SyncBinding `json:"binding,omitempty"`
	Notice    string                `json:"conversation_notice,omitempty"`
}

func (api *StreamingAPI) handleDesktopCrewOpen(w http.ResponseWriter, r *http.Request) {
	if !desktopConnectionGate(w, r) {
		return
	}
	var input struct {
		Folder string `json:"folder"`
	}
	if r.Method == http.MethodPost && json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&input) != nil {
		externalError(w, 400, "invalid_request", "Choose an empty folder on this Mac.")
		return
	}
	out, err := api.openServerCrewOnMac(r, mux.Vars(r)["connection"], mux.Vars(r)["project"], input.Folder, r.Method == http.MethodGet)
	if err != nil {
		externalError(w, 409, "local_project_unavailable", err.Error())
		return
	}
	externalJSON(w, out)
}

// The page and local AgentWorks chat use this same operation. The server
// independently admits its actual owner through crewSyncRoots on every call.
func (api *StreamingAPI) openServerCrewOnMac(r *http.Request, connectionID, project, folder string, inspect bool) (*crewLocalOpenReply, error) {
	claims := GetUserFromContext(r.Context())
	if !IsLocalMode() || !websiteDeviceClaims(claims) || !personalLaptopSettingsClaims(claims) {
		return nil, fmt.Errorf("open AgentWorks on your Mac and use your own connected server")
	}
	if _, err := uuid.Parse(project); err != nil {
		return nil, fmt.Errorf("choose an existing project on your connected server")
	}
	connection, err := desktopConnectionFor(claims.UserID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("connect to this project's server in the Mac app first")
	}
	remote, err := desktopCrewSyncCall(r.Context(), claims.UserID, &connection, project, crewSyncRequest{Action: "inspect"})
	if err != nil {
		return nil, err
	}
	if remote.ProjectID != project || remote.Project == nil {
		return nil, fmt.Errorf("the server did not return this project's details")
	}
	if strings.TrimSpace(folder) == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return nil, e
		}
		folder = filepath.Join(home, "AgentWorks", slugifyCrewTitle(remote.Project.Name))
	}
	out := &crewLocalOpenReply{ProjectID: project, Folder: folder, Project: remote.Project}
	existing := false
	// Only reuse an existing root whose server-controlled pair points here.
	all, err := defaultProjectOwners().All()
	if err != nil {
		return nil, err
	}
	for _, record := range all {
		if record.Product != "work" || record.ProjectID != project {
			continue
		}
		if record.OwnerID != claims.UserID {
			return nil, fmt.Errorf("this project belongs to another account on this Mac")
		}
		roots, e := api.crewSyncRoots(r.Context(), claims, project)
		if e != nil {
			return nil, e
		}
		binding, e := crewroot.ReadSync(project)
		if e != nil || binding == nil || binding.Side != "local" || binding.ConnectionID != connection.ID || binding.PeerProjectID != project {
			return nil, fmt.Errorf("a different project with this identity is already on this Mac; open its existing copy")
		}
		out.Folder, out.Binding = roots.Files, binding
		if remote.Binding == nil || remote.Binding.PairID != binding.PairID {
			return nil, fmt.Errorf("the existing local copy no longer matches this server; reconnect its original project")
		}
		if inspect || (binding.PendingAction == "" && readCrewSourceProject(project) != nil) {
			return out, nil
		}
		folder, existing = roots.Files, true
		break
	}
	if remote.Binding != nil && (remote.Binding.Side != "server" || remote.Binding.PeerProjectID != project) {
		return nil, crewAlreadyOnMac(remote.Binding)
	}
	// A pair seen on the server isn't an admission for a second Mac. Only
	// this installation's private receipt can resume an interrupted restore.
	localPair, err := crewroot.ReadSync(project)
	if err != nil {
		return nil, err
	}
	if remote.Binding != nil && (localPair == nil || localPair.Side != "local" || localPair.PairID != remote.Binding.PairID || localPair.ConnectionID != connection.ID || localPair.PeerProjectID != project || localPair.PeerServer != connection.Server) {
		return nil, crewAlreadyOnMac(remote.Binding)
	}
	if !existing && remote.Binding != nil && remote.Binding.Editing != "server" {
		return nil, fmt.Errorf("work is on this project's original Mac; reconnect that copy before opening it here")
	}
	if inspect {
		return out, nil
	}
	if !existing {
		if !filepath.IsAbs(folder) {
			return nil, fmt.Errorf("choose an empty folder using its full path")
		}
		if err := os.MkdirAll(folder, 0700); err != nil {
			return nil, fmt.Errorf("the selected folder could not be created; choose an empty folder")
		}
		entries, err := os.ReadDir(folder)
		if err != nil || len(entries) != 0 {
			return nil, fmt.Errorf("pick an empty folder; existing files will stay where they are")
		}
		folder, err = validateNewLocalCrewRoot(folder)
		if err != nil {
			return nil, err
		}
	}
	pair := uuid.NewString()
	if localPair != nil && remote.Binding != nil {
		pair = localPair.PairID
	}
	macName, _ := os.Hostname()
	remote, err = desktopCrewSyncCall(r.Context(), claims.UserID, &connection, project, crewSyncRequest{Action: "pair", PairID: pair, SourceID: project, MacName: macName})
	if err != nil {
		return nil, err
	}
	if remote.ProjectID != project || remote.Binding == nil || remote.Binding.PairID != pair || remote.Project == nil {
		return nil, fmt.Errorf("the server did not confirm this project's pair; retry")
	}
	binding := crewroot.SyncBinding{PairID: pair, Side: "local", Editing: "server", ConnectionID: connection.ID, PeerProjectID: project, PeerServer: connection.Server, SplitFiles: remote.Binding.SplitFiles, MacName: remote.Binding.MacName}
	// Resolve the original flat/split layout before writing local manifests.
	if err := crewroot.WriteSync(project, binding); err != nil {
		return nil, err
	}
	if !existing {
		_, err = api.createWorkspace(r.Context(), claims, "work", crewSpec{Name: remote.Project.Name, Purpose: remote.Project.Purpose, Icon: remote.Project.Icon, Mode: remote.Project.Mode, RootPath: folder}, false, workspaceCreation{ReservedID: project, Restore: true})
		if err != nil {
			return nil, err
		}
	}
	// Existing download copies files without moving editing. Opening a view
	// never starts a process; the first local send uses the usual handoff.
	data, _ := json.Marshal(map[string]string{"action": "download"})
	download := r.Clone(r.Context())
	download.Method, download.Body = http.MethodPost, io.NopCloser(bytes.NewReader(data))
	download = mux.SetURLVars(download, map[string]string{"connection": connection.ID, "project": project})
	response := &internalResponseCapture{}
	api.runDesktopCrewSync(response, download, false)
	if response.status >= 400 {
		return nil, fmt.Errorf("%s", laptopActionErrorText(response.body.String()))
	}
	roots, err := api.crewSyncRoots(r.Context(), claims, project)
	if err != nil {
		return nil, err
	}
	if remote.Conversation != nil {
		main, e := api.crewHandoffMain(r.Context(), claims, project)
		if e != nil {
			return nil, e
		}
		unlock, e := wf.LockWorkspace(r.Context(), roots.Files)
		if e != nil {
			return nil, e
		}
		defer unlock()
		if remote.Conversation.Native != nil {
			if e = desktopCrewNativeSync(r.Context(), roots, connection, &binding, remote.Conversation.Native, false); e != nil {
				return nil, e
			}
		}
		if e = api.importCrewHandoffConversation(r.Context(), claims, roots, pair, "open-"+pair, remote.Conversation, main); e != nil {
			return nil, e
		}
		out.Notice = remote.Conversation.Notice
	}
	// Private metadata contains names only and explains dependencies still
	// needed on this computer. It is never a token or secret export.
	if p, e := crewSyncManifestPath(project); e == nil {
		if e = fsutil.WriteJSONAtomic(p+".source-project", remote.Project, 0600); e != nil {
			return nil, e
		}
	}
	out.Folder, out.Binding = folder, &binding
	return out, nil
}
