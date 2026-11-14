package filesnapshots

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// Bound Git's delta search and stream large blobs as well as Go's copies.
func (s Store) syncCommand(ctx context.Context, args ...string) *exec.Cmd {
	return s.command(ctx, append([]string{"-c", "core.bigFileThreshold=16m", "-c", "core.packedGitWindowSize=16m", "-c", "core.packedGitLimit=32m", "-c", "pack.windowMemory=16m", "-c", "pack.deltaCacheSize=16m", "-c", "pack.threads=1"}, args...)...)
}

// Sync uses the existing object store, but never the turn index or turn refs.
// Its caller supplies a complete sync-policy capture, not a partial review.
func syncRef(pair, name string) string { return "refs/sync/" + digest(pair) + "/" + name }

func (s Store) syncRoot(ctx context.Context, tree string) (string, error) {
	cmd := s.syncCommand(ctx, "commit-tree", tree, "-m", "AgentWorks sync baseline")
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=AgentWorks", "GIT_AUTHOR_EMAIL=sync@example.com", "GIT_COMMITTER_NAME=AgentWorks", "GIT_COMMITTER_EMAIL=sync@example.com", "GIT_AUTHOR_DATE=@0 +0000", "GIT_COMMITTER_DATE=@0 +0000")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
func (s Store) syncBase(ctx context.Context, pair string) (string, error) {
	if err := s.init(ctx); err != nil {
		return "", err
	}
	if out, err := s.syncCommand(ctx, "rev-parse", "--verify", syncRef(pair, "baseline")).Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	cmd := s.syncCommand(ctx, "mktree")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	base, err := s.syncRoot(ctx, strings.TrimSpace(string(out)))
	if err != nil {
		return "", err
	}
	return base, s.syncCommand(ctx, "update-ref", syncRef(pair, "baseline"), base).Run()
}

func (s Store) EncodeSync(ctx context.Context, pair string, roots crewroot.Roots, snapshot *crewsync.Snapshot) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	if err := agentworksclient.EnsurePrivateDirectory(s.dir()); err != nil {
		return err
	}
	base, err := s.syncBase(ctx, pair)
	if err != nil {
		return err
	}
	raw, err := s.syncCommand(ctx, "ls-tree", "-r", "--name-only", "-z", base).Output()
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	for _, key := range strings.Split(string(raw), "\x00") {
		if key != "" {
			existing[key] = true
		}
	}
	omitted := map[string]bool{}
	for _, key := range snapshot.Omitted {
		omitted[key] = true
	}
	changes := map[string]crewsync.Change{}
	for _, change := range snapshot.Changes {
		changes[change.Path] = change
	}
	keys := make([]string, 0, len(snapshot.Files))
	for key := range snapshot.Files {
		if !omitted[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	reader, writer := io.Pipe()
	cmd := s.syncCommand(ctx, "fast-import", "--quiet", "--force")
	cmd.Stdin = reader
	written := make(chan error, 1)
	go func() {
		write := func() error {
			if _, err := fmt.Fprintf(writer, "commit %s\ncommitter AgentWorks <sync@example.com> 0 +0000\ndata 4\nsync\nfrom %s\n", syncRef(pair, "pending"), base); err != nil {
				return err
			}
			for key := range existing {
				if _, ok := snapshot.Files[key]; !ok || omitted[key] {
					if _, err := fmt.Fprintf(writer, "D %s\n", gitQuotePath(key)); err != nil {
						return err
					}
				}
			}
			for _, key := range keys {
				if !crewsync.Allowed(key, false) {
					return fmt.Errorf("invalid sync path")
				}
				change, changed := changes[key]
				if !changed && existing[key] {
					continue
				}
				mode := "100644"
				if snapshot.Files[key].Executable {
					mode = "100755"
				}
				if _, err := fmt.Fprintf(writer, "M %s inline %s\ndata %d\n", mode, gitQuotePath(key), snapshot.Files[key].Size); err != nil {
					return err
				}
				var err error
				if changed {
					err = change.CopyTo(ctx, writer)
				} else {
					err = crewsync.CopyContent(ctx, writer, roots, key, snapshot.Files[key])
				}
				if err != nil {
					return err
				}
				if _, err := io.WriteString(writer, "\n"); err != nil {
					return err
				}
			}
			_, err := io.WriteString(writer, "\ndone\n")
			return err
		}
		err := write()
		_ = writer.CloseWithError(err)
		written <- err
	}()
	err = cmd.Run()
	_ = reader.Close()
	if writeErr := <-written; writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("capture project snapshot: %w", err)
	}
	head, err := s.syncCommand(ctx, "rev-parse", syncRef(pair, "pending")).Output()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.dir(), "sync-bundle-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	file := filepath.Join(dir, "content.bundle")
	if err = s.syncCommand(ctx, "bundle", "create", file, syncRef(pair, "pending"), "^"+base).Run(); err != nil {
		return err
	}
	if err = os.Chmod(file, 0600); err != nil {
		return err
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.Size() > crewsync.SyncLimits(ctx).Bytes+(8<<20) {
		return fmt.Errorf("project bundle exceeds transfer limit")
	}
	snapshot.Content = &crewsync.ContentSnapshot{Base: base, Head: strings.TrimSpace(string(head)), BundlePath: file}
	keep = true
	for i := range snapshot.Changes {
		snapshot.Changes[i].DataPath = ""
	}
	return nil
}
