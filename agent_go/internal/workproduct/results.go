package workproduct

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const resultsPath = "outputs/results.json"
const maxResults = 100

var resultsMu sync.Mutex

type Result struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
	Status    string `json:"status,omitempty"`
}
type Results struct {
	Results []Result `json:"results"`
}

// Result paths use the same virtual project paths as workspace file tools.
// In a local-folder Crew, _files/ points into the user's existing folder.
func resultFile(root crewroot.Roots, rel string) (string, error) {
	if len(rel) == 0 || len(rel) > 1024 || strings.ContainsAny(rel, "\\\x00\r\n") || strings.Contains(rel, ":") || strings.HasPrefix(rel, "/") || path.Clean(rel) != rel || rel == "." {
		return "", fmt.Errorf("use a file path relative to this project")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", fmt.Errorf("path leaves this project")
		}
	}
	file, bound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), root.Virtual+"/"+rel)
	if err != nil {
		return "", err
	}
	if !bound || crewroot.Within(root.Private, file) || !(crewroot.Within(root.Files, file) || crewroot.Within(root.Resources, file)) {
		return "", fmt.Errorf("choose a project file")
	}
	return file, nil
}

func readResults(root crewroot.Roots) (Results, error) {
	out := Results{Results: []Result{}}
	file, err := resultFile(root, resultsPath)
	if err != nil {
		return out, err
	}
	f, err := os.Open(file)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 256*1024+1))
	if err != nil {
		return out, err
	}
	if len(raw) > 256*1024 {
		return out, fmt.Errorf("Results is too large")
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("Results could not be read")
	}
	if len(out.Results) > maxResults {
		return out, fmt.Errorf("too many Results")
	}
	for i := range out.Results {
		row := &out.Results[i]
		if len(row.Title) > 200 || len(row.UpdatedAt) > 64 {
			return out, fmt.Errorf("invalid Result")
		}
		file, err := resultFile(root, row.Path)
		if err != nil {
			return out, err
		}
		info, err := os.Stat(file)
		row.Status = "ready"
		if os.IsNotExist(err) {
			row.Status = "missing"
		} else if err != nil || !info.Mode().IsRegular() {
			row.Status = "unavailable"
		}
	}
	if out.Results == nil {
		out.Results = []Result{}
	}
	return out, nil
}

// ReadResults resolves the server-owned binding. The caller must authorize the project first.
func ReadResults(workspacePath string) (Results, error) {
	root, bound, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), workspacePath)
	if err != nil {
		return Results{}, err
	}
	if !bound {
		return Results{}, fmt.Errorf("project is unavailable")
	}
	resultsMu.Lock()
	defer resultsMu.Unlock()
	return readResults(root)
}

func workResultsFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		client := workspace.NewClient(workspaceAPIURL, workspace.WithUserID(runtime.UserID), workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID}))
		return agentprofiles.ToolSpec{
			Name: "manage_results", Category: "results",
			Description: "List, add or remove finished project files in Results. Use published Artifacts for pages (they appear automatically); never record an Artifact twice. Record finished documents, spreadsheets, reports or drafts, not temporary files. Paths are relative to this project's workspace (use _files/ for files in a local existing folder). Does not send, publish or share files.",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "enum": []string{"list", "add", "remove"}}, "path": map[string]interface{}{"type": "string", "maxLength": 1024}, "title": map[string]interface{}{"type": "string", "maxLength": 200}}, "required": []string{"action"}, "additionalProperties": false},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				root, bound, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), runtime.WorkspacePath)
				if err != nil || !bound || root.Owner != runtime.UserID {
					return "", fmt.Errorf("use a project you own")
				}
				action, _ := args["action"].(string)
				rel, _ := args["path"].(string)
				title, _ := args["title"].(string)
				if action != "list" && action != "add" && action != "remove" {
					return "", fmt.Errorf("choose list, add or remove")
				}
				if err := client.ValidatePathWithContext(ctx, path.Join(root.Virtual, resultsPath), action != "list"); err != nil {
					return "", err
				}
				resultsMu.Lock()
				defer resultsMu.Unlock()
				out, err := readResults(root)
				if err != nil {
					return "", err
				}
				if action != "list" {
					file, err := resultFile(root, rel)
					if err != nil {
						return "", err
					}
					if err := client.ValidatePathWithContext(ctx, root.Virtual+"/"+rel, false); err != nil {
						return "", err
					}
					if action == "add" {
						if strings.TrimSpace(title) == "" || len(title) > 200 {
							return "", fmt.Errorf("give the result a short title")
						}
						info, err := os.Stat(file)
						if err != nil || !info.Mode().IsRegular() {
							return "", fmt.Errorf("save the finished project file first")
						}
					}
					rows := make([]Result, 0, len(out.Results)+1)
					if action == "add" {
						rows = append(rows, Result{Path: rel, Title: strings.TrimSpace(title), UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
					}
					for _, row := range out.Results {
						if row.Path != rel {
							row.Status = ""
							rows = append(rows, row)
						}
					}
					if len(rows) > maxResults {
						return "", fmt.Errorf("remove an old Result before adding another")
					}
					out.Results = rows
					raw, err := json.MarshalIndent(out, "", "  ")
					if err != nil {
						return "", err
					}
					if _, err = client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: root.Virtual + "/" + resultsPath, Content: string(raw)}); err != nil {
						return "", err
					}
				}
				raw, err := json.Marshal(out)
				return string(raw), err
			},
		}, nil
	}
}
