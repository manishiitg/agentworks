package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
	"github.com/spf13/viper"
)

// One inventory for platform/API agents and native CLIs. Priority is stable:
// canonical installs win over provider projections with the same folder name.
var workspaceSkillRoots = []string{"skills", ".skills", ".agents/skills", ".claude/skills", ".cursor/skills", ".codex/skills", ".gemini/skills", ".pi/skills"}
var workspaceSkillsMu sync.Mutex

type workspaceSkillRequest struct {
	WorkspacePath string `json:"workspace_path"`
	Name          string `json:"name,omitempty"`
}
type workspaceSkillDocument struct {
	Name     string   `json:"folder_name"`
	FilePath string   `json:"file_path"`
	Content  string   `json:"document"`
	Managed  bool     `json:"managed"`
	UsedBy   []string `json:"used_by"`
	// A skill installed from Brain records where it came from (.brain-source.json). BrainSource is that record; BrainEdited
	// says a file in the folder differs from what was installed.
	BrainSource string `json:"brain_source,omitempty"`
	BrainEdited bool   `json:"brain_edited,omitempty"`
	Source      string `json:"source,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
}

// skillWorkspace is where one workspace keeps its skills. A Crew bound in the
// server's registry keeps them under its resource root (.agentworks/ of a
// laptop folder) and its settings in private storage, the same split the file
// tools use; everything else keeps both in its docs folder.
type skillWorkspace struct {
	dir    string // skill installs, skills-lock.json
	config string // workflow.json and planning/
	native string // a laptop Crew's own folder: native CLIs also find provider skills there
}

func skillWorkspaceDir(docs, workspace string) (string, error) {
	w, err := skillWorkspaceRoots(docs, workspace)
	return w.dir, err
}

func skillWorkspaceRoots(docs, workspace string) (skillWorkspace, error) {
	ref, ok := workspaceref.Parse(workspace)
	if !ok || ref.String() != workspace || strings.Contains(workspace, "..") {
		return skillWorkspace{}, fmt.Errorf("invalid workspace_path")
	}
	logical := ref.Logical()
	if !(strings.HasPrefix(logical, "Workflow/") || strings.HasPrefix(logical, "Crew/") || (ref.HasOwner() && (logical == "Chats" || strings.HasPrefix(logical, "Chats/")))) {
		return skillWorkspace{}, fmt.Errorf("skills require a project or workflow workspace")
	}
	if roots, bound, err := crewroot.Lookup(docs, workspace); bound || err != nil {
		if err != nil {
			return skillWorkspace{}, err
		}
		if workspace != roots.Virtual {
			return skillWorkspace{}, fmt.Errorf("skills require the Crew's root workspace")
		}
		w := skillWorkspace{dir: roots.Resources, config: roots.Private}
		if roots.Local {
			w.native = roots.Files
		}
		return w, nil
	}
	dir := filepath.Join(docs, filepath.FromSlash(workspace))
	if err := refuseSymlinkComponents(docs, dir); err != nil {
		return skillWorkspace{}, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return skillWorkspace{}, fmt.Errorf("workspace does not exist")
	}
	return skillWorkspace{dir: dir, config: dir}, nil
}

// Keep unknown configuration fields when removing an attachment.
func skillConfig(dir, rel string) (map[string]interface{}, error) {
	file := filepath.Join(dir, filepath.FromSlash(rel))
	if err := refuseSymlinkComponents(dir, file); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config map[string]interface{}
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return config, nil
}
func skillNames(value interface{}) []string {
	var names []string
	if values, ok := value.([]interface{}); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				names = append(names, name)
			}
		}
	}
	return names
}
func skillUsage(manifest, steps map[string]interface{}) map[string][]string {
	used := map[string][]string{}
	add := func(value interface{}, label string) {
		for _, name := range skillNames(value) {
			used[name] = append(used[name], label)
		}
	}
	if caps, ok := manifest["capabilities"].(map[string]interface{}); ok {
		add(caps["selected_skills"], "Main chat")
	}
	if rows, ok := steps["steps"].([]interface{}); ok {
		for _, value := range rows {
			row, _ := value.(map[string]interface{})
			cfg, _ := row["agent_configs"].(map[string]interface{})
			title, _ := row["title"].(string)
			if title == "" {
				title, _ = row["id"].(string)
			}
			add(cfg["enabled_skills"], "Step: "+title)
		}
	}
	return used
}
func safeSkillName(name string) bool {
	return name != "" && name != "." && !strings.Contains(name, "..") && !strings.ContainsAny(name, "/\\\x00")
}
func skillLocations(dir, name string) []string {
	var found []string
	for _, root := range workspaceSkillRoots {
		folder := filepath.Join(dir, filepath.FromSlash(root), name)
		if refuseSymlinkComponents(dir, filepath.Join(folder, "SKILL.md")) != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(folder, "SKILL.md")); err == nil && info.Mode().IsRegular() {
			found = append(found, folder)
		}
	}
	return found
}

type skillLocation struct{ base, folder, path string }

// locations lists one skill's copies, resource root first. A laptop Crew's own
// folder adds only the native provider folders (.claude/skills and the like)
// its CLI already reads, reported under the issued _files path.
func (w skillWorkspace) locations(name string) []skillLocation {
	var found []skillLocation
	for _, folder := range skillLocations(w.dir, name) {
		rel, _ := filepath.Rel(w.dir, folder)
		found = append(found, skillLocation{w.dir, folder, filepath.ToSlash(rel)})
	}
	if w.native != "" {
		for _, root := range nativeSkillRoots() {
			folder := filepath.Join(w.native, filepath.FromSlash(root), name)
			if refuseSymlinkComponents(w.native, filepath.Join(folder, "SKILL.md")) != nil {
				continue
			}
			if info, err := os.Stat(filepath.Join(folder, "SKILL.md")); err == nil && info.Mode().IsRegular() {
				found = append(found, skillLocation{w.native, folder, crewroot.FilesSegment + "/" + root + "/" + name})
			}
		}
	}
	return found
}

func nativeSkillRoots() []string {
	var roots []string
	for _, root := range workspaceSkillRoots {
		if strings.HasSuffix(root, "/skills") {
			roots = append(roots, root)
		}
	}
	return roots
}

var errLegacySkillLink = errors.New("legacy skill contains a link")

// A one-time compatibility migration copies only saved attachments, including
// step attachments, with binary assets intact. The old store is never an
// inventory or runtime fallback. Keep it on disk for other, unmigrated projects.
func migrateWorkspaceSkills(docs, workspace, dir, state string, manifest, steps map[string]interface{}) error {
	marker := filepath.Join(state, ".workspace-skills-v1")
	if _, err := os.Lstat(marker); err == nil {
		return nil
	}
	used := skillUsage(manifest, steps)
	ref := workspaceref.MustParse(workspace)
	owner := ref.Owner()
	// Legacy shared workflows used the root store, not another account's folder.
	bases := []string{}
	if owner != "" && projectSkillsOwnerPattern.MatchString(owner) {
		bases = append(bases, filepath.Join(docs, filepath.FromSlash(workspaceref.PhysicalPathOf(owner, "skills"))))
	}
	bases = append(bases, filepath.Join(docs, "skills"))
	for name := range used {
		hasOwnedSource := false
		for _, folder := range skillLocations(dir, name) {
			if _, err := os.Lstat(filepath.Join(folder, ".agentworks-managed")); os.IsNotExist(err) {
				hasOwnedSource = true
				break
			}
		}
		if !safeSkillName(name) || hasOwnedSource {
			continue
		}
		for _, base := range bases {
			source := filepath.Join(base, name)
			if refuseSymlinkComponents(docs, source) != nil {
				continue
			}
			if info, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
				continue
			}
			// Validate the entire source tree before copying: copyDir follows file links. A legacy skill that holds a
			// link is skipped, not fatal: failing here failed every skill call for the workspace, including the
			// runtime's, on every call, and the step silently ran without its skills.
			if err := filepath.WalkDir(source, func(_ string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return errLegacySkillLink
				}
				return nil
			}); err != nil {
				if errors.Is(err, errLegacySkillLink) {
					log.Printf("[SKILLS] Not migrating legacy skill %q into %s: it contains a link", name, workspace)
					break
				}
				return err
			}
			target := filepath.Join(dir, "skills")
			if err := refuseSymlinkComponents(dir, target); err != nil {
				return err
			}
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			if err := installSkillFolder(source, target, name); err != nil {
				return err
			}
			break
		}
	}
	return os.WriteFile(marker, []byte("Workspace skills migrated.\n"), 0644)
}

func workspaceSkillState(req workspaceSkillRequest) (skillWorkspace, map[string]interface{}, map[string]interface{}, error) {
	docs := viper.GetString("docs-dir")
	w, err := skillWorkspaceRoots(docs, req.WorkspacePath)
	if err != nil {
		return w, nil, nil, err
	}
	manifest, err := skillConfig(w.config, "workflow.json")
	if err != nil {
		return w, nil, nil, err
	}
	steps, err := skillConfig(w.config, "planning/step_config.json")
	if err != nil {
		return w, nil, nil, err
	}
	if err := migrateWorkspaceSkills(docs, req.WorkspacePath, w.dir, w.config, manifest, steps); err != nil {
		return w, nil, nil, err
	}
	return w, manifest, steps, nil
}
func handleWorkspaceSkillList(c *gin.Context) {
	var req workspaceSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	workspaceSkillsMu.Lock()
	defer workspaceSkillsMu.Unlock()
	w, manifest, steps, err := workspaceSkillState(req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	dir := w.dir
	used := skillUsage(manifest, steps)
	// Report provenance only when recorded by the installer. Directory names
	// and the skill's own prose do not establish who installed a file.
	lock, _ := skillConfig(dir, "skills-lock.json")
	lockSkills, _ := lock["skills"].(map[string]interface{})
	names := map[string]bool{}
	scan := func(top string, roots []string) {
		for _, root := range roots {
			base := filepath.Join(top, filepath.FromSlash(root))
			if refuseSymlinkComponents(top, base) != nil {
				continue
			}
			entries, _ := os.ReadDir(base)
			for _, entry := range entries {
				if entry.IsDir() && safeSkillName(entry.Name()) {
					names[entry.Name()] = true
				}
			}
		}
	}
	scan(dir, workspaceSkillRoots)
	if w.native != "" {
		scan(w.native, nativeSkillRoots())
	}
	documents := []workspaceSkillDocument{}
	for name := range names {
		locations := w.locations(name)
		if len(locations) == 0 {
			continue
		}
		folder := locations[0].folder
		file := filepath.Join(folder, "SKILL.md")
		if err := refuseSymlinkComponents(locations[0].base, file); err != nil {
			continue
		}
		content, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		_, markerErr := os.Lstat(filepath.Join(folder, ".agentworks-managed"))
		doc := workspaceSkillDocument{Name: name, FilePath: locations[0].path + "/SKILL.md", Content: string(content), Managed: markerErr == nil, UsedBy: used[name]}
		doc.BrainSource, doc.BrainEdited = brainSkillSource(folder)
		if entry, ok := lockSkills[name].(map[string]interface{}); ok {
			doc.Source, _ = entry["source"].(string)
			doc.SourceType, _ = entry["sourceType"].(string)
		}
		documents = append(documents, doc)
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].Name < documents[j].Name })
	c.JSON(http.StatusOK, gin.H{"documents": documents, "usage": used})
}

func removeSkillReference(config map[string]interface{}, field, name string) bool {
	names := skillNames(config[field])
	kept := []string{}
	for _, value := range names {
		if value != name {
			kept = append(kept, value)
		}
	}
	if len(kept) == len(names) {
		return false
	}
	config[field] = kept
	return true
}
func saveSkillConfig(dir, rel string, config map[string]interface{}) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	dest := filepath.Join(dir, filepath.FromSlash(rel))
	if err := refuseSymlinkComponents(dir, dest); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(dest), ".skills-config-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(temp.Name(), dest)
}
func handleWorkspaceSkillDelete(c *gin.Context) {
	var req workspaceSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil || !safeSkillName(req.Name) {
		c.JSON(400, gin.H{"error": "invalid skill request"})
		return
	}
	workspaceSkillsMu.Lock()
	defer workspaceSkillsMu.Unlock()
	w, manifest, steps, err := workspaceSkillState(req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	// Uninstall removes only the workspace's own copies, never files in a
	// laptop Crew's project folder.
	dir := w.dir
	locations := skillLocations(dir, req.Name)
	// Provider projections with no owned source are managed by the platform.
	if len(locations) > 0 {
		if _, err := os.Lstat(filepath.Join(locations[0], ".agentworks-managed")); err == nil {
			c.JSON(409, gin.H{"error": "This skill is managed by the platform."})
			return
		}
	}
	lock, err := skillConfig(dir, "skills-lock.json")
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if entries, ok := lock["skills"].(map[string]interface{}); ok {
		if _, exists := entries[req.Name]; exists {
			delete(entries, req.Name)
			if err := saveSkillConfig(dir, "skills-lock.json", lock); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
		}
	}
	if caps, ok := manifest["capabilities"].(map[string]interface{}); ok && removeSkillReference(caps, "selected_skills", req.Name) {
		if err := saveSkillConfig(w.config, "workflow.json", manifest); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	changed := false
	if rows, ok := steps["steps"].([]interface{}); ok {
		for _, value := range rows {
			row, _ := value.(map[string]interface{})
			cfg, _ := row["agent_configs"].(map[string]interface{})
			if removeSkillReference(cfg, "enabled_skills", req.Name) {
				changed = true
			}
		}
	}
	if changed {
		if err := saveSkillConfig(w.config, "planning/step_config.json", steps); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	for _, folder := range locations {
		if err := os.RemoveAll(folder); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(200, gin.H{"success": true})
}

// Imports are staged in a new folder; provider-writable links are never used
// as write destinations. JSON []byte keeps binary assets intact over HTTP.
func handleWorkspaceSkillImport(c *gin.Context) {
	var req struct {
		WorkspacePath string            `json:"workspace_path"`
		Name          string            `json:"name"`
		Files         map[string][]byte `json:"files"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !safeSkillName(req.Name) || len(req.Files["SKILL.md"]) == 0 {
		c.JSON(400, gin.H{"error": "invalid skill import"})
		return
	}
	size := 0
	for name, data := range req.Files {
		if name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, "\\\x00") || strings.Contains(name, "..") || filepath.ToSlash(filepath.Clean(name)) != name {
			c.JSON(400, gin.H{"error": "invalid skill file path"})
			return
		}
		size += len(data)
	}
	if size > 20*1024*1024 {
		c.JSON(400, gin.H{"error": "skill exceeds 20 MiB"})
		return
	}
	workspaceSkillsMu.Lock()
	defer workspaceSkillsMu.Unlock()
	w, _, _, err := workspaceSkillState(workspaceSkillRequest{WorkspacePath: req.WorkspacePath})
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	dir := w.dir
	target := filepath.Join(dir, "skills")
	if err := refuseSymlinkComponents(dir, target); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	temp, err := os.MkdirTemp(target, ".import-")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer os.RemoveAll(temp)
	for name, data := range req.Files {
		dest := filepath.Join(temp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	if err := installSkillFolder(temp, target, req.Name); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"success": true})
}

