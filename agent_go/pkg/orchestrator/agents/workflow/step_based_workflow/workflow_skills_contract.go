package step_based_workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const WorkflowSkillsContractVersion = "1.0.51"

type workflowSkillsFilesKey struct{}
type workflowSkillsFiles struct {
	read  func(context.Context, string) (string, error)
	write func(context.Context, string, string) error
}

// WithWorkflowSkillsFiles supplies the existing platform's staged, anchored
// authoring transport. Strings hold raw bytes in memory, never a JSON asset body.
func WithWorkflowSkillsFiles(ctx context.Context, read func(context.Context, string) (string, error), write func(context.Context, string, string) error) context.Context {
	return context.WithValue(ctx, workflowSkillsFilesKey{}, workflowSkillsFiles{read: read, write: write})
}

type WorkflowSkillsMigrationFile struct {
	Source string `json:"source"`
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type WorkflowSkillsMigration struct {
	Version             string                            `json:"version"`
	Folder              string                            `json:"folder,omitempty"`
	Files               []WorkflowSkillsMigrationFile     `json:"files"`
	IndexSHA256         string                            `json:"index_sha256,omitempty"`
	StepAccess          map[string]string                 `json:"retired_step_access"`
	RetiredStepSettings map[string]map[string]interface{} `json:"retired_step_settings,omitempty"`
}

func UsesWorkflowSkills(version string) bool {
	var major, minor, patch int
	if n, _ := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); n != 3 {
		return false
	}
	return major > 1 || major == 1 && (minor > 0 || minor == 0 && patch >= 51)
}

func skillsMigrationHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func skillsMigrationDocument(raw string) (map[string]interface{}, error) {
	d := map[string]interface{}{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("workflow document is invalid JSON")
	}
	if err := decoder.Decode(&d); err != nil {
		return nil, err
	}
	if d == nil {
		return nil, fmt.Errorf("workflow document must be an object")
	}
	return d, nil
}

func skillsMigrationProse(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".mdx", ".txt", ".rst":
		return true
	}
	return false
}

// Keep old code and platform ledgers in place. Only guides and their ordinary
// supporting bundles become skill references, never a second scripted source.
func skillsMigrationIncludes(relative string) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	name := parts[len(parts)-1]
	if (len(parts) == 2 && name == "main.py") || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "_metadata.json") || name == "_freshness.json" {
		return false
	}
	if skillsMigrationProse(name) {
		return true
	}
	for _, part := range parts[:len(parts)-1] {
		if part == "assets" || part == "references" || parts[0] == "_global" && part == "scripts" {
			return true
		}
	}
	return false
}

func collectWorkflowSkillSources(root *os.Root, readSource func(string) ([]byte, error)) ([]WorkflowSkillsMigrationFile, error) {
	var files []WorkflowSkillsMigrationFile
	err := fs.WalkDir(root.FS(), "learnings", func(p string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && p == "learnings" {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel("learnings", p)
		if err != nil {
			return err
		}
		if !skillsMigrationIncludes(rel) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("guide %s is a linked file; save it as an ordinary workflow file before migrating", rel)
		}
		var raw []byte
		if readSource != nil {
			raw, err = readSource(p)
		} else {
			raw, err = root.ReadFile(p)
		}
		if err != nil {
			return err
		}
		files = append(files, WorkflowSkillsMigrationFile{Source: filepath.ToSlash(filepath.Join("learnings", rel)), Target: filepath.ToSlash(filepath.Join("references", rel)), SHA256: skillsMigrationHash(raw), Bytes: int64(len(raw))})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Source < files[j].Source })
	return files, err
}

func workflowSkillsIndex(folder string, files []WorkflowSkillsMigrationFile, titles map[string]string) string {
	var text strings.Builder
	fmt.Fprintf(&text, "---\nname: %s\ndescription: Reusable task guides for this workflow. Open only the reference needed for the current task.\n---\n# Workflow how-to\n\nOpen the relevant guide, rather than loading all references.\n", folder)
	for _, f := range files {
		if !skillsMigrationProse(f.Source) {
			continue
		}
		parts := strings.Split(f.Source, "/")
		origin := "Shared workflow guide"
		if len(parts) > 2 && parts[1] != "_global" {
			origin = "Step " + parts[1]
			if title := titles[parts[1]]; title != "" {
				origin += " (" + strings.ReplaceAll(title, "\n", " ") + ")"
			}
		}
		fmt.Fprintf(&text, "- %s: `%s`\n", origin, f.Target)
	}
	return text.String()
}

