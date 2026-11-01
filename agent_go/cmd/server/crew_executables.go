package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowkb"

	"github.com/google/uuid"
)

// Crew executable identities live outside editable workspace files. Engine roots
// stay canonical until the separate, one-off data-layout conversion is tested.
// PLAT-746-265: a display name or manifest owner is never an execution authority.
type crewExecutableBinding struct {
	ID       string `json:"id"`
	CrewID   string `json:"crew_id"`
	CrewRoot string `json:"crew_root"`
	Root     string `json:"workspace_path"`
	Kind     string `json:"kind"`
}

var crewExecutablesMu sync.Mutex

func crewExecutablesPath() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "ownership", "crew-executables"), nil
}

func crewExecutableWorkspace(raw string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")))
	if docs := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH")); docs != "" && filepath.IsAbs(clean) {
		if rel, err := filepath.Rel(docs, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			clean = filepath.ToSlash(rel)
		}
	}
	return path.Clean(strings.Trim(clean, "/"))
}

func crewExecutableKey(raw string) string { return workflowOwnerKey(crewExecutableWorkspace(raw)) }

func crewExecutableFile(root string) (string, error) {
	dir, err := crewExecutablesPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(root)))), nil
}

func readCrewExecutables() (map[string]crewExecutableBinding, error) {
	dir, err := crewExecutablesPath()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	result := map[string]crewExecutableBinding{}
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		var binding crewExecutableBinding
		if err == nil {
			err = json.Unmarshal(raw, &binding)
		}
		if err != nil {
			log.Printf("[CREW_EXECUTABLE] unavailable record=%s reason=%v", entry.Name(), err)
			continue
		}
		expected, _ := crewExecutableFile(binding.Root)
		if binding.Root != crewExecutableKey(binding.Root) || filepath.Base(expected) != entry.Name() {
			continue
		}
		result[binding.Root] = binding
	}
	return result, nil
}

func findCrewExecutable(rawRoot string) (*crewExecutableBinding, error) {
	workspace := crewExecutableWorkspace(rawRoot)
	if strings.HasPrefix(workspace, "Workflow/.relay_releases/") {
		draft, err := relayDraftWorkspaceForRelease(context.Background(), workspace)
		if err != nil {
			return nil, err
		}
		workspace = draft
	}
	root := crewExecutableKey(workspace)
	if root == "" {
		return nil, nil
	}
	file, err := crewExecutableFile(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read executable binding: %w", err)
	}
	var binding crewExecutableBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return nil, fmt.Errorf("read executable binding: %w", err)
	}
	if binding.Root != root || binding.ID == "" || binding.CrewID == "" || (binding.Kind != "workflow" && binding.Kind != "relay") {
		return nil, fmt.Errorf("invalid executable binding")
	}
	owner, found := crewProjectOwnerID(binding.CrewRoot)
	if !found || owner == "" {
		return nil, fmt.Errorf("owning Crew is unavailable")
	}
	return &binding, nil
}

