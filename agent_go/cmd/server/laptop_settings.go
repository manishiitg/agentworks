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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

type crewLaptopSettings struct {
	DeviceID string                       `json:"device_id"`
	Folders  []localfiles.FolderSelection `json:"folders"`
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
		externalError(w, 403, "forbidden", "Use your website login to manage your own Crew's laptop access.")
		return "", false
	}
	binding, _, ok := api.authorizeCrewProjectRead(r, mux.Vars(r)["project_id"])
	if !ok || !crewProjectOwnedByCallerStrict(claims.UserID, agentProfileRuntimeWorkspace(claims.UserID, binding.WorkspacePath)) {
		externalError(w, 403, "forbidden", "Only this Crew's owner can connect their laptop.")
		return "", false
	}
	return binding.WorkspacePath, true
}

func (api *StreamingAPI) handleCrewLaptopSettings(w http.ResponseWriter, r *http.Request) {
	root, ok := api.laptopSettingsRoot(w, r)
	if !ok {
		return
	}
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
		if _, err := api.localDeviceCall(r.Context(), claims, settings.DeviceID, localfiles.Request{Operation: "validate_folders", CrewID: root, Folders: settings.Folders}); err != nil {
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
		log.Printf("[LAPTOP_GRANT] user=%q crew=%q device=%q folder=%q access=%q change=removed", claims.UserID, root, previous.DeviceID, old.Path, old.Access)
	}
	for _, folder := range settings.Folders {
		log.Printf("[LAPTOP_GRANT] user=%q crew=%q device=%q folder=%q access=%q change=saved", claims.UserID, root, settings.DeviceID, folder.Path, folder.Access)
	}
	externalJSON(w, settings)
}

func (api *StreamingAPI) handleLaptopBrowse(w http.ResponseWriter, r *http.Request) {
	if _, ok := api.laptopSettingsRoot(w, r); !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := contextWithLaptopTimeout(r)
	defer cancel()
	response, err := api.localDeviceCall(ctx, GetUserFromContext(r.Context()), r.URL.Query().Get("device_id"), localfiles.Request{Operation: "browse_folders", Path: r.URL.Query().Get("path")})
	if err != nil {
		externalError(w, 400, "folder_unavailable", err.Error())
		return
	}
	externalJSON(w, response.Directory)
}

func currentLaptopFolder(owner, root, device, id string, write bool) ([]localfiles.FolderSelection, error) {
	settings, err := readLaptopSettings(owner, root)
	if err != nil || settings.DeviceID != device {
		return nil, fmt.Errorf("This laptop is no longer connected to this Crew. Check Settings → Laptop access.")
	}
	if id == "" && len(settings.Folders) == 1 {
		id = settings.Folders[0].ID
	}
	for _, folder := range settings.Folders {
		if folder.ID == id {
			if write && folder.Access != "write" {
				return nil, fmt.Errorf("Read access does not allow commands. Choose Read & write for this folder in Crew Settings.")
			}
			return settings.Folders, nil
		}
	}
	return nil, fmt.Errorf("Choose a folder listed in this Crew's Laptop access settings")
}

// The browser cannot keep an unbounded request open while the laptop is offline.
func contextWithLaptopTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 20*time.Second)
}
