package filesnapshots

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
					if _, err := fmt.Fprintf(writer, "D %s\n", strconv.Quote(key)); err != nil {
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
				if _, err := fmt.Fprintf(writer, "M %s inline %s\ndata %d\n", mode, strconv.Quote(key), snapshot.Files[key].Size); err != nil {
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

// DecodeSync imports only one named ref. It validates the entire tree before
// exposing bytes to rooted Apply; Git never checks out onto the working files.
func (s Store) DecodeSync(ctx context.Context, pair string, snapshot *crewsync.Snapshot) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	content := snapshot.Content
	if content == nil || !objectName(content.Base) || !objectName(content.Head) {
		return fmt.Errorf("invalid project bundle")
	}
	if err := agentworksclient.EnsurePrivateDirectory(s.dir()); err != nil {
		return err
	}
	base, err := s.syncBase(ctx, pair)
	if err != nil {
		return err
	}
	// The old pending/incoming ref pins prerequisites across an interrupted ack.
	if base != content.Base && s.syncCommand(ctx, "cat-file", "-e", content.Base+"^{commit}").Run() != nil {
		return fmt.Errorf("sync baseline is unavailable; retry the pending transfer")
	}
	file := content.BundlePath
	info, e := os.Lstat(file)
	if e != nil || !info.Mode().IsRegular() || info.Size() > crewsync.SyncLimits(ctx).Bytes+(8<<20) {
		return fmt.Errorf("invalid project bundle file")
	}
	if err = s.syncCommand(ctx, "bundle", "verify", file).Run(); err != nil {
		return fmt.Errorf("project bundle does not match its baseline")
	}
	heads, err := s.syncCommand(ctx, "bundle", "list-heads", file).Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(heads)) != content.Head+" "+syncRef(pair, "pending") {
		return fmt.Errorf("unexpected project bundle references")
	}
	if err = s.syncCommand(ctx, "-c", "protocol.file.allow=always", "-c", "transfer.fsckObjects=true", "-c", "core.protectHFS=true", "-c", "core.protectNTFS=true", "fetch", "--no-tags", "--no-write-fetch-head", file, "+"+syncRef(pair, "pending")+":"+syncRef(pair, "incoming")).Run(); err != nil {
		return fmt.Errorf("cannot import project bundle")
	}
	parents, err := s.syncCommand(ctx, "rev-list", "--parents", "-n", "1", content.Head).Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(parents)) != content.Head+" "+content.Base {
		return fmt.Errorf("unexpected project snapshot ancestry")
	}
	if base != content.Base {
		tree, e := s.syncCommand(ctx, "rev-parse", content.Head+"^{tree}").Output()
		if e != nil {
			return e
		}
		acknowledged, e := s.syncRoot(ctx, strings.TrimSpace(string(tree)))
		if e != nil {
			return e
		}
		if acknowledged != base {
			return fmt.Errorf("project baseline changed; finish the pending transfer")
		}
	}
	return s.syncBytes(ctx, snapshot)
}