func migrationAppendSkill(object map[string]interface{}, field, folder string) {
	if folder == "" {
		return
	}
	values, _ := object[field].([]interface{})
	for _, value := range values {
		if value == folder {
			return
		}
	}
	object[field] = append(values, folder)
}

// Repoint live references as part of the staged migration. The stamp verifies
// persisted copies. Audit notes retain their wording outside runtime prompts.
func rewriteWorkflowSkillReferences(value interface{}, migration *WorkflowSkillsMigration) {
	var walk func(interface{})
	rewrite := func(text string) string {
		for _, file := range migration.Files {
			text = strings.ReplaceAll(text, file.Source, "skills/"+migration.Folder+"/"+file.Target)
		}
		return text
	}
	walk = func(value interface{}) {
		switch current := value.(type) {
		case []interface{}:
			for i, child := range current {
				if text, ok := child.(string); ok {
					current[i] = rewrite(text)
				} else {
					walk(child)
				}
			}
		case map[string]interface{}:
			for key, child := range current {
				if key == "goal_agent_migration" {
					continue
				}
				if text, ok := child.(string); ok && (key == "description" || key == "message" || key == "system_prompt") {
					current[key] = rewrite(text)
				} else if key == "context_dependencies" || key == "additional_read_paths" {
					walk(child)
				} else if _, ok := child.(string); !ok {
					walk(child)
				}
			}
			if typ, _ := current["type"].(string); typ == "agent" {
				if description, ok := current["description"].(string); ok {
					lines := strings.Split(description, "\n")
					inResources := false
					for i, line := range lines {
						trimmed := strings.TrimSpace(line)
						if heading := descriptionSectionHeading.FindStringSubmatch(trimmed); heading != nil {
							level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
							if level <= 2 {
								inResources = level == 2 && strings.EqualFold(strings.TrimSpace(heading[1]), "Resources")
							}
						}
						if !inResources {
							continue
						}
						label, use, ok := resourceIntentLine(line)
						if !ok || (label != "learnings" && label != "workflow learnings" && label != "learning") {
							continue
						}
						note, _ := current["goal_agent_migration"].(map[string]interface{})
						if note == nil {
							note = map[string]interface{}{}
							current["goal_agent_migration"] = note
						}
						note["retired_learning_resource"] = line
						lines[i] = "- Skills: reads task-specific how-to guides"
						if statesNoUse(use) {
							lines[i] = "- Skills: none"
						}
					}
					current["description"] = strings.Join(lines, "\n")
				}
			}
		}
	}
	walk(value)
}

func migrateWorkflowSkillSelections(plan, config map[string]interface{}, migration *WorkflowSkillsMigration) map[string]string {
	configs, _ := config["steps"].([]interface{})
	byID := map[string]map[string]interface{}{}
	for _, raw := range configs {
		if sc, ok := raw.(map[string]interface{}); ok {
			id, _ := sc["id"].(string)
			byID[id] = sc
		}
	}
	titles := map[string]string{}
	var walk func(interface{})
	walk = func(value interface{}) {
		switch current := value.(type) {
		case []interface{}:
			for _, child := range current {
				walk(child)
			}
		case map[string]interface{}:
			id, _ := current["id"].(string)
			typ, _ := current["type"].(string)
			if id != "" && (typ == "agent" || typ == "scripted") {
				titles[id], _ = current["title"].(string)
				sc := byID[id]
				ac := map[string]interface{}{}
				if sc != nil {
					if saved, ok := sc["agent_configs"].(map[string]interface{}); ok {
						ac = saved
					}
				}
				access, recorded := migration.StepAccess[id]
				if !recorded {
					access, _ = ac["learnings_access"].(string)
					migration.StepAccess[id] = access
				}
				if migration.Folder != "" && access != "none" {
					if sc == nil {
						sc = map[string]interface{}{"id": id}
						configs = append(configs, sc)
						byID[id] = sc
					}
					migrationAppendSkill(ac, "enabled_skills", migration.Folder)
				}
				delete(ac, "learnings_access")
				for _, key := range []string{"lock_learnings", "lock_learnings_reason", "disable_learning", "learning_detail_level", "learning_after_loop_iteration", "keep_learning_full"} {
					if value, present := ac[key]; present {
						if migration.RetiredStepSettings == nil {
							migration.RetiredStepSettings = map[string]map[string]interface{}{}
						}
						if migration.RetiredStepSettings[id] == nil {
							migration.RetiredStepSettings[id] = map[string]interface{}{}
						}
						migration.RetiredStepSettings[id][key] = value
						delete(ac, key)
					}
				}
				if sc != nil {
					sc["agent_configs"] = ac
				}
			}
			for key, child := range current {
				if key != "goal_agent_migration" {
					walk(child)
				}
			}
		}
	}
	walk(plan)
	config["steps"] = configs
	return titles
}

