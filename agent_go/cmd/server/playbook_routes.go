package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

type playbookCatalogItem struct {
	MetadataVersion      int                      `json:"metadata_version"`
	ContentSchema        string                   `json:"content_schema"`
	ID                   string                   `json:"id"`
	Version              string                   `json:"version"`
	Changelog            []playbookChangelogEntry `json:"changelog,omitempty"`
	Title                string                   `json:"title"`
	Description          string                   `json:"description"`
	Hierarchy            []string                 `json:"hierarchy"`
	Order                int                      `json:"order"`
	Entrypoint           string                   `json:"entrypoint"`
	Audience             string                   `json:"audience"`
	TeamScope            string                   `json:"team_scope"`
	SetupPrompt          string                   `json:"setup_prompt"`
	SetupInputs          []map[string]interface{} `json:"setup_inputs"`
	RequiredCapabilities []string                 `json:"required_capabilities"`
	RecommendedTools     []map[string]interface{} `json:"recommended_tools"`
	PulseFocus           []map[string]interface{} `json:"pulse_focus"`
	Outputs              []string                 `json:"outputs"`
	AgentSlots           []playbookAgentSlot      `json:"agent_slots,omitempty"`
	Handoffs             []playbookHandoff        `json:"handoffs,omitempty"`
	SetupChecks          []string                 `json:"setup_checks,omitempty"`
	Category             string                   `json:"category"`
	SourceDir            string                   `json:"-"`
}

type playbookAgentSlot struct {
	ID              string   `json:"id"`
	AgentPlaybookID string   `json:"agent_playbook_id"`
	Accepts         []string `json:"accepts,omitempty"`
	Required        bool     `json:"required"`
	Output          string   `json:"output"`
}