func objectName(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

func (s Store) syncBytes(ctx context.Context, snapshot *crewsync.Snapshot) error {
	raw, err := s.syncCommand(ctx, "ls-tree", "-r", "-z", "--full-tree", snapshot.Content.Head).Output()
	if err != nil {
		return err
	}
	omitted := map[string]bool{}
	for _, key := range snapshot.Omitted {
		if !crewsync.Allowed(key, false) {
			return fmt.Errorf("invalid omitted path")
		}
		omitted[key] = true
	}
	changed := map[string]int{}
	for i, c := range snapshot.Changes {
		if _, ok := changed[c.Path]; ok {
			return fmt.Errorf("duplicate project change")
		}
		changed[c.Path] = i
	}
	type entry struct {
		path, oid string
		exec      bool
	}
	var entries []entry
	seen := map[string]bool{}
	folded := map[string]string{}
	for _, line := range bytes.Split(raw, []byte{0}) {
		if len(line) == 0 {
			continue
		}
		meta, key, ok := strings.Cut(string(line), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || !crewsync.Allowed(key, false) || omitted[key] {
			return fmt.Errorf("unsupported or excluded project tree entry")
		}
		for p := key; p != "."; p = filepath.ToSlash(filepath.Dir(p)) {
			lower := strings.ToLower(p)
			if old, ok := folded[lower]; ok && old != p {
				return fmt.Errorf("project paths differ only by case")
			}
			folded[lower] = p
		}
		if _, ok := snapshot.Files[key]; !ok {
			return fmt.Errorf("unlisted project file")
		}
		seen[key] = true
		entries = append(entries, entry{key, fields[2], fields[0] == "100755"})
	}
	if len(entries) > crewsync.SyncLimits(ctx).Files {
		return fmt.Errorf("too many project files")
	}
	for key := range snapshot.Files {
		if !seen[key] && !omitted[key] {
			return fmt.Errorf("project snapshot is incomplete")
		}
	}
	if snapshot.DecodedDir != "" {
		_ = os.RemoveAll(snapshot.DecodedDir)
		snapshot.DecodedDir = ""
	}
	stage, err := os.MkdirTemp(s.dir(), "sync-decoded-")
	if err != nil {
		return err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()
	cmd := s.syncCommand(ctx, "cat-file", "--batch")
	var ids strings.Builder
	for _, e := range entries {
		ids.WriteString(e.oid + "\n")
	}
	cmd.Stdin = strings.NewReader(ids.String())
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() {
		pipe.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(pipe)
	var total int64
	for _, e := range entries {
		header, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		parts := strings.Fields(header)
		if len(parts) != 3 || parts[1] != "blob" {
			return fmt.Errorf("invalid project blob")
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || size < 0 || size > crewsync.SyncLimits(ctx).FileBytes {
			return fmt.Errorf("project file exceeds 64 MiB")
		}
		total += size
		if total > crewsync.SyncLimits(ctx).Bytes {
			return fmt.Errorf("project exceeds 256 MiB")
		}
		f := snapshot.Files[e.path]
		if f.Size != size || f.Executable != e.exec {
			return fmt.Errorf("project file does not match its snapshot")
		}
		var out *os.File
		dst := io.Writer(io.Discard)
		i, changed := changed[e.path]
		if changed {
			if snapshot.Changes[i].After == nil || *snapshot.Changes[i].After != f {
				return fmt.Errorf("invalid changed file in project tree")
			}
			out, err = os.CreateTemp(stage, "file-")
			if err != nil {
				return err
			}
			dst = out
		}
		err = crewsync.CopyChecked(ctx, dst, io.LimitReader(reader, size), f)
		if out != nil {
			closeErr := out.Close()
			if err == nil {
				err = closeErr
			}
			snapshot.Changes[i].DataPath = out.Name()
		}
		if err != nil {
			return err
		}
		separator, err := reader.ReadByte()
		if err != nil || separator != '\n' {
			return fmt.Errorf("invalid project blob framing")
		}
	}
	if err = cmd.Wait(); err != nil {
		return err
	}
	keepStage = true
	snapshot.DecodedDir = stage
	return nil
}

// Pin the acknowledged tree as a deterministic parentless commit. Turn pruning
// cannot delete this namespace; at most one pending/incoming prerequisite is kept.
func (s Store) AckSync(ctx context.Context, pair string, content *crewsync.ContentSnapshot) error {
	if content == nil || !objectName(content.Head) {
		return fmt.Errorf("missing project receipt")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	tree, err := s.syncCommand(ctx, "rev-parse", content.Head+"^{tree}").Output()
	if err != nil {
		return err
	}
	root, err := s.syncRoot(ctx, strings.TrimSpace(string(tree)))
	if err != nil {
		return err
	}
	return s.syncCommand(ctx, "update-ref", syncRef(pair, "baseline"), root).Run()
}
