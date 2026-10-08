package localfiles

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/coding-agent-loop/workspace/patchformat"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func (e *Executor) patch(ctx context.Context, g Grant, r Request) ([]wf.WriteReceipt, error) {
	if r.RequestID == "" || len(r.RequestID) > 100 || len(r.Content) > wf.MaxFileBytes {
		return nil, &wf.FileError{Status: 400, Message: "patch requires bounded diff and request identity"}
	}
	sections, err := patchformat.SplitApplyPatch(r.Content)
	if err != nil {
		return nil, &wf.FileError{Status: 400, Message: err.Error()}
	}
	if !patchformat.IsApplyPatchFormat(r.Content) || len(sections) == 1 && r.Path != "" {
		sections = []patchformat.ApplyPatchSection{{Path: r.Path, Patch: r.Content}}
	}
	if len(sections) == 0 || len(sections) > 32 {
		return nil, &wf.FileError{Status: 400, Message: "patch must name 1 to 32 files"}
	}
	if g.Downloads {
		companion := e.grants["downloads"]
		downloadTargets := 0
		within := func(grant Grant, path string) bool {
			if containsPath(grant.Root, path) {
				return true
			}
			canonical, err := filepath.EvalSymlinks(grant.Root)
			return err == nil && containsPath(canonical, path)
		}
		for _, section := range sections {
			if filepath.IsAbs(section.Path) && !within(g, section.Path) && within(companion, section.Path) {
				downloadTargets++
			}
		}
		if downloadTargets > 0 {
			if downloadTargets != len(sections) {
				return nil, &wf.FileError{Status: 400, Message: "patch each shared folder separately"}
			}
			g = companion
		}
	}
	if !g.Writable {
		return nil, &wf.FileError{Status: 403, Message: "folder is read-only"}
	}
	type change struct {
		file    wf.File
		content string
		receipt wf.WriteReceipt
	}
	changes := make([]change, 0, len(sections))
	receipts := []wf.WriteReceipt{}
	editor := e.editors[g.ID]
	err = editor.Serialized(ctx, func(locked context.Context) error {
		seen := map[string]bool{}
		// Reuse the same Begin Patch parser and diff application as server files.
		// Every path and hunk is checked before any mutation.
		for _, section := range sections {
			if err := locked.Err(); err != nil {
				return err
			}
			p := section.Path
			if filepath.IsAbs(p) {
				relative, err := filepath.Rel(g.Root, p)
				if err != nil {
					return err
				}
				if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					root, err := filepath.EvalSymlinks(g.Root)
					if err != nil {
						return err
					}
					relative, err = filepath.Rel(root, p)
					if err != nil {
						return err
					}
				}
				p = relative
			}
			p, err = wf.CleanRelative(filepath.ToSlash(p))
			if err != nil || wf.ProtectedWrite(p) || !g.Guard.Allows(p, true) || seen[p] {
				return &wf.FileError{Status: 403, Message: "patch path is outside grants, protected or duplicated"}
			}
			seen[p] = true
			file, err := editor.Read(".", p, &g.Guard)
			if err != nil {
				return err
			}
			if section.IsAdd && file.Exists {
				return &wf.FileError{Status: 409, Message: "patch cannot add an existing file"}
			}
			content, err := handlers.ApplyDiffPatchDirect(file.Content, section.Patch)
			if err != nil {
				return &wf.FileError{Status: 400, Message: err.Error()}
			}
			if len(content) > wf.MaxFileBytes {
				return &wf.FileError{Status: 413, Message: "patched file exceeds 2 MiB"}
			}
			changes = append(changes, change{file: file, content: content})
		}
		for i := range changes {
			change := &changes[i]
			var receipt wf.WriteReceipt
			err := locked.Err()
			if err == nil {
				receipt, err = editor.Write(locked, wf.WriteRequest{Root: ".", Path: change.file.Path, Content: change.content, ExpectedRevision: change.file.Revision, RequestID: fmt.Sprintf("%s-%d", r.RequestID, i), Actor: e.Hello.DeviceID + "/" + g.ID, Identity: r.Identity, Guard: &g.Guard})
			}
			if err != nil {
				failures := []string{}
				// Same all-or-rollback contract as the existing workspace patch tool.
				for j := i - 1; j >= 0; j-- {
					original := changes[j]
					var restoreErr error
					if original.file.Exists {
						_, restoreErr = editor.Write(context.WithoutCancel(locked), wf.WriteRequest{Root: ".", Path: original.file.Path, Content: original.file.Content, ExpectedRevision: original.receipt.Revision, RequestID: fmt.Sprintf("%s-rollback-%d", r.RequestID, j), Actor: e.Hello.DeviceID + "/" + g.ID, Identity: r.Identity, Guard: &g.Guard})
					} else {
						restoreErr = editor.RemoveCreatedLocked(context.WithoutCancel(locked), original.file.Path)
					}
					if restoreErr != nil {
						failures = append(failures, original.file.Path)
					}
				}
				if len(failures) > 0 {
					return &wf.FileError{Status: 409, Message: "patch failed; rollback incomplete for " + strings.Join(failures, ", ")}
				}
				return err
			}
			change.receipt = receipt
			receipts = append(receipts, receipt)
		}
		return nil
	})
	return receipts, err
}
