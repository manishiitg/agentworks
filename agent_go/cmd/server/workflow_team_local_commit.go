package server

import (
	"context"
	"os"
	"path"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// A trusted native commit already holds the cross-process workspace lock.
// Calling the HTTP document writer here waits for the same lock in another
// process. Use the anchored atomic writer after the native team validator.
func heldTeamFile(ctx context.Context, file string) (*os.Root, string, bool, error) {
	root, rel := teamWorkspacePath(file)
	held, _ := ctx.Value(teamLockHeldKey{}).(string)
	if root == "" || held != root {
		return nil, "", false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", true, err
	}
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		return nil, "", true, err
	}
	defer base.Close()
	anchored, err := wf.OpenDirectory(base, root, false)
	return anchored, rel, true, err
}

func writeHeldTeamFile(ctx context.Context, file, content string) (bool, error) {
	root, rel, held, err := heldTeamFile(ctx, file)
	if !held || err != nil {
		return held, err
	}
	defer root.Close()
	return true, wf.ReplaceNativeManagedFile(root, rel, []byte(content))
}

func deleteHeldTeamFile(ctx context.Context, file string) (bool, error) {
	root, rel, held, err := heldTeamFile(ctx, file)
	if !held || err != nil {
		return held, err
	}
	defer root.Close()
	parent, err := wf.OpenDirectory(root, path.Dir(rel), false)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	defer parent.Close()
	if err = externalNoSymlinks(parent, path.Base(rel)); err != nil {
		return true, err
	}
	err = parent.Remove(path.Base(rel))
	if os.IsNotExist(err) {
		err = nil
	}
	return true, err
}