func handleWorkspaceSkillFiles(c *gin.Context) {
	var req workspaceSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil || !safeSkillName(req.Name) {
		c.JSON(400, gin.H{"error": "invalid skill request"})
		return
	}
	workspaceSkillsMu.Lock()
	defer workspaceSkillsMu.Unlock()
	w, _, _, err := workspaceSkillState(req)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	locations := w.locations(req.Name)
	if len(locations) == 0 {
		c.JSON(404, gin.H{"error": "skill not found"})
		return
	}
	dir := locations[0].base
	var files []struct {
		RelPath string
		Content []byte
	}
	root := locations[0].folder
	total := int64(0)
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Only the root guide is attached separately. Nested SKILL.md files
		// are reference documents and must remain readable in the bundle.
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || file == filepath.Join(root, "SKILL.md") || entry.Name() == ".agentworks-managed" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 4*1024*1024 || total+info.Size() > 20*1024*1024 {
			return nil
		}
		if err := refuseSymlinkComponents(dir, file); err != nil {
			return err
		}
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, file)
		files = append(files, struct {
			RelPath string
			Content []byte
		}{filepath.ToSlash(relative), content})
		total += info.Size()
		return nil
	})
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"files": files})
}

const brainSkillMarker = ".brain-source.json"

// brainSkillSource reads the record written when a skill was installed from Brain and reports whether any installed file
// has changed since (a changed, added or missing file counts).
func brainSkillSource(folder string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(folder, brainSkillMarker))
	if err != nil || len(raw) > 64*1024 {
		return "", false
	}
	var marker struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(raw, &marker) != nil {
		return "", false
	}
	edited := false
	seen := map[string]bool{}
	_ = filepath.WalkDir(folder, func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || entry.Name() == brainSkillMarker || entry.Name() == ".agentworks-managed" {
			return nil
		}
		rel, _ := filepath.Rel(folder, file)
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		data, err := os.ReadFile(file)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		if marker.Files[rel] != hex.EncodeToString(sum[:]) {
			edited = true
		}
		return nil
	})
	for rel := range marker.Files {
		if !seen[rel] {
			edited = true
		}
	}
	return string(raw), edited
}
