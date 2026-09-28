package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// systemSkill defines a skill that should be auto-installed on startup
type systemSkill struct {
	Source string // CLI source (owner/repo@skill)
	Name   string // Expected skill folder name after install
}

// getSystemSkills returns the list of skills to auto-install on startup.
// Add new required skills here.
func getSystemSkills() []systemSkill {
	return []systemSkill{
		{Source: "anthropics/skills@skill-creator", Name: "skill-creator"},
	}
}

// syncSystemSkillsOnStartup installs missing system skills directly to the filesystem.
// Runs inside Docker where npx and the skills/ folder are directly accessible.
func syncSystemSkillsOnStartup(docsDir string) {
	skillsDir := filepath.Join(docsDir, "skills")

	// Check if npx is available
	npxPath, err := exec.LookPath("npx")
	if err != nil {
		fmt.Printf("⚠️  npx not found — skipping system skills sync\n")
		return
	}

	var toInstall []systemSkill
	for _, ss := range getSystemSkills() {
		skillMdPath := filepath.Join(skillsDir, ss.Name, "SKILL.md")
		if _, err := os.Stat(skillMdPath); err == nil {
			continue // Already exists
		}
		toInstall = append(toInstall, ss)
	}

	if len(toInstall) == 0 {
		fmt.Printf("✅ All %d system skills already installed\n", len(getSystemSkills()))
		return
	}

	fmt.Printf("📦 Installing %d missing system skill(s)...\n", len(toInstall))

	installed := 0
	for _, ss := range toInstall {
		if err := installSkillToDir(npxPath, ss.Source, skillsDir); err != nil {
			fmt.Printf("⚠️  Failed to install %s: %v\n", ss.Name, err)
			continue
		}
		// Verify it was installed
		skillMdPath := filepath.Join(skillsDir, ss.Name, "SKILL.md")
		if _, err := os.Stat(skillMdPath); err == nil {
			installed++
			fmt.Printf("  ✅ Installed %s\n", ss.Name)
		}
	}

	fmt.Printf("📦 System skills sync complete: %d/%d installed\n", installed, len(toInstall))
}

