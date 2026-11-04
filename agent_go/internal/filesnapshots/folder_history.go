package filesnapshots

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// SaveFolderRevision keeps a complete, content-only tree independently of turn
// pruning and transfer acknowledgements. The caller supplies admitted paths and
// holds its folder lock. It never snapshots a person's chats or credentials.
func (s Store) SaveFolderRevision(ctx context.Context, revision, previous string, roots crewroot.Roots, snapshot crewsync.Snapshot) error {
	// Use the committed metadata's revision as the baseline. A failed write
	// must not let its uncommitted pending tree become the next edit's baseline.
	if err := s.init(ctx); err != nil {
		return err
	}
	var base string
	if previous == "0" {
		var err error
		base, err = s.syncBase(ctx, "folder-initial")
		if err != nil {
			return err
		}
	} else {
		raw, err := s.syncCommand(ctx, "rev-parse", "refs/folder-history/"+digest(previous)).Output()
		if err != nil {
			return err
		}
		base = strings.TrimSpace(string(raw))
	}
	if err := s.syncCommand(ctx, "update-ref", syncRef("folder-history", "baseline"), base).Run(); err != nil {
		return err
	}
	snapshot.Changes = append([]crewsync.Change(nil), snapshot.Changes...)
	if err := s.EncodeSync(ctx, "folder-history", roots, &snapshot); err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(snapshot.Content.BundlePath))
	if err := s.syncCommand(ctx, "update-ref", "refs/folder-history/"+digest(revision), snapshot.Content.Head).Run(); err != nil {
		return err
	}
	return nil
}

// CopyFolderRevision streams one admitted historical file. A caller must check
// current folder membership and its own path/version metadata before calling.
func (s Store) CopyFolderRevision(ctx context.Context, revision, key string, dst io.Writer) error {
	if !crewsync.Allowed(key, false) {
		return fmt.Errorf("invalid folder history path")
	}
	cmd := s.syncCommand(ctx, "cat-file", "blob", "refs/folder-history/"+digest(revision)+":"+key)
	cmd.Stdout = dst
	return cmd.Run()
}

func (s Store) CompareFolderRevisions(ctx context.Context, left, right, key string) (string, error) {
	if !crewsync.Allowed(key, false) {
		return "", fmt.Errorf("invalid folder history path")
	}
	ref := func(revision string) (string, error) {
		if revision == "0" {
			return s.syncBase(ctx, "folder-initial")
		}
		return "refs/folder-history/" + digest(revision), nil
	}
	a, err := ref(left)
	if err != nil {
		return "", err
	}
	b, err := ref(right)
	if err != nil {
		return "", err
	}
	cmd := s.syncCommand(ctx, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", a, b, "--", key)
	out := &limitedOutput{}
	cmd.Stdout = out
	if err = cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}
