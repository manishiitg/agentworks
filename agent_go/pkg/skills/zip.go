package skills

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
)

// ValidateZipSkill validates a skill from an uploaded zip file without extracting to workspace
func ValidateZipSkill(workspaceAPIURL string, file multipart.File, header *multipart.FileHeader) (*ValidateSkillResponse, error) {
	// Verify .zip extension
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		return &ValidateSkillResponse{Valid: false, Error: "file must be a .zip file"}, nil
	}

	// Read the zip file into memory
	data, err := io.ReadAll(file)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("failed to read file: %v", err)}, nil
	}

	// Open zip archive
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("failed to read zip: %v", err)}, nil
	}

	// Find SKILL.md in the zip
	skillFile, basePath, err := findSkillMdInZip(zipReader)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: err.Error()}, nil
	}

	// Read SKILL.md content
	rc, err := skillFile.Open()
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("failed to open %s: %v", SkillFileName, err)}, nil
	}
	defer rc.Close()

	content, err := io.ReadAll(rc)
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("failed to read %s: %v", SkillFileName, err)}, nil
	}

	// Validate the SKILL.md content
	frontmatter, _, err := ValidateSkillContent(string(content))
	if err != nil {
		return &ValidateSkillResponse{Valid: false, Error: fmt.Sprintf("invalid %s: %v", SkillFileName, err)}, nil
	}

	// Collect file list (relative to the skill root)
	var fileNames []string
	for _, f := range zipReader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// Get path relative to the skill folder
		relPath := strings.TrimPrefix(f.Name, basePath)
		relPath = strings.TrimPrefix(relPath, "/")
		if relPath != "" {
			fileNames = append(fileNames, relPath)
		}
	}

	// The authorized API checks for an existing install in its workspace.

	return &ValidateSkillResponse{Valid: true, Frontmatter: frontmatter, Files: fileNames, Exists: false}, nil
}

// ImportZipSkillInto imports files into one workspace.
func ImportZipSkillInto(workspaceAPIURL string, file multipart.File, header *multipart.FileHeader, targetDir string) (*ImportSkillResponse, error) {
	// Verify .zip extension
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		return &ImportSkillResponse{Success: false, Error: "file must be a .zip file"}, nil
	}

	// Read the zip file into memory
	data, err := io.ReadAll(file)
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("failed to read file: %v", err)}, nil
	}

	// Open zip archive
	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("failed to read zip: %v", err)}, nil
	}

	// Find SKILL.md in the zip
	skillFile, basePath, err := findSkillMdInZip(zipReader)
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: err.Error()}, nil
	}

	// Read and validate SKILL.md content
	rc, err := skillFile.Open()
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("failed to open %s: %v", SkillFileName, err)}, nil
	}
	content, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("failed to read %s: %v", SkillFileName, err)}, nil
	}

	frontmatter, _, err := ValidateSkillContent(string(content))
	if err != nil {
		return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("invalid %s: %v", SkillFileName, err)}, nil
	}

	// Determine skill name from frontmatter or filename
	skillName := frontmatter.Name
	if skillName == "" {
		// Fall back to zip filename without extension
		skillName = strings.TrimSuffix(header.Filename, ".zip")
	}
	skillName = sanitizeFolderName(skillName)

	files := map[string][]byte{}

	// Extract all files from the zip to the skill folder
	for _, f := range zipReader.File {
		// Skip directories
		if f.FileInfo().IsDir() {
			continue
		}

		// Get path relative to the skill root
		relPath := strings.TrimPrefix(f.Name, basePath)
		relPath = strings.TrimPrefix(relPath, "/")
		if relPath == "" {
			continue
		}

		// Security check: prevent path traversal
		if strings.Contains(relPath, "..") || strings.HasPrefix(relPath, "/") {
			return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("invalid path in zip: %s", f.Name)}, nil
		}

		// Read and write file content
		fileRC, err := f.Open()
		if err != nil {
			return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("failed to open %s: %v", f.Name, err)}, nil
		}
		fileContent, err := io.ReadAll(fileRC)
		fileRC.Close()
		if err != nil {
			return &ImportSkillResponse{Success: false, Error: fmt.Sprintf("failed to read %s: %v", f.Name, err)}, nil
		}

		files[relPath] = fileContent
	}
	if err := InstallSkillFilesIn(workspaceAPIURL, strings.TrimSuffix(targetDir, "/skills"), skillName, files); err != nil {
		return &ImportSkillResponse{Success: false, Error: err.Error()}, nil
	}

	return &ImportSkillResponse{Success: true, SkillName: skillName}, nil
}

// findSkillMdInZip locates SKILL.md in a zip, handling both root-level and single-folder wrapper structures
func findSkillMdInZip(reader *zip.Reader) (*zip.File, string, error) {
	// First, check if SKILL.md is at the root
	for _, f := range reader.File {
		if f.Name == SkillFileName {
			return f, "", nil
		}
	}

	// Check for single-folder wrapper structure
	// Find the first directory at root level
	var rootFolder string
	for _, f := range reader.File {
		name := strings.TrimSuffix(f.Name, "/")
		// Check if this is a top-level directory
		if !strings.Contains(name, "/") && f.FileInfo().IsDir() {
			rootFolder = name
			break
		}
		// Also detect implicit directories from file paths
		if idx := strings.Index(f.Name, "/"); idx > 0 {
			candidate := f.Name[:idx]
			if rootFolder == "" {
				rootFolder = candidate
			} else if rootFolder != candidate {
				// Multiple root folders - not a single-folder wrapper
				rootFolder = ""
				break
			}
		}
	}

	if rootFolder != "" {
		// Check if SKILL.md exists in the root folder
		skillPath := rootFolder + "/" + SkillFileName
		for _, f := range reader.File {
			if f.Name == skillPath {
				return f, rootFolder, nil
			}
		}
	}

	return nil, "", fmt.Errorf("no %s found in zip file", SkillFileName)
}