// installSkillToDir runs npx skills add to install a skill directly into the target directory.
func installSkillToDir(npxPath, source, skillsDir string) error {
	// Parse source — handle owner/repo@skill-name format
	cliSource := source
	skillFilter := "*"
	if atIdx := strings.LastIndex(source, "@"); atIdx > 0 {
		beforeAt := source[:atIdx]
		afterAt := source[atIdx+1:]
		if strings.Contains(beforeAt, "/") && !strings.Contains(beforeAt, ":") && afterAt != "" {
			cliSource = beforeAt
			skillFilter = afterAt
		}
	}

	// Create temp dir for CLI operations
	tempDir, err := os.MkdirTemp("", "skills-sync-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Run npx skills add
	cmd := exec.Command(npxPath, "skills", "add", cliSource,
		"--agent", "universal",
		"--skill", skillFilter,
		"--copy",
		"-y",
	)
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npx skills add failed: %w\nOutput: %s", err, string(output))
	}

	// Copy from .agents/skills/ to target skillsDir
	agentsSkillsDir := filepath.Join(tempDir, ".agents", "skills")
	entries, err := os.ReadDir(agentsSkillsDir)
	if err != nil {
		return fmt.Errorf("no skills output found: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		srcDir := filepath.Join(agentsSkillsDir, entry.Name())
		destDir := filepath.Join(skillsDir, entry.Name())

		// Remove existing and copy fresh
		os.RemoveAll(destDir)
		if err := copyDir(srcDir, destDir); err != nil {
			return fmt.Errorf("failed to copy skill %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			content, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, content, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

// --- HTTP Handlers for skill CLI routes (called by backend) ---

// handleSkillCLIAvailable checks if npx/skills CLI is available
func handleSkillCLIAvailable(c *gin.Context) {
	_, err := exec.LookPath("npx")
	c.JSON(http.StatusOK, gin.H{"available": err == nil})
}

// skillInstallRequest is the request body for POST /api/skills/cli/install
type skillInstallRequest struct {
	Source string `json:"source"` // owner/repo@skill-name
	// TargetDir, when set, installs into one project's skills folder
	// (docs-relative _users/<user>/Chats/<Product>/projects/<project>/skills)
	// instead of the account-wide skills/ library, e.g. a private Code.
	TargetDir string `json:"target_dir,omitempty"`
}

// projectSkillsDirPattern is the only shape a TargetDir may have.
var projectSkillsDirPattern = regexp.MustCompile(`^_users/[A-Za-z0-9._@-]+/Chats/[A-Za-z]+/projects/[A-Za-z0-9._-]+/skills$`)

// skillInstallTargetDir resolves the folder an install writes to: the
// account-wide library, or a validated project skills folder.
func skillInstallTargetDir(docsDir, target string) (string, error) {
	target = strings.Trim(filepath.ToSlash(strings.TrimSpace(target)), "/")
	if target == "" {
		return filepath.Join(docsDir, "skills"), nil
	}
	return projectSkillsDir(docsDir, target)
}

// projectSkillsDir validates a project skills folder and returns it, created
// if missing. The project must exist, and no path component below the docs
// root may be a symlink: an agent may create links inside its own project
// (the sandbox allows it), and following one would install into, or delete
// from, another user's tree.
func projectSkillsDir(docsDir, target string) (string, error) {
	target = strings.Trim(filepath.ToSlash(strings.TrimSpace(target)), "/")
	if strings.Contains(target, "..") || !projectSkillsDirPattern.MatchString(target) {
		return "", fmt.Errorf("target_dir must be a project's skills folder")
	}
	dir := filepath.Join(docsDir, filepath.FromSlash(target))
	project := filepath.Dir(dir)
	if err := refuseSymlinkComponents(docsDir, project); err != nil {
		return "", err
	}
	if info, err := os.Lstat(project); err != nil || !info.IsDir() {
		return "", fmt.Errorf("target_dir's project does not exist")
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create project skills folder: %w", err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("target_dir must be a real folder, not a link")
	}
	return dir, nil
}

// refuseSymlinkComponents fails when any existing component of dir below
// root is a symlink. Missing components are fine: they are not links.
func refuseSymlinkComponents(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target_dir escapes the workspace")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target_dir must not pass through a link")
		}
	}
	return nil
}

// installSkillFolder copies src into dir/name. The copy is made in a fresh,
// unpredictably named folder beside the target and renamed into place, so a
// link planted at dir/name is never written through: renaming a folder onto
// a link fails instead.
func installSkillFolder(src, dir, name string) error {
	staging, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := copyDir(src, staging); err != nil {
		return err
	}
	dest := filepath.Join(dir, name)
	if info, err := os.Lstat(dest); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link; remove it first", name)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
	}
	return os.Rename(staging, dest)
}

// projectSkillDeleteRequest is POST /api/skills/project/delete.
type projectSkillDeleteRequest struct {
	TargetDir string `json:"target_dir"`
	Name      string `json:"name"`
}

// handleProjectSkillDelete removes one skill from a project's skills folder
// (a private Code's), refusing any path that passes through a link. A link
// named like the skill is removed itself, never followed.
func handleProjectSkillDelete(c *gin.Context) {
	var req projectSkillDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid skill name"})
		return
	}
	dir, err := projectSkillsDir(viper.GetString("docs-dir"), req.TargetDir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		err = os.Remove(path)
	} else {
		err = os.RemoveAll(path)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// skillInstallResponse is the response from install
type skillInstallResponse struct {
	InstalledSkills []string             `json:"installed_skills"`
	LockEntries     map[string]lockEntry `json:"lock_entries,omitempty"`
	Errors          []string             `json:"errors,omitempty"`
}

// lockEntry matches skills-lock.json entry format
type lockEntry struct {
	Source       string `json:"source"`
	SourceType   string `json:"sourceType"`
	ComputedHash string `json:"computedHash"`
}

// handleSkillInstall installs a skill via npx skills add
func handleSkillInstall(c *gin.Context) {
	var req skillInstallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Source == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source is required"})
		return
	}

	npxPath, err := exec.LookPath("npx")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "npx not available"})
		return
	}

	docsDir := viper.GetString("docs-dir")
	skillsDir, err := skillInstallTargetDir(docsDir, req.TargetDir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Parse source
	cliSource := req.Source
	skillFilter := "*"
	if atIdx := strings.LastIndex(req.Source, "@"); atIdx > 0 {
		beforeAt := req.Source[:atIdx]
		afterAt := req.Source[atIdx+1:]
		if strings.Contains(beforeAt, "/") && !strings.Contains(beforeAt, ":") && afterAt != "" {
			cliSource = beforeAt
			skillFilter = afterAt
		}
	}

	// Create temp dir
	tempDir, err := os.MkdirTemp("", "skills-install-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp dir"})
		return
	}
	defer os.RemoveAll(tempDir)

	// Run npx skills add
	cmd := exec.Command(npxPath, "skills", "add", cliSource,
		"--agent", "universal",
		"--skill", skillFilter,
		"--copy", "-y",
	)
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  fmt.Sprintf("npx skills add failed: %v", err),
			"output": string(output),
		})
		return
	}

	result := skillInstallResponse{
		LockEntries: make(map[string]lockEntry),
	}

	// Copy installed skills from temp .agents/skills/ to workspace skills/
	agentsSkillsDir := filepath.Join(tempDir, ".agents", "skills")
	entries, err := os.ReadDir(agentsSkillsDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillMdPath := filepath.Join(agentsSkillsDir, entry.Name(), "SKILL.md")
			if _, statErr := os.Stat(skillMdPath); statErr != nil {
				continue
			}

			srcDir := filepath.Join(agentsSkillsDir, entry.Name())
			var copyErr error
			if strings.TrimSpace(req.TargetDir) != "" {
				// A project folder is writable by its agent: never follow a link.
				copyErr = installSkillFolder(srcDir, skillsDir, entry.Name())
			} else {
				destDir := filepath.Join(skillsDir, entry.Name())
				os.RemoveAll(destDir)
				copyErr = copyDir(srcDir, destDir)
			}
			if copyErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to copy %s: %v", entry.Name(), copyErr))
				continue
			}
			result.InstalledSkills = append(result.InstalledSkills, entry.Name())
		}
	}

	// Read and sync lock file
	lockPath := filepath.Join(tempDir, "skills-lock.json")
	if lockData, readErr := os.ReadFile(lockPath); readErr == nil {
		var lockFile struct {
			Skills map[string]lockEntry `json:"skills"`
		}
		if json.Unmarshal(lockData, &lockFile) == nil {
			result.LockEntries = lockFile.Skills
			// The account-wide lock file tracks the shared library only.
			if strings.TrimSpace(req.TargetDir) == "" {
				syncLockFile(docsDir, lockFile.Skills)
			}
		}
	}

	c.JSON(http.StatusOK, result)
}