type playbookHandoff struct {
	ID           string `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	ArtifactType string `json:"artifact_type"`
	Required     bool   `json:"required"`
}

type playbookChangelogEntry struct {
	Version string `json:"version"`
	Summary string `json:"summary"`
}

// Playbooks may specialize Strategic Review, where the useful question depends
// on the customer's goals and domain. Technical and Architecture Review keep
// their canonical evidence- and structure-driven scopes. Filtering here also
// makes older catalog manifests harmless without breaking reads.
func strategicPlaybookFocus(focuses []map[string]interface{}) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, 1)
	for _, focus := range focuses {
		if module, _ := focus["module"].(string); module == pulseModuleStrategicReview {
			filtered = append(filtered, focus)
		}
	}
	return filtered
}

func playbooksRoot() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("AGENTWORKS_PLAYBOOKS_DIR")); configured != "" {
		if info, err := os.Stat(configured); err == nil && info.IsDir() {
			return configured, nil
		}
		return "", fmt.Errorf("AGENTWORKS_PLAYBOOKS_DIR is not a readable directory")
	}
	_, sourceFile, _, _ := runtime.Caller(0)
	candidates := []string{
		"playbooks",
		filepath.Join("..", "playbooks"),
		filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "playbooks"),
		filepath.Join(filepath.Dir(os.Args[0]), "playbooks"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("playbook catalog not found; set AGENTWORKS_PLAYBOOKS_DIR")
}

func loadPlaybookCatalog() ([]playbookCatalogItem, error) {
	root, err := playbooksRoot()
	if err != nil {
		return nil, err
	}
	items := make([]playbookCatalogItem, 0)
	err = filepath.WalkDir(filepath.Join(root, "agentic-engineering-platform"), func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "playbook.json" {
			return nil
		}
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		var item playbookCatalogItem
		if jsonErr := json.Unmarshal(content, &item); jsonErr != nil {
			return fmt.Errorf("parse %s: %w", filePath, jsonErr)
		}
		if item.ID == "" || item.Entrypoint == "" {
			return fmt.Errorf("invalid playbook manifest %s", filePath)
		}
		item.SourceDir = filepath.Dir(filePath)
		item.Category = playbookCategory(item.Hierarchy)
		item.PulseFocus = strategicPlaybookFocus(item.PulseFocus)
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Order < items[j].Order
	})
	return items, nil
}

func playbookCategory(hierarchy []string) string {
	for index, value := range hierarchy {
		if value == "Agentic Engineering Platform" && index+1 < len(hierarchy) {
			return hierarchy[index+1]
		}
	}
	if len(hierarchy) > 0 {
		return hierarchy[len(hierarchy)-1]
	}
	return "Other"
}

func findPlaybook(id string) (*playbookCatalogItem, error) {
	items, err := loadPlaybookCatalog()
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].ID == id {
			return &items[index], nil
		}
	}
	return nil, os.ErrNotExist
}

func installedBuilderSkillNames(installed []InstalledPlaybook) []string {
	var names []string
	for _, playbook := range installed {
		if playbook.Status != "disabled" && strings.TrimSpace(playbook.SkillName) != "" {
			names = appendUniqueStrings(names, playbook.SkillName)
		}
	}
	return names
}

func (api *StreamingAPI) handleListPlaybooks(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	items, err := loadPlaybookCatalog()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "playbooks": items, "total": len(items)})
}

func (api *StreamingAPI) handleListInstalledPlaybooks(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if workspacePath == "" {
		http.Error(w, "workspace_path parameter is required", http.StatusBadRequest)
		return
	}
	manifest, exists, err := ReadWorkflowManifest(r.Context(), workspacePath)
	if err != nil || !exists {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}
	if workflowAccessForManifest(GetUserFromContext(r.Context()), manifest) == WorkflowAccessNone {
		writeWorkflowPermissionDenied(w, "read")
		return
	}
	installed := manifest.InstalledPlaybooks
	if installed == nil {
		installed = []InstalledPlaybook{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "installed": installed})
}

type installPlaybookRequest struct {
	WorkspacePath string `json:"workspace_path"`
	PlaybookID    string `json:"playbook_id"`
}

func (api *StreamingAPI) handleInstallPlaybook(w http.ResponseWriter, r *http.Request) {
	setCORS(w)
	var request installPlaybookRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	request.WorkspacePath = strings.TrimSpace(request.WorkspacePath)
	request.PlaybookID = strings.TrimSpace(request.PlaybookID)
	if request.WorkspacePath == "" || request.PlaybookID == "" {
		http.Error(w, "workspace_path and playbook_id are required", http.StatusBadRequest)
		return
	}
	item, err := findPlaybook(request.PlaybookID)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "playbook not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	manifest, exists, err := ReadWorkflowManifest(r.Context(), request.WorkspacePath)
	if err != nil || !exists {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}
	if !requireWorkflowOwner(w, r, request.WorkspacePath) {
		return
	}
	skillName := "agentworks-playbook-" + item.ID
	if _, err := skills.ValidateSkillName(skillName); err != nil {
		http.Error(w, "playbook has an invalid skill name", http.StatusInternalServerError)
		return
	}
	hash, err := installPlaybookSkill(r.Context(), request.WorkspacePath, skillName, item)
	if err != nil {
		http.Error(w, fmt.Sprintf("install playbook skill: %v", err), http.StatusInternalServerError)
		return
	}
	installed := InstalledPlaybook{
		ID: item.ID, Title: item.Title, Version: item.Version, Category: item.Category,
		SkillName: skillName, SourceHash: hash, Status: "draft", InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	replaced := false
	for index := range manifest.InstalledPlaybooks {
		if manifest.InstalledPlaybooks[index].ID == installed.ID {
			manifest.InstalledPlaybooks[index] = installed
			replaced = true
			break
		}
	}
	if !replaced {
		manifest.InstalledPlaybooks = append(manifest.InstalledPlaybooks, installed)
	}
	if err := WriteWorkflowManifest(r.Context(), request.WorkspacePath, manifest); err != nil {
		http.Error(w, fmt.Sprintf("save installed playbook: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "installed": installed})
}

func installPlaybookSkill(ctx context.Context, workspacePath, skillName string, item *playbookCatalogItem) (string, error) {
	client := skills.NewWorkspaceAPIClient(getWorkspaceAPIURL())
	destinationRoot := path.Join(workspacePath, skills.SkillsBasePath, skillName)
	if err := createPlaybookWorkspaceFolder(client, destinationRoot); err != nil {
		return "", err
	}
	hasher := sha256.New()
	externalReferences := map[string]string{}
	err := filepath.WalkDir(item.SourceDir, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(item.SourceDir, sourcePath)
		if relErr != nil || relative == "." {
			return relErr
		}
		destination := path.Join(destinationRoot, filepath.ToSlash(relative))
		if entry.IsDir() {
			return createPlaybookWorkspaceFolder(client, destination)
		}
		content, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			return readErr
		}
		if strings.ContainsRune(string(content), '\x00') {
			return nil
		}
		if strings.EqualFold(filepath.Ext(sourcePath), ".md") {
			content = []byte(rewriteExternalPlaybookLinks(string(content), sourcePath, item.SourceDir, externalReferences))
		}
		_, _ = hasher.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hasher.Write(content)
		if relative == "SETUP.json" && len(item.SetupChecks) > 0 {
			return installPlaybookSetupFile(ctx, client, workspacePath, destination, item, content)
		}
		return client.WriteFile(destination, string(content))
	})
	if err != nil {
		return "", err
	}
	referencePaths := make([]string, 0, len(externalReferences))
	for destination := range externalReferences {
		referencePaths = append(referencePaths, destination)
	}
	sort.Strings(referencePaths)
	for _, relative := range referencePaths {
		sourcePath := externalReferences[relative]
		content, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			return "", readErr
		}
		destination := path.Join(destinationRoot, relative)
		if err := createPlaybookWorkspaceFolder(client, path.Dir(destination)); err != nil {
			return "", err
		}
		_, _ = hasher.Write([]byte(relative))
		_, _ = hasher.Write(content)
		if err := client.WriteFile(destination, string(content)); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// Setup progress belongs to the customer. A repeated installation keeps it;
// an update retains the old record before starting the new version's checks.
func installPlaybookSetupFile(ctx context.Context, client *skills.WorkspaceAPIClient, workspacePath, destination string, item *playbookCatalogItem, content []byte) error {
	current, found, err := readFileFromWorkspace(ctx, destination)
	if err != nil {
		return fmt.Errorf("read existing playbook setup: %w", err)
	}
	if found {
		var existing struct {
			ID      string `json:"playbook_id"`
			Version string `json:"playbook_version"`
		}
		if json.Unmarshal([]byte(current), &existing) == nil && existing.ID == item.ID && existing.Version == item.Version {
			return nil
		}
		digest := sha256.Sum256([]byte(current))
		historyPath := path.Join(workspacePath, "playbooks", "setup-history", item.ID, hex.EncodeToString(digest[:])+".json")
		if err := createPlaybookWorkspaceFolder(client, path.Dir(historyPath)); err != nil {
			return err
		}
		if err := client.WriteFile(historyPath, current); err != nil {
			return fmt.Errorf("preserve previous playbook setup: %w", err)
		}
		var next map[string]interface{}
		if err := json.Unmarshal(content, &next); err != nil {
			return err
		}
		next["previous_setup_path"] = historyPath
		content, err = json.MarshalIndent(next, "", "  ")
		if err != nil {
			return err
		}
	}
	return client.WriteFile(destination, string(content))
}

var markdownLinkPattern = regexp.MustCompile(`\]\(([^)#]+)(#[^)]*)?\)`)

// rewriteExternalPlaybookLinks keeps an installed skill self-contained. Source
// packages may share references at a category level (../references/...). The
// installer copies those files under references/shared/ and rewrites the link
// from the installed file's location.
func rewriteExternalPlaybookLinks(content, sourcePath, sourceRoot string, external map[string]string) string {
	return markdownLinkPattern.ReplaceAllStringFunc(content, func(match string) string {
		parts := markdownLinkPattern.FindStringSubmatch(match)
		if len(parts) < 2 || strings.Contains(parts[1], "://") || strings.HasPrefix(parts[1], "/") {
			return match
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(parts[1])))
		relToSource, err := filepath.Rel(sourceRoot, resolved)
		if err != nil || (relToSource != ".." && !strings.HasPrefix(relToSource, ".."+string(filepath.Separator))) {
			return match
		}
		if info, statErr := os.Stat(resolved); statErr != nil || info.IsDir() {
			return match
		}
		playbookRoot, rootErr := playbooksRoot()
		if rootErr != nil {
			return match
		}
		catalogRoot := filepath.Join(playbookRoot, "agentic-engineering-platform")
		relToCatalog, relErr := filepath.Rel(filepath.Clean(catalogRoot), resolved)
		if relErr != nil || strings.HasPrefix(relToCatalog, "..") {
			return match
		}
		externalRelative := path.Join("references", "shared", filepath.ToSlash(relToCatalog))
		external[externalRelative] = resolved
		destinationFileRelative, fileRelErr := filepath.Rel(sourceRoot, sourcePath)
		if fileRelErr != nil {
			return match
		}
		rewritten, rewriteErr := filepath.Rel(filepath.Dir(destinationFileRelative), filepath.FromSlash(externalRelative))
		if rewriteErr != nil {
			return match
		}
		return "](" + filepath.ToSlash(rewritten) + parts[2] + ")"
	})
}

func createPlaybookWorkspaceFolder(client *skills.WorkspaceAPIClient, folder string) error {
	if err := client.CreateFolder(folder); err != nil && !strings.Contains(strings.ToLower(err.Error()), "exist") {
		return err
	}
	return nil
}
