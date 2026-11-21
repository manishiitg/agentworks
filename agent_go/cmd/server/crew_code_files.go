package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// The files in a workflow's code folder, for the Files tab: its main file, script.json, each task's Python file and
// any helper. Hidden files and caches are left out.

type crewCodeFile struct {
	Name  string `json:"name"`
	Size  int    `json:"size"`
	Lines int    `json:"lines"`
}

func crewCodeFileHidden(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || part == "__pycache__" {
			return true
		}
	}
	return false
}

// handleCrewCodeFiles lists the code folder of one workflow. Like the script itself, it is for the Crew's Builder.
func (api *StreamingAPI) handleCrewCodeFiles(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok {
		http.Error(w, "This Crew is unavailable.", http.StatusNotFound)
		return
	}
	resource := &crewWorkflowResources{userID: owner}
	source, err := resource.ResolveCrewScript(r.Context(), binding.Root, "")
	if err != nil {
		http.Error(w, "Could not read this workflow's code.", http.StatusBadRequest)
		return
	}
	files := []crewCodeFile{}
	for name, content := range source.Files {
		if crewCodeFileHidden(name) {
			continue
		}
		files = append(files, crewCodeFile{Name: name, Size: len(content), Lines: strings.Count(string(content), "\n") + 1})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"files": files})
}
