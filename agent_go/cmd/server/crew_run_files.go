package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A run keeps its own bookkeeping next to what the script made. These files are the platform's, not the run's output.
var crewRunBookkeeping = map[string]bool{"inputs.json": true, "run_metadata.json": true, "webhook_progress.json": true}

type crewRunFile struct {
	Name string `json:"name"`
	// Path is the workspace path a person can open, such as Workflow/<id>/runs/<run>/report.md.
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// listCrewRunFiles lists the plain files a script run made directly in its run folder (a report, for example).
// Folders, hidden files, links and the platform's own bookkeeping files are left out. The run folder must be a
// single name inside this workflow's own runs folder.
func listCrewRunFiles(binding crewExecutableBinding, runFolder string) ([]crewRunFile, error) {
	if runFolder == "" || runFolder == "." || runFolder == ".." || strings.HasPrefix(runFolder, ".") || strings.ContainsAny(runFolder, "/\\") {
		return nil, fmt.Errorf("choose a run folder")
	}
	layout, found, err := crewroot.LookupExecutable(getWorkspaceDocsAbsPath(), binding.Root)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("this workflow's folders are unavailable")
	}
	runsRoot := filepath.Join(layout.Runtime, "runs")
	dir := filepath.Join(runsRoot, runFolder)
	if !crewroot.Within(runsRoot, dir) || dir == runsRoot {
		return nil, fmt.Errorf("choose a run folder")
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []crewRunFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	files := []crewRunFile{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || strings.HasPrefix(name, ".") || crewRunBookkeeping[name] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, crewRunFile{Name: name, Path: path.Join(binding.Root, "runs", runFolder, name), Size: info.Size()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

// handleCrewRunFiles lists the files one run made. Like the script's source, it is for the Crew's Builder.
func (api *StreamingAPI) handleCrewRunFiles(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	files, err := listCrewRunFiles(*binding, r.URL.Query().Get("run_folder"))
	if err != nil {
		http.Error(w, "Could not list this run's files.", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"files": files})
}
