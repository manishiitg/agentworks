package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func DiscoverSkillsIn(workspaceAPIURL, workspacePath string) (*WorkspaceInventory, error) {
	var raw struct {
		Documents []struct {
			Name     string   `json:"folder_name"`
			FilePath string   `json:"file_path"`
			Document string   `json:"document"`
			Managed  bool     `json:"managed"`
			UsedBy   []string `json:"used_by"`
		} `json:"documents"`
		Usage map[string][]string `json:"usage"`
	}
	if err := workspaceSkillOperation(context.Background(), workspaceAPIURL, "list", workspacePath, "", &raw); err != nil {
		return nil, err
	}
	result := &WorkspaceInventory{Skills: []Skill{}, Usage: raw.Usage}
	for _, doc := range raw.Documents {
		parsed, err := ParseSkillFromContent(doc.Document, doc.Name, doc.FilePath)
		if err != nil {
			continue
		}
		parsed.Managed = doc.Managed || IsBuiltinSkill(doc.Name)
		parsed.UsedBy = doc.UsedBy
		result.Skills = append(result.Skills, *parsed)
	}
	for name, usedBy := range raw.Usage {
		builtin := builtinAttachableSkill(name)
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
			result.Skills = append(result.Skills, Skill{Frontmatter: SkillFrontmatter{Name: builtin.Name, Description: builtin.Description}, FolderName: name, Content: builtin.Content, FilePath: "Platform", Managed: true, UsedBy: usedBy})
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