// The caller is the existing owner/editor-checked native plan mutation, with
// the workflow lock and normal collision checks. Sources and copies stay under
// that same workflow; no user-level/shared skill directory is written.
func createMigrateWorkflowSkillsExecutor(workspacePath string, logger loggerv2.Logger, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, _ map[string]interface{}) (string, error) {
		transport, ok := ctx.Value(workflowSkillsFilesKey{}).(workflowSkillsFiles)
		if !ok {
			return "", fmt.Errorf("Migrate guides from the workflow Builder or MCP plan tool so the complete edit uses the platform's authoring checks")
		}
		readFile := transport.read
		var preparationError error
		writeFile := func(c context.Context, p, raw string) error {
			err := transport.write(c, p, raw)
			if err != nil {
				preparationError = err
			}
			return err
		}
		planFileMutex.Lock()
		defer planFileMutex.Unlock()
		docsRoot, err := os.OpenRoot(GetPromptDocsRoot())
		if err != nil {
			return "", err
		}
		defer docsRoot.Close()
		root, err := wf.OpenDirectory(docsRoot, filepath.ToSlash(workspacePath), false)
		if err != nil {
			return "", err
		}
		defer root.Close()
		manifestPath := normalizePathForWorkspaceAPI("workflow.json", workspacePath)
		planPath := normalizePathForWorkspaceAPI("planning/plan.json", workspacePath)
		configPath := normalizePathForWorkspaceAPI("planning/step_config.json", workspacePath)
		manifestRaw, err := readFile(ctx, manifestPath)
		if err != nil {
			return "", err
		}
		manifest, err := skillsMigrationDocument(manifestRaw)
		if err != nil {
			return "", err
		}
		if version, _ := manifest["version"].(string); UsesWorkflowSkills(version) {
			return `{"status":"already_migrated"}`, nil
		}
		var layout int
		fmt.Sscan(fmt.Sprint(manifest["code_layout_version"]), &layout)
		if layout != 1 {
			return "", fmt.Errorf("finish the existing code-layout upgrade to code_layout_version=1 before migrating workflow guides")
		}
		planRaw, err := readFile(ctx, planPath)
		if err != nil {
			return "", err
		}
		plan, err := skillsMigrationDocument(planRaw)
		if err != nil {
			return "", err
		}
		configRaw, readErr := readFile(ctx, configPath)
		configRaw, err = goalAgentConfigForMigration(configRaw, readErr)
		if err != nil {
			return "", err
		}
		config, err := skillsMigrationDocument(configRaw)
		if err != nil {
			return "", err
		}
		if _, err = ParseStepConfigContent(configRaw); err != nil {
			return "", err
		}
		files, err := collectWorkflowSkillSources(root, func(p string) ([]byte, error) {
			raw, e := readFile(ctx, normalizePathForWorkspaceAPI(p, workspacePath))
			return []byte(raw), e
		})
		if err != nil {
			return "", err
		}
		migration := WorkflowSkillsMigration{Version: WorkflowSkillsContractVersion, Files: files, StepAccess: map[string]string{}}
		var previous WorkflowSkillsMigration
		if saved, ok := manifest["workflow_skills_migration"]; ok {
			raw, _ := json.Marshal(saved)
			if err = json.Unmarshal(raw, &previous); err != nil {
				return "", err
			}
			migration.Folder = previous.Folder
			migration.RetiredStepSettings = previous.RetiredStepSettings
			if previous.StepAccess != nil {
				migration.StepAccess = previous.StepAccess
			}
		}
		if len(files) > 0 && migration.Folder == "" {
			for n := 1; ; n++ {
				migration.Folder = "workflow-how-to"
				if n > 1 {
					migration.Folder = fmt.Sprintf("workflow-how-to-%d", n)
				}
				if _, err = root.Lstat(filepath.Join("skills", migration.Folder)); os.IsNotExist(err) {
					break
				} else if err != nil {
					return "", err
				}
			}
		}
		if migration.Folder != "" {
			if _, err = skills.ValidateSkillName(migration.Folder); err != nil {
				return "", err
			}
		}
		if previous.Folder != "" {
			indexPath := normalizePathForWorkspaceAPI(filepath.Join("skills", migration.Folder, "SKILL.md"), workspacePath)
			if index, e := readFile(ctx, indexPath); e == nil {
				if previous.IndexSHA256 == "" || skillsMigrationHash([]byte(index)) != previous.IndexSHA256 {
					return "", fmt.Errorf("the previous migration folder is now a different or edited skill; ask the Builder to reconcile its index before retrying")
				}
			} else if !workspacepkg.IsFileNotFound(e) {
				return "", e
			}
		}
		titles := migrateWorkflowSkillSelections(plan, config, &migration)
		writeManifest := func() error {
			manifest["workflow_skills_migration"] = migration
			raw, e := json.MarshalIndent(manifest, "", "  ")
			if e != nil {
				return e
			}
			return writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, manifestPath), manifestPath, string(raw))
		}
		// Save the chosen folder before the first copy, so an interrupted first
		// migration retries the same private bundle instead of making duplicates.
		if previous.Version == "" {
			if err = writeManifest(); err != nil {
				return "", err
			}
		}
		oldHashes := map[string]string{}
		for _, file := range previous.Files {
			oldHashes[file.Target] = file.SHA256
		}
		for _, file := range files {
			source := normalizePathForWorkspaceAPI(file.Source, workspacePath)
			target := normalizePathForWorkspaceAPI(filepath.Join("skills", migration.Folder, filepath.FromSlash(file.Target)), workspacePath)
			sourceText, e := readFile(ctx, source)
			raw := []byte(sourceText)
			if e != nil {
				return "", e
			}
			if skillsMigrationHash(raw) != file.SHA256 {
				return "", fmt.Errorf("guide changed during migration; retry after the edit finishes: %s", file.Source)
			}
			if existingText, e := readFile(ctx, target); e == nil {
				existing := []byte(existingText)
				if bytes.Equal(existing, raw) {
					continue
				}
				if skillsMigrationHash(existing) != oldHashes[file.Target] {
					return "", fmt.Errorf("skill reference was edited separately; reconcile it with %s before retrying", file.Source)
				}
			} else if !workspacepkg.IsFileNotFound(e) {
				return "", e
			}
			if e = writeFile(ctx, target, string(raw)); e != nil {
				return "", e
			}

		}
		if migration.Folder != "" {
			indexPath := normalizePathForWorkspaceAPI(filepath.Join("skills", migration.Folder, "SKILL.md"), workspacePath)
			index := []byte(workflowSkillsIndex(migration.Folder, files, titles))
			if existing, e := readFile(ctx, indexPath); e == nil {
				if existing != string(index) && skillsMigrationHash([]byte(existing)) != previous.IndexSHA256 {
					return "", fmt.Errorf("the migrated skill index was edited separately; reconcile it before retrying")
				}
			} else if !workspacepkg.IsFileNotFound(e) {
				return "", e
			}
			if err = writeFile(ctx, indexPath, string(index)); err != nil {
				return "", err
			}
			migration.IndexSHA256 = skillsMigrationHash(index)
			caps, _ := manifest["capabilities"].(map[string]interface{})
			if caps == nil {
				caps = map[string]interface{}{}
				manifest["capabilities"] = caps
			}
			migrationAppendSkill(caps, "selected_skills", migration.Folder)
		}
		rewriteWorkflowSkillReferences(plan, &migration)
		rewriteWorkflowSkillReferences(config, &migration)
		newPlan, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return "", err
		}
		if !sameAgentJSON([]byte(planRaw), newPlan) {
			if err = writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, planPath), planPath, string(newPlan)); err != nil {
				return "", err
			}
		}
		newConfig, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return "", err
		}
		if !sameAgentJSON([]byte(configRaw), newConfig) {
			if err = writeFile(workspacepkg.WithSystemManagedWritePaths(ctx, configPath), configPath, string(newConfig)); err != nil {
				return "", err
			}
		}
		if err = writeManifest(); err != nil {
			return "", err
		}
		logPlanChange(ctx, workspacePath, PlanChangelogEntry{Tool: "migrate_workflow_skills", Reason: fmt.Sprintf("Preserved %d workflow guide/support files in private skills/%s; retired learnings access without changing knowledge-base grants. Old files and history remain.", len(files), migration.Folder), BeforeSnapshot: json.RawMessage(planRaw), AfterSnapshot: json.RawMessage(newPlan)}, readFile, withPlanMutationWriteAccess(workspacePath, writeFile), logger)
		if preparationError != nil {
			return "", preparationError
		}
		return fmt.Sprintf(`{"status":"migrated","copied_files":%d,"skill":%q,"next":"Review the workflow-local skill index and preserved references, then stamp 1.0.51. Old learning files remain as history."}`, len(files), migration.Folder), nil
	}
}

