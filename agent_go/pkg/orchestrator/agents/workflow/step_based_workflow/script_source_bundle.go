package step_based_workflow

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (hcpo *StepBasedWorkflowOrchestrator) scriptSnapshotReadOnlyPaths() []string {
	paths := []string{hcpo.GetWorkspacePath() + "/runs/source-blobs"}
	if hcpo.selectedRunFolder == "" {
		return paths
	}
	execution := "runs/" + hcpo.selectedRunFolder + "/execution"
	view := workflowfiles.WorkflowFS{Docs: GetPromptDocsRoot(), Root: hcpo.GetWorkspacePath()}
	_ = fs.WalkDir(view, execution, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "code" {
			paths = append(paths, hcpo.GetWorkspacePath()+"/"+name)
			return fs.SkipDir
		}
		return nil
	})
	return paths
}

// Source bytes are immutable per content hash. Runs link the private snapshot,
// never the editable source, so unchanged vendored libraries are stored once.
func (hcpo *StepBasedWorkflowOrchestrator) materializeScriptBundle(ctx context.Context, destination string, files map[string][]byte) error {
	root, cachePath, err := workflowfiles.OpenWorkflowDirectory(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), "runs/source-blobs")
	if err != nil {
		return err
	}
	defer root.Close()
	output, outputPath, err := workflowfiles.OpenWorkflowDirectory(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), strings.TrimPrefix(destination, hcpo.GetWorkspacePath()+"/"))
	if err != nil {
		return err
	}
	defer output.Close()
	if root.Name() != output.Name() {
		return fmt.Errorf("script snapshot must stay in its owning runtime")
	}
	bundle := make(map[string][]byte, len(files))
	for name, data := range files {
		if !CrewScriptBundleFile(name) {
			return fmt.Errorf("invalid saved script bundle")
		}
		// The resolved definition is written separately through the server writer.
		if name == "script.json" {
			continue
		}
		bundle[name] = data
	}
	return workflowfiles.MaterializeSourceSnapshot(ctx, root, cachePath, outputPath, bundle)
}
