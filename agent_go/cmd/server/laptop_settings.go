package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// crewLaptopSettings is one person's computer and folders for one project (owner, 27 Nov): each owner and co-owner keeps
// their own, a turn uses only its own person's, and nobody's chat reaches another person's computer.
type crewLaptopSettings struct {
	DeviceID string                       `json:"device_id"`
	Folders  []localfiles.FolderSelection `json:"folders"`
	// Commands lets the agent run terminal commands on the computer. It is separate from the folders' Read or
	// Read & write, which cover files only; a command also needs a Read & write folder.
	Commands bool `json:"commands"`
}

var laptopSettingsMu sync.Mutex

func laptopSettingsPath(owner, root string) (string, error) {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(owner + "\x00" + agentProfileRuntimeWorkspace(owner, root)))
	return filepath.Join(state, "crew-laptop", hex.EncodeToString(sum[:])+".json"), nil
}
func readLaptopSettings(owner, root string) (crewLaptopSettings, error) {
	file, err := laptopSettingsPath(owner, root)
	if err != nil {
		return crewLaptopSettings{}, err
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return crewLaptopSettings{Folders: []localfiles.FolderSelection{}}, nil
	}
	if err != nil {
		return crewLaptopSettings{}, err
	}
	var settings crewLaptopSettings
	err = json.Unmarshal(data, &settings)
	return settings, err
}
func (api *StreamingAPI) laptopSettingsRoot(w http.ResponseWriter, r *http.Request) (string, bool) {
	claims := GetUserFromContext(r.Context())
	if !websiteDeviceClaims(claims) || !userAccessForClaims(claims).CanEdit {
		externalError(w, 403, "forbidden", "Use your website login to manage your own project's laptop access.")
		return "", false
	}
	binding, _, ok := api.authorizeCrewProjectRead(r, mux.Vars(r)["project_id"])
	if !ok || !crewProjectOwnedByCaller(claims.UserID, agentProfileRuntimeWorkspace(claims.UserID, binding.WorkspacePath)) {
		externalError(w, 403, "forbidden", "Only a project owner can connect a laptop.")
		return "", false
	}
	return binding.WorkspacePath, true
}

func (api *StreamingAPI) handleCrewLaptopSettings(w http.ResponseWriter, r *http.Request) {
	root, ok := api.laptopSettingsRoot(w, r)
	if !ok {
		return
	}
	api.serveCrewLaptopSettings(w, r, root)
}

// Both website and MCP resolve ownership before using the same saved grants.
func (api *StreamingAPI) serveCrewLaptopSettings(w http.ResponseWriter, r *http.Request, root string) {
	claims := GetUserFromContext(r.Context())
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		settings, err := readLaptopSettings(claims.UserID, root)
		if err != nil {
			externalError(w, 500, "settings_unavailable", "Laptop settings could not be read.")
			return
		}
		externalJSON(w, settings)
		return
	}
	var settings crewLaptopSettings
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&settings) != nil || len(settings.Folders) > 32 || settings.DeviceID != "" && !localfiles.ValidID(settings.DeviceID) || settings.DeviceID == "" && len(settings.Folders) > 0 {
		externalError(w, 400, "invalid_settings", "Choose a connected computer and its folders.")
		return
	}
	seen := map[string]bool{}
	for i := range settings.Folders {
		folder := &settings.Folders[i]
		if folder.ID == "" {
			folder.ID = "folder-" + uuid.NewString()
		}
		if !localfiles.ValidID(folder.ID) || seen[folder.ID] || len(folder.Path) > 4096 || folder.Access != "read" && folder.Access != "write" {
			externalError(w, 400, "invalid_folder", "Choose Read or Read & write for each folder.")
			return
		}
		seen[folder.ID] = true
	}
	previous, err := readLaptopSettings(claims.UserID, root)
	if err != nil {
		externalError(w, 500, "settings_unavailable", "Laptop settings could not be read.")
		return
	}
	needsLaptop := false
	for _, folder := range settings.Folders {
		existing := false
		if settings.DeviceID == previous.DeviceID {
			for _, old := range previous.Folders {
				if folder.ID == old.ID && folder.Path == old.Path && (old.Access == "write" || folder.Access == "read") {
					existing = true
				}
			}
		}
		if !existing {
			needsLaptop = true
		}
	}
	// Reducing/removing grants also works while the computer is offline.

	if needsLaptop {
		if _, err := api.laptopSettingsDeviceCall(r.Context(), claims, settings.DeviceID, localfiles.Request{Operation: "validate_folders", CrewID: root, Folders: settings.Folders}); err != nil {
			externalError(w, 400, "folder_unavailable", err.Error())
			return
		}
	}
	file, err := laptopSettingsPath(claims.UserID, root)
	if err != nil {
		externalError(w, 500, "settings_unavailable", "Laptop settings could not be saved.")
		return
	}
	laptopSettingsMu.Lock()
	defer laptopSettingsMu.Unlock()
	data, _ := json.Marshal(settings)
	err = os.MkdirAll(filepath.Dir(file), 0700)
	if err == nil {
		err = os.WriteFile(file, data, 0600)
	}
	if err != nil {
		externalError(w, 500, "settings_unavailable", "Laptop settings could not be saved.")
		return
	}
	for _, old := range previous.Folders {
		log.Printf("[LAPTOP_GRANT] actor=%q crew=%q device=%q folder=%q access=%q change=removed", claims.UserID, root, previous.DeviceID, old.Path, old.Access)
	}
	for _, folder := range settings.Folders {
		log.Printf("[LAPTOP_GRANT] actor=%q crew=%q device=%q folder=%q access=%q change=saved", claims.UserID, root, settings.DeviceID, folder.Path, folder.Access)
	}
	externalJSON(w, settings)
}