func registerCrewExecutable(binding crewExecutableBinding) error {
	crewExecutablesMu.Lock()
	defer crewExecutablesMu.Unlock()
	binding.Root = crewExecutableKey(binding.Root)
	if binding.Root == "" {
		return fmt.Errorf("invalid executable root")
	}
	file, err := crewExecutableFile(binding.Root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(file); err == nil {
		return fmt.Errorf("executable already registered")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	return writeRegistryFileAtomically(file, data)
}

// All actions use the existing Crew resolver, including its co-owner and token
// checks. A Run person cannot author executables through this Builder endpoint.
func (api *StreamingAPI) handleCrewExecutables(w http.ResponseWriter, r *http.Request) {
	if api.projectActions == nil || api.projectActions.registry == nil {
		http.Error(w, "Crew services are unavailable", http.StatusServiceUnavailable)
		return
	}
	caller := GetUserIDFromContext(r.Context())
	profile, err := api.projectActions.registry.Resolve("work", 0, caller)
	if err != nil {
		http.Error(w, "Crew is unavailable", http.StatusNotFound)
		return
	}
	project, err := resolveCrewProjectBinding(r.Context(), caller, profile, r.URL.Query().Get("crew_id"), "")
	if err != nil || !project.OwnedByCaller {
		http.Error(w, "Only this Crew's owners can manage its workflows and programs.", http.StatusForbidden)
		return
	}
	root := project.Binding.WorkspacePath
	if r.Method == http.MethodGet {
		records, err := readCrewExecutables()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type entry struct {
			crewExecutableBinding
			Manifest *WorkflowManifest `json:"manifest"`
		}
		entries := []entry{}
		for _, binding := range records {
			if binding.CrewRoot != root {
				continue
			}
			manifest, found, err := ReadWorkflowManifest(r.Context(), binding.Root)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if found {
				entries = append(entries, entry{binding, manifest})
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Manifest.Label < entries[j].Manifest.Label })
		w.Header().Set("Content-Type", "application/json")
		agents, err := crewExecutableAgentChoices(r.Context(), project.OwnerID, project.Binding.ResourceID, root)
		if err != nil {
			http.Error(w, "Could not load this Crew's agents", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"executables": entries, "agents": agents})
		return
	}
	var input struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "Enter a name and choose Workflow or Program.", http.StatusBadRequest)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 || (input.Kind != "workflow" && input.Kind != "relay") {
		http.Error(w, "Enter a name (up to 120 characters) and choose Workflow or Program.", http.StatusBadRequest)
		return
	}
	id := uuid.NewString()
	binding := crewExecutableBinding{ID: id, CrewID: project.Binding.ResourceID, CrewRoot: root, Root: "Workflow/" + id, Kind: input.Kind}
	manifest := NewWorkflowManifest(input.Name)
	manifest.ID = id
	// The existing engine represents an ordinary workflow with an omitted kind.
	if input.Kind == "relay" {
		manifest.Kind = "relay"
	}
	manifest.CreatedBy = project.OwnerID
	manifest.Access = &WorkflowAccess{Owners: []string{project.OwnerID}, Readers: []string{}}
	manifest.Capabilities.LLMConfig = productDefaultWorkflowLLMConfig(r.Context())
	if input.Kind == "relay" {
		manifest.RelayRuntime = "python"
		manifest.RelayDurability = "dbos"
		if err := initializePythonRelayWorkspaceWithSource(r.Context(), binding.Root, defaultNativeDBOSRelaySource); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := registerCrewExecutable(binding); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	registerWorkflowOwner(binding.Root, project.OwnerID)
	if err := WriteWorkflowManifest(r.Context(), binding.Root, manifest); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(binding)
}

// PLAT-746-265: KB authorization also reads manifests directly from disk.
// Resolve both consumers and sources through the same trusted parent audience.
func init() { workflowkb.ManifestAuthority = crewExecutableKnowledgeAudience }

func crewExecutableKnowledgeAudience(root string, manifest workflowkb.Manifest) (workflowkb.Manifest, error) {
	binding, err := findCrewExecutable(root)
	if err != nil || binding == nil {
		return manifest, err
	}
	if manifest.ID != binding.ID {
		return manifest, fmt.Errorf("knowledge executable identity does not match its Crew")
	}
	id, ok := projectIdentityOf(binding.CrewRoot)
	if !ok {
		return manifest, fmt.Errorf("knowledge Crew is unavailable")
	}
	record, found := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !found || record.OwnerID == "" {
		return manifest, fmt.Errorf("knowledge Crew owner is unavailable")
	}
	manifest.CreatedBy = record.OwnerID
	// Keep source-specific writer opt-in; only audience/ownership are inherited.
	if manifest.Access == nil {
		manifest.Access = &struct {
			Owners           []string `json:"owners"`
			Readers          []string `json:"readers"`
			AllowedKBWriters []string `json:"allowed_kb_writers"`
		}{}
	}
	manifest.Access.Owners = append([]string{record.OwnerID}, record.CoOwners...)
	manifest.Access.Readers = nil
	if projectSharingEnabled() {
		manifest.Access.Readers = append([]string{}, record.RunPeople...)
	}
	manifest.Everyone = crewSharedWithOthers(binding.CrewRoot)
	return manifest, nil
}
