package step_based_workflow

import (
	"context"
	"crypto/sha256"
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

// Only the main script owns the run's source identity. Nested run_script calls
// keep their own bundle hashes in helper receipts and must not replace it.
func (hcpo *StepBasedWorkflowOrchestrator) recordScriptSourceSnapshot(ctx context.Context, id, directory string) error {
	if hcpo.directCrewScript == nil || id != hcpo.directCrewScript.ID {
		return nil
	}
	view := workflowfiles.WorkflowFS{Docs: GetPromptDocsRoot(), Root: hcpo.GetWorkspacePath()}
	relative := strings.TrimPrefix(directory, hcpo.GetWorkspacePath()+"/")
	hash, err := pythonBundleFingerprint(view, relative)
	if err != nil {
		return fmt.Errorf("read executed script snapshot: %w", err)
	}
	inputs, err := fs.ReadFile(view, "runs/"+hcpo.selectedRunFolder+"/inputs.json")
	if err != nil {
		return fmt.Errorf("read executed input snapshot: %w", err)
	}
	inputsHash := fmt.Sprintf("%x", sha256.Sum256(inputs))
	if hcpo.resumingPython() {
		resume := hcpo.executionOptions.PythonResume
		if hash != resume.SourceHash || inputsHash != resume.InputsHash {
			return fmt.Errorf("%s", pythonResumeNewRun)
		}
	}
	return hcpo.upsertRunMetadata(ctx, hcpo.selectedRunFolder, func(metadata map[string]interface{}) {
		metadata["source_snapshot_hash"] = hash
		metadata["inputs_snapshot_hash"] = inputsHash
	})
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
