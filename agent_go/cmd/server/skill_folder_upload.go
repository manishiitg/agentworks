package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// A folder selected by the person is uploaded as files, not a host filesystem
// path. The existing workspace importer owns destination access and staging.
func skillFolderUpload(importFiles bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 12*1024*1024)
		if err := r.ParseMultipartForm(12 * 1024 * 1024); err != nil {
			http.Error(w, "Choose a skill folder smaller than 10 MB.", 400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		var names []string
		if json.Unmarshal([]byte(r.FormValue("paths")), &names) != nil || len(names) == 0 || len(names) > 256 {
			http.Error(w, "Choose one skill folder with at most 256 files.", 400)
			return
		}
		files := map[string][]byte{}
		total := 0
		base := ""
		for _, name := range names {
			if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
				http.Error(w, "Invalid skill file path.", 400)
				return
			}
			if path.Base(name) == "SKILL.md" {
				if base != "" {
					http.Error(w, "Choose one skill folder, not a library of skills.", 400)
					return
				}
				base = path.Dir(name) + "/"
			}
		}
		if base == "" || base == "./" {
			http.Error(w, "Choose a folder with SKILL.md at its root.", 400)
			return
		}
		for i, name := range names {
			if !strings.HasPrefix(name, base) {
				http.Error(w, "All files must belong to the chosen skill folder.", 400)
				return
			}
			rel := strings.TrimPrefix(name, base)
			if _, exists := files[rel]; exists {
				http.Error(w, "Duplicate skill file path.", 400)
				return
			}
			file, _, err := r.FormFile(fmt.Sprintf("file%d", i))
			if err != nil {
				http.Error(w, "A selected file is missing. Choose the folder again.", 400)
				return
			}
			data, err := io.ReadAll(io.LimitReader(file, 10*1024*1024+1))
			file.Close()
			if err != nil {
				http.Error(w, "A selected file could not be read.", 400)
				return
			}
			total += len(data)
			if total > 10*1024*1024 {
				http.Error(w, "Skill folder exceeds 10 MB.", 400)
				return
			}
			files[rel] = data
		}
		front, _, err := skills.ValidateSkillContent(string(files["SKILL.md"]))
		if err != nil {
			http.Error(w, "This folder needs a valid SKILL.md: "+err.Error(), 400)
			return
		}
		if importFiles {
			if err := skills.InstallSkillFilesIn(getWorkspaceAPIURL(), r.URL.Query().Get("workspace_path"), front.Name, files); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			writeUsersJSON(w, 200, map[string]any{"success": true, "skill_name": front.Name})
			return
		}
		writeUsersJSON(w, 200, map[string]any{"name": front.Name, "description": front.Description, "document": string(files["SKILL.md"]), "files": names})
	}
}
