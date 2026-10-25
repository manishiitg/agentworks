package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"net/http"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
)

var WorkspaceSkillDirectories = []string{"skills", ".skills", ".agents/skills", ".claude/skills", ".cursor/skills", ".codex/skills", ".gemini/skills", ".pi/skills"}

func workspaceSkillOperation(ctx context.Context, workspaceAPIURL, operation, workspacePath, name string, result interface{}) error {
	if strings.TrimSpace(workspacePath) == "" {
		return fmt.Errorf("choose a workspace to manage skills")
	}
	if name != "" {
		if _, err := ValidateSkillName(name); err != nil {
			return err
		}
	}
	payload, _ := json.Marshal(map[string]string{"workspace_path": workspacePath, "name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(workspaceAPIURL, "/")+"/api/skills/workspace/"+operation, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	wsauth.SetHeader(req)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&failure)
		return fmt.Errorf("workspace skills: %s (status %d)", failure.Error, resp.StatusCode)
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

type WorkspaceInventory struct {
	Skills []Skill             `json:"skills"`
	Usage  map[string][]string `json:"usage"`
}

// Custom means extra files in the workspace, not proof that the person
// installed them. Old provider projections alone do not establish provenance.
func (s Skill) IsCustomWorkspaceSkill(workspacePath string) bool {
	if s.Managed || s.FileManaged || s.FilePath == "" || s.FilePath == "Platform" {
		return false
	}
	if s.Source != "" || s.SourceURL != "" || s.BrainSource != "" {
		return true
	}
	path := strings.TrimPrefix(s.FilePath, strings.TrimRight(workspacePath, "/")+"/")
	return strings.HasPrefix(path, "skills/") || strings.HasPrefix(path, ".skills/")
}

func DiscoverSkillsIn(workspaceAPIURL, workspacePath string) (*WorkspaceInventory, error) {
	var raw struct {
		Documents []struct {
			Name        string   `json:"folder_name"`
			FilePath    string   `json:"file_path"`
			Document    string   `json:"document"`
			Managed     bool     `json:"managed"`
			UsedBy      []string `json:"used_by"`
			BrainSource string   `json:"brain_source"`
			BrainEdited bool     `json:"brain_edited"`
			Source      string   `json:"source"`
			SourceType  string   `json:"source_type"`
		} `json:"documents"`
		Usage map[string][]string `json:"usage"`
	}
	if err := workspaceSkillOperation(context.Background(), workspaceAPIURL, "list", workspacePath, "", &raw); err != nil {
		return nil, err
	}
	result := &WorkspaceInventory{Skills: []Skill{}, Usage: raw.Usage}
	for _, doc := range raw.Documents {
		if !(productpolicy.Selection{}).AllowsSkill(doc.Name) {
			delete(result.Usage, doc.Name)
			continue
		}
		if doc.Managed || IsBuiltinSkill(doc.Name) {
			doc.Document = (productpolicy.Selection{}).Text(doc.Document)
		}
		parsed, err := ParseSkillFromContent(doc.Document, doc.Name, doc.FilePath)
		if err != nil {
			// A malformed installed file is still part of the inventory. Show
			// the failure rather than silently losing it from the Skills panel.
			parsed = &Skill{Frontmatter: SkillFrontmatter{Name: doc.Name}, FolderName: doc.Name, FilePath: doc.FilePath, Content: doc.Document, ValidationError: err.Error()}
		}
		// Ownership comes from the workspace's managed-file marker. An
		// imported file may share a name with a registered product procedure;
		// that does not make this physical copy a platform-managed file.
		parsed.Managed = doc.Managed
		parsed.FileManaged, parsed.Builtin = doc.Managed, IsBuiltinSkill(doc.Name)
		parsed.UsedBy = doc.UsedBy
		parsed.BrainSource, parsed.BrainEdited = doc.BrainSource, doc.BrainEdited
		parsed.Source, parsed.SourceType = doc.Source, doc.SourceType
		result.Skills = append(result.Skills, *parsed)
	}
	for name, usedBy := range raw.Usage {
		if !(productpolicy.Selection{}).AllowsSkill(name) {
			delete(result.Usage, name)
			continue
		}
		builtin := (productpolicy.Selection{}).Skill(builtinAttachableSkill(name))
		if builtin == nil {
			continue
		}
		found := false
		for _, installed := range result.Skills {
			if installed.FolderName == name {
				found = true
				break
			}
		}
		if !found {
			result.Skills = append(result.Skills, Skill{Frontmatter: SkillFrontmatter{Name: builtin.Name, Description: builtin.Description}, FolderName: name, Content: builtin.Content, FilePath: "Platform", Managed: true, Builtin: true, UsedBy: usedBy})
		}
	}

	return result, nil
}
func UninstallSkillIn(ctx context.Context, workspaceAPIURL, workspacePath, name string) error {
	if _, err := ValidateSkillName(name); err != nil {
		return err
	}
	if IsBuiltinSkill(name) {
		return fmt.Errorf("this skill is managed by the platform")
	}
	return workspaceSkillOperation(ctx, workspaceAPIURL, "delete", workspacePath, name, nil)
}

func InstallSkillFilesIn(workspaceAPIURL, workspacePath, name string, files map[string][]byte) error {
	if strings.TrimSpace(workspacePath) == "" {
		return fmt.Errorf("choose a workspace before importing skills")
	}
	if _, err := ValidateSkillName(name); err != nil {
		return err
	}
	if IsBuiltinSkill(name) {
		return fmt.Errorf("this skill is managed by the platform")
	}
	payload, err := json.Marshal(map[string]interface{}{"workspace_path": workspacePath, "name": name, "files": files})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(workspaceAPIURL, "/")+"/api/skills/workspace/import", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	wsauth.SetHeader(req)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&failure)
		return fmt.Errorf("import failed: %s", failure.Error)
	}
	return nil
}
