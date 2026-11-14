package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// InstallWithData coordinates a first data seed and code publication in the
// same process. Normal get/list/resolve readers take this existing shared
// workspace lock, so none observes the new pointer before data commits.
// installData keeps its SQLite transaction open while invoking publish, rolls
// it back on every error, and returns only once its commit succeeds/fails.
func InstallWithData(ctx context.Context, docs string, request Request, installData func(publish func() error) error) (Result, error) {
	if installData == nil || request.Action != "create" && request.Action != "update" || !identifier.MatchString(request.ID) {
		return Result{}, fail(400, "prepare one item before installing its data")
	}
	clean, err := wf.CleanRelative(request.Root)
	if err != nil || clean != request.Root || clean == "." {
		return Result{}, fail(400, "canonical project root required")
	}
	release, err := wf.LockWorkspace(ctx, docs)
	if err != nil {
		return Result{}, err
	}
	defer release()
	root, err := openProjectRoot(docs, clean)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	before, err := loadState(root, request.ID)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	var result Result
	publish := func() error {
		draft, err := executeLocked(ctx, docs, request)
		if err != nil {
			return err
		}
		if draft.Dashboard == nil {
			return fmt.Errorf("the item draft could not be prepared")
		}
		result, err = executeLocked(ctx, docs, Request{Root: request.Root, ID: request.ID, Action: "publish", ExpectedRevision: draft.Dashboard.Revision, IncludeDraft: true, Guard: request.Guard})
		return err
	}
	if err = installData(publish); err != nil {
		// Revision files may remain unreferenced, just as cancelled drafts do; the
		// authoritative pointer is restored while readers are still excluded.
		var restoreErr error
		if existed {
			var data []byte
			data, restoreErr = json.Marshal(before)
			if restoreErr == nil {
				restoreErr = atomicWrite(root, StatePath(request.ID), data)
			}
		} else {
			restoreErr = root.Remove(StatePath(request.ID))
			if os.IsNotExist(restoreErr) {
				restoreErr = nil
			}
		}
		return Result{}, errors.Join(err, restoreErr)
	}
	return result, nil
}
