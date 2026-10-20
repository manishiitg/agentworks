package step_based_workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// readStepLearningFiles reads all learning files from a step-specific folder
// Reads .md files from the step folder, all files from code/ subfolder (Code Execution Mode),
// and .py/.sh files from scripts/ subfolder (Simple Mode)
// Deletes _learning_new.md if it exists (leftover temp file from previous runs)
// Excludes metadata files (.learning_metadata.json)
// Returns a map of filename -> content
func (hcpo *StepBasedWorkflowOrchestrator) readStepLearningFiles(ctx context.Context, stepLearningsPath string) (map[string]string, error) {
	learningFiles := make(map[string]string)

	// List all files in the step folder
	files, err := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, stepLearningsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list files in %s: %w", stepLearningsPath, err)
	}

	// Delete _learning_new.md if it exists (leftover temp file from previous runs)
	tempFilePath := filepath.Join(stepLearningsPath, "_learning_new.md")
	exists, _ := hcpo.BaseOrchestrator.CheckWorkspaceFileExists(ctx, tempFilePath)
	if exists {
		if err := hcpo.BaseOrchestrator.DeleteWorkspaceFile(ctx, tempFilePath); err != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to delete temp file %s: %v", tempFilePath, err))
		} else {
			hcpo.GetLogger().Info(fmt.Sprintf("🗑️ Deleted leftover temp file: %s", tempFilePath))
		}
	}

	// Read root-level learning files from the step folder.
	// Step-local SKILL.md is deprecated; scripted steps should persist
	// code artifacts (main.py, helpers) here instead.
	// Exclude metadata files and temporary files used for internal tracking only.
	for _, file := range files {
		// Skip metadata files - these should not be passed to execution agents
		if file == ".learning_metadata.json" || strings.HasSuffix(file, ".learning_metadata.json") || file == "script_metadata.json" {
			continue
		}
		// Skip temporary learning files - _learning_new.md should have been deleted above, but skip it if still present
		if file == "_learning_new.md" {
			continue
		}
		if file == "SKILL.md" {
			continue
		}
		if strings.HasSuffix(file, ".md") {
			filePath := filepath.Join(stepLearningsPath, file)
			content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, filePath)
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read learning file %s: %v", filePath, err))
				continue
			}
			learningFiles[file] = content
			continue
		}
		// scripted saves main.py and helper files directly in the learnings root, not in code/.
		// Count common script/helper extensions here so empty-checks and prompt learnings work.
		if strings.HasSuffix(file, ".py") || strings.HasSuffix(file, ".sh") {
			filePath := filepath.Join(stepLearningsPath, file)
			content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, filePath)
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read root learning file %s: %v", filePath, err))
				continue
			}
			learningFiles[file] = content
		}
	}

	// Check if code/ subfolder exists (for code execution mode)
	// This subfolder contains code examples/patterns (Python, shell scripts, etc.)
	codeSubfolderPath := filepath.Join(stepLearningsPath, "code")
	codeFiles, err := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, codeSubfolderPath)
	if err == nil && len(codeFiles) > 0 {
		// Read ALL files from code/ subfolder (any language/format the learning agent saved)
		// Skip metadata and hidden files only
		codeFileCount := 0
		for _, file := range codeFiles {
			if strings.HasPrefix(file, ".") {
				continue // Skip hidden/metadata files
			}
			filePath := filepath.Join(codeSubfolderPath, file)
			content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, filePath)
			if err != nil {
				hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read code learning file %s: %v", filePath, err))
				continue
			}
			// Prefix with "code/" to indicate it's from the code subfolder
			learningFiles[filepath.Join("code", file)] = content
			codeFileCount++
		}
		if codeFileCount > 0 {
			hcpo.GetLogger().Info(fmt.Sprintf("📁 Read %d code file(s) from code/ subfolder", codeFileCount))
		}
	}
	// Note: If code/ subfolder doesn't exist or is empty, that's fine - it's optional

	// Check if scripts/ subfolder exists (for simple mode)
	// This subfolder contains .py Python scripts and .sh shell scripts
	scriptsSubfolderPath := filepath.Join(stepLearningsPath, "scripts")
	scriptFiles, err := hcpo.BaseOrchestrator.ListWorkspaceFiles(ctx, scriptsSubfolderPath)
	if err == nil && len(scriptFiles) > 0 {
		// Read all .py and .sh files from scripts/ subfolder
		scriptFileCount := 0
		for _, file := range scriptFiles {
			if strings.HasSuffix(file, ".py") || strings.HasSuffix(file, ".sh") {
				filePath := filepath.Join(scriptsSubfolderPath, file)
				content, err := hcpo.BaseOrchestrator.ReadWorkspaceFile(ctx, filePath)
				if err != nil {
					hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Failed to read script learning file %s: %v", filePath, err))
					continue
				}
				// Prefix with "scripts/" to indicate it's from the scripts subfolder
				learningFiles[filepath.Join("scripts", file)] = content
				scriptFileCount++
			}
		}
		if scriptFileCount > 0 {
			hcpo.GetLogger().Info(fmt.Sprintf("📁 Read %d script file(s) (.py/.sh) from scripts/ subfolder", scriptFileCount))
		}
	}
	// Note: If scripts/ subfolder doesn't exist or is empty, that's fine - it's optional

	return learningFiles, nil
}

// formatStepLearningFilesAsHistory formats a map of learning files (filename -> content) into a formatted history string
// Returns the combined content and list of file paths
func (hcpo *StepBasedWorkflowOrchestrator) formatStepLearningFilesAsHistory(learningFiles map[string]string) (string, []string) {
	if len(learningFiles) == 0 {
		return "", []string{}
	}

	var result strings.Builder
	result.WriteString("## Learning Context (Pre-loaded - DO NOT re-read these files)\n\n")
	result.WriteString("**Note**: The following learning content has been pre-loaded from the learnings folder. ")
	result.WriteString("You do NOT need to read these files again - the full content is included below.\n\n")
	filePaths := make([]string, 0, len(learningFiles))

	// Sort filenames for consistent output
	filenames := make([]string, 0, len(learningFiles))
	for filename := range learningFiles {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)

	// Format each file with clear source attribution
	for i, filename := range filenames {
		content := learningFiles[filename]
		if i > 0 {
			result.WriteString("\n---\n\n")
		}

		// For SKILL.md files, extract name from frontmatter and strip it from content
		displayName := filename
		displayContent := content
		if filename == "SKILL.md" && strings.HasPrefix(content, "---") {
			displayName, displayContent = extractSkillLearningContent(content)
		}

		// Make it very clear this is the file content, already loaded
		result.WriteString(fmt.Sprintf("### 📄 Skill: `%s` (content already loaded below)\n\n", displayName))
		result.WriteString(displayContent)
		result.WriteString("\n")
		filePaths = append(filePaths, filename)
	}

	return result.String(), filePaths
}

// extractSkillLearningContent parses SKILL.md YAML frontmatter and returns (name, body).
// If parsing fails, returns the filename and full content unchanged.
func extractSkillLearningContent(content string) (string, string) {
	// Find the closing frontmatter delimiter
	rest := content[3:] // Skip opening ---
	endIndex := strings.Index(rest, "\n---")
	if endIndex == -1 {
		return "SKILL.md", content
	}

	frontmatterYAML := strings.TrimSpace(rest[:endIndex])
	body := strings.TrimSpace(rest[endIndex+4:]) // Skip \n---

	// Extract name from frontmatter (simple line-based parse to avoid import)
	name := "SKILL.md"
	for _, line := range strings.Split(frontmatterYAML, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, "\"'")
			break
		}
	}

	return name, body
}
