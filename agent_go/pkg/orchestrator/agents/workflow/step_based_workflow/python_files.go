package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const pythonCrewFileLimit = 256 << 10

// Python supplies only a relative filename. The server-owned executable binding
// chooses its Crew, including the live knowledgebase after source is frozen.
// This is the existing file reader; it never lends a raw filesystem grant.
func pythonCrewFileReader(workspace string) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		p, ok := args["path"].(string)
		clean, err := wf.CleanRelative(p)
		if !ok || len(args) != 1 || err != nil || clean != p || p == "." || wf.Private(p) || strings.HasPrefix(p, "Crew/") || strings.HasPrefix(p, "Workflow/") || strings.HasPrefix(p, "_users/") {
			return "", fmt.Errorf("Choose a relative file in this Crew, such as knowledgebase/notes/example.md. Private files and other Crews are not available here.")
		}
		docs := GetPromptDocsRoot()
		layout, found, err := crewroot.LookupExecutable(docs, workspace)
		if err != nil || !found {
			return "", fmt.Errorf("Run this helper from a saved workflow in its Crew, then choose a file from that Crew's Files.")
		}
		guard := &wf.FolderGuard{ReadPaths: []string{"."}}
		root, name, _, guard, _, err := wf.BoundCrewFileRequest(docs, layout.Crew.Virtual, p, "", guard)
		if err != nil {
			return "", fmt.Errorf("This file is not available here. Choose a file from this Crew's Files.")
		}
		scope, _, err := crewroot.ScopeRoot(docs, root, name)
		// Knowledge is a managed public resource stored privately. Other
		// platform settings, run/chat records and database files stay private.
		if err != nil || crewroot.Within(layout.Crew.Private, scope) && !strings.HasPrefix(p, "knowledgebase/") {
			return "", fmt.Errorf("This file is private. Choose a document, note, skill or knowledge file from this Crew's Files instead.")
		}
		opened, relative, err := wf.OpenWorkflowDirectory(docs, root, name)
		if err != nil {
			return "", fmt.Errorf("This file is not available here. Choose a regular file from this Crew's Files.")
		}
		defer opened.Close()
		file, err := wf.ReadAt(opened, relative, guard)
		if err != nil || !file.Exists {
			return "", fmt.Errorf("This file is not available here. Choose a regular file from this Crew's Files.")
		}
		if file.Size > pythonCrewFileLimit || file.Encoding != "utf-8" {
			return "", fmt.Errorf("Choose a text file up to 256 KiB. Split a larger document into smaller files in this Crew.")
		}
		data, err := json.Marshal(map[string]string{"content": file.Content})
		return string(data), err
	}
}