func (api *StreamingAPI) handleLaptopBrowse(w http.ResponseWriter, r *http.Request) {
	root, ok := api.laptopSettingsRoot(w, r)
	if !ok {
		return
	}
	api.serveLaptopBrowse(w, r, root)
}

func (api *StreamingAPI) serveLaptopBrowse(w http.ResponseWriter, r *http.Request, root string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := contextWithLaptopTimeout(r)
	defer cancel()
	response, err := api.localDeviceCall(ctx, GetUserFromContext(r.Context()), r.URL.Query().Get("device_id"), localfiles.Request{CrewID: root, Operation: "browse_folders", Path: r.URL.Query().Get("path")})
	if err != nil {
		externalError(w, 400, "folder_unavailable", err.Error())
		return
	}
	externalJSON(w, response.Directory)
}

func currentLaptopFolder(owner, root, device, id string, write bool) ([]localfiles.FolderSelection, error) {
	settings, err := readLaptopSettings(owner, root)
	if err != nil || settings.DeviceID != device {
		return nil, fmt.Errorf("This laptop is no longer connected to this project. Check Settings → Laptop access.")
	}
	if id == "" && len(settings.Folders) == 1 {
		id = settings.Folders[0].ID
	}
	for _, folder := range settings.Folders {
		if folder.ID == id {
			if write && folder.Access != "write" {
				return nil, fmt.Errorf("Read access does not allow commands. Choose Read & write for this folder in project settings.")
			}
			return settings.Folders, nil
		}
	}
	return nil, fmt.Errorf("Choose a folder listed in this project's Laptop access settings")
}

// The browser cannot keep an unbounded request open while the laptop is offline.
func contextWithLaptopTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 20*time.Second)
}

// laptopFoldersPrompt lists, in the chat's instructions, the folders on the person's own computer that this turn can use
// (GitHub #4535: a turn had both linked folders and their tools, yet the agent said none were attached and offered to
// attach one, because only server folders were listed). Rebuilt every turn from the saved settings, so it always
// matches the panel; empty when the turn has no computer connection.
func laptopFoldersPrompt(claims *UserClaims, target *laptopConnectionTarget, root string) string {
	if claims == nil || target == nil || !target.valid() {
		return ""
	}
	settings, err := readLaptopSettings(claims.UserID, root)
	if err != nil || settings.DeviceID != target.DeviceID || len(settings.Folders) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Folders on the person's own computer, linked for this project (Folders on this Mac in the side panel):\n")
	write := false
	for _, folder := range settings.Folders {
		access := "Read"
		if folder.Access == "write" {
			access, write = "Read & write", true
		}
		fmt.Fprintf(&b, "- `%s` (%s)\n", folder.Path, access)
	}
	b.WriteString("Reach them only with these tools (find them with search_tools if they are not listed): list_local_files, copy_local_file_to_project")
	if write {
		b.WriteString(", write_local_file")
		if settings.Commands {
			b.WriteString(", execute_local_shell_command")
		}
	}
	b.WriteString(". Your file and shell tools work on the server, not on this computer. To add or change folders, use manage_laptop_access.\n")
	return b.String()
}