func ValidateWorkflowSkillsStamp(workflowDir string) error {
	root, err := os.OpenRoot(workflowDir)
	if err != nil {
		return err
	}
	defer root.Close()
	raw, err := root.ReadFile("workflow.json")
	if err != nil {
		return err
	}
	var manifest struct {
		CodeLayoutVersion int                      `json:"code_layout_version"`
		Migration         *WorkflowSkillsMigration `json:"workflow_skills_migration"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if manifest.CodeLayoutVersion != 1 || manifest.Migration == nil || manifest.Migration.Version != WorkflowSkillsContractVersion {
		return fmt.Errorf("run migrate_workflow_skills after the existing code-layout upgrade before stamping %s", WorkflowSkillsContractVersion)
	}
	files, err := collectWorkflowSkillSources(root, nil)
	if err != nil {
		return err
	}
	if len(files) != len(manifest.Migration.Files) {
		return fmt.Errorf("workflow guides changed; run migrate_workflow_skills again")
	}
	for i, file := range files {
		if file != manifest.Migration.Files[i] {
			return fmt.Errorf("workflow guide changed: %s; run migrate_workflow_skills again", file.Source)
		}
		if _, err = skills.ValidateSkillName(manifest.Migration.Folder); err != nil {
			return err
		}
		copied, err := root.ReadFile(filepath.Join("skills", manifest.Migration.Folder, filepath.FromSlash(file.Target)))
		if err != nil || skillsMigrationHash(copied) != file.SHA256 {
			return fmt.Errorf("workflow guide copy is missing or changed: %s", file.Source)
		}
	}
	if len(files) > 0 {
		index, err := root.ReadFile(filepath.Join("skills", manifest.Migration.Folder, "SKILL.md"))
		if err != nil {
			return err
		}
		if manifest.Migration.IndexSHA256 == "" || skillsMigrationHash(index) != manifest.Migration.IndexSHA256 {
			return fmt.Errorf("the migrated skill index is missing or changed; run migrate_workflow_skills again")
		}
	}
	config, err := root.ReadFile(filepath.Join("planning", "step_config.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var c StepConfigFile
	if err == nil {
		parsed, parseErr := ParseStepConfigContent(string(config))
		if parseErr != nil {
			return parseErr
		}
		c.Steps = parsed
	}
	selected := map[string][]string{}
	for _, step := range c.Steps {
		if step.AgentConfigs != nil {
			if step.AgentConfigs.LearningsAccess != "" {
				return fmt.Errorf("retire step %s learnings_access through migrate_workflow_skills", step.ID)
			}
			selected[step.ID] = step.AgentConfigs.EnabledSkills
		}
	}
	if len(files) > 0 {
		for id, access := range manifest.Migration.StepAccess {
			if access == "none" {
				continue
			}
			found := false
			for _, name := range selected[id] {
				if name == manifest.Migration.Folder {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("select the migrated workflow skill for step %s through migrate_workflow_skills", id)
			}
		}
	}
	return nil
}