// searchResult represents a skill found via search
type searchResult struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Skill    string `json:"skill"`
	URL      string `json:"url"`
	Installs string `json:"installs"`
}

// handleSkillSearch searches the skills registry
func handleSkillSearch(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
		return
	}

	npxPath, err := exec.LookPath("npx")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "npx not available"})
		return
	}

	cmd := exec.Command(npxPath, "skills", "find", query)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("search failed: %v", err)})
		return
	}

	results := parseSkillSearchOutput(string(output))
	c.JSON(http.StatusOK, results)
}

// parseSkillSearchOutput parses npx skills find output
func parseSkillSearchOutput(output string) []searchResult {
	var results []searchResult
	lines := strings.Split(output, "\n")

	for i := 0; i < len(lines); i++ {
		line := stripANSICodes(lines[i])
		line = strings.TrimSpace(line)

		if strings.Contains(line, "@") && strings.Contains(line, "installs") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				fullName := parts[0]
				installs := strings.Join(parts[1:], " ")

				atIdx := strings.LastIndex(fullName, "@")
				if atIdx > 0 {
					r := searchResult{
						Name:     fullName,
						Source:   fullName[:atIdx],
						Skill:    fullName[atIdx+1:],
						Installs: installs,
					}

					if i+1 < len(lines) {
						nextLine := stripANSICodes(lines[i+1])
						nextLine = strings.TrimSpace(nextLine)
						nextLine = strings.TrimPrefix(nextLine, "└ ")
						if strings.HasPrefix(nextLine, "https://") {
							r.URL = nextLine
							i++
						}
					}
					results = append(results, r)
				}
			}
		}
	}
	return results
}

// stripANSICodes removes ANSI escape codes from a string
func stripANSICodes(s string) string {
	result := s
	for strings.Contains(result, "\x1b[") {
		start := strings.Index(result, "\x1b[")
		end := start + 2
		for end < len(result) && result[end] != 'm' {
			end++
		}
		if end < len(result) {
			result = result[:start] + result[end+1:]
		} else {
			break
		}
	}
	return result
}

// syncLockFile merges new entries into the workspace's skills-lock.json
func syncLockFile(docsDir string, newEntries map[string]lockEntry) {
	lockPath := filepath.Join(docsDir, "skills-lock.json")

	existing := map[string]lockEntry{}
	if data, err := os.ReadFile(lockPath); err == nil {
		var lockFile struct {
			Version int                  `json:"version"`
			Skills  map[string]lockEntry `json:"skills"`
		}
		if json.Unmarshal(data, &lockFile) == nil && lockFile.Skills != nil {
			existing = lockFile.Skills
		}
	}

	for name, entry := range newEntries {
		existing[name] = entry
	}

	lockFile := map[string]interface{}{
		"version": 1,
		"skills":  existing,
	}
	data, _ := json.MarshalIndent(lockFile, "", "  ")
	os.WriteFile(lockPath, data, 0644)
}
