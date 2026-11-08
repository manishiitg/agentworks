package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
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
	cache, err := workflowfiles.OpenDirectory(root, cachePath, true)
	if err != nil {
		return err
	}
	defer cache.Close()
	if err := cache.Chmod(".", 0700); err != nil {
		return err
	}
	output, outputPath, err := workflowfiles.OpenWorkflowDirectory(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), strings.TrimPrefix(destination, hcpo.GetWorkspacePath()+"/"))
	if err != nil {
		return err
	}
	defer output.Close()
	if root.Name() != output.Name() {
		return fmt.Errorf("script snapshot must stay in its owning runtime")
	}
	for name, data := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !CrewScriptBundleFile(name) {
			return fmt.Errorf("invalid saved script bundle")
		}
		// The resolved definition is written separately through the server writer.
		if name == "script.json" {
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		if info, err := cache.Lstat(hash); os.IsNotExist(err) {
			temporary := ".new-" + uuid.NewString()
			file, err := cache.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0444)
			if err != nil {
				return err
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr == nil && closeErr == nil {
				err = cache.Link(temporary, hash)
			}
			_ = cache.Remove(temporary)
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err != nil && !os.IsExist(err) {
				return err
			}
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
			return fmt.Errorf("invalid immutable script snapshot")
		}
		target := filepath.Join(outputPath, filepath.FromSlash(name))
		parent, err := workflowfiles.OpenDirectory(root, filepath.Dir(target), true)
		if err != nil {
			return err
		}
		parent.Close()
		// Remove only this invocation's old directory entry, never a blob inode.
		if err := root.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := root.Link(filepath.Join(cachePath, hash), target); err != nil {
			return err
		}
	}
	return nil
}
