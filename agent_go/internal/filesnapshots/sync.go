package filesnapshots

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// Sync uses the existing object store, but never the turn index or turn refs.
// Its caller supplies a complete sync-policy capture, not a partial review.
func syncRef(pair, name string) string { return "refs/sync/" + digest(pair) + "/" + name }

func (s Store) syncRoot(ctx context.Context, tree string) (string, error) {
	cmd := s.command(ctx, "commit-tree", tree, "-m", "AgentWorks sync baseline")
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_NAME=AgentWorks", "GIT_AUTHOR_EMAIL=sync@example.com", "GIT_COMMITTER_NAME=AgentWorks", "GIT_COMMITTER_EMAIL=sync@example.com", "GIT_AUTHOR_DATE=@0 +0000", "GIT_COMMITTER_DATE=@0 +0000")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
func (s Store) syncBase(ctx context.Context, pair string) (string, error) {
	if err := s.init(ctx); err != nil {
		return "", err
	}
	if out, err := s.command(ctx, "rev-parse", "--verify", syncRef(pair, "baseline")).Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	cmd := s.command(ctx, "mktree")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	base, err := s.syncRoot(ctx, strings.TrimSpace(string(out)))
	if err != nil {
		return "", err
	}
	return base, s.command(ctx, "update-ref", syncRef(pair, "baseline"), base).Run()
}

func (s Store) EncodeSync(ctx context.Context, pair string, snapshot *crewsync.Snapshot) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	base, err := s.syncBase(ctx, pair)
	if err != nil {
		return err
	}
	var input bytes.Buffer
	fmt.Fprintf(&input, "commit %s\ncommitter AgentWorks <sync@example.com> 0 +0000\ndata 4\nsync\nfrom %s\ndeleteall\n", syncRef(pair, "pending"), base)
	keys := make([]string, 0, len(snapshot.Contents))
	for key := range snapshot.Contents {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !crewsync.Allowed(key, false) {
			return fmt.Errorf("invalid sync path")
		}
		mode := "100644"
		if snapshot.Files[key].Executable {
			mode = "100755"
		}
		data := snapshot.Contents[key]
		fmt.Fprintf(&input, "M %s inline %s\ndata %d\n", mode, strconv.Quote(key), len(data))
		input.Write(data)
		input.WriteByte('\n')
	}
	input.WriteString("\ndone\n")
	cmd := s.command(ctx, "fast-import", "--quiet", "--force")
	cmd.Stdin = &input
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("capture project snapshot: %w", err)
	}
	head, err := s.command(ctx, "rev-parse", syncRef(pair, "pending")).Output()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.dir(), "sync-bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "content.bundle")
	if err = s.command(ctx, "bundle", "create", file, syncRef(pair, "pending"), "^"+base).Run(); err != nil {
		return err
	}
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.Size() > publication.MaxBytes+8<<20 {
		return fmt.Errorf("project bundle exceeds transfer limit")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	snapshot.Content = &crewsync.ContentSnapshot{Base: base, Head: strings.TrimSpace(string(head)), Bundle: data}
	snapshot.Contents = nil
	for i := range snapshot.Changes {
		snapshot.Changes[i].Data = nil
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
	if content == nil || !objectName(content.Base) || !objectName(content.Head) || len(content.Bundle) > publication.MaxBytes+8<<20 {
		return fmt.Errorf("invalid project bundle")
	}
	base, err := s.syncBase(ctx, pair)
	if err != nil {
		return err
	}
	// The old pending/incoming ref pins prerequisites across an interrupted ack.
	if base != content.Base && s.command(ctx, "cat-file", "-e", content.Base+"^{commit}").Run() != nil {
		return fmt.Errorf("sync baseline is unavailable; retry the pending transfer")
	}
	dir, err := os.MkdirTemp(s.dir(), "sync-incoming-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "content.bundle")
	if err = os.WriteFile(file, content.Bundle, 0600); err != nil {
		return err
	}
	if err = s.command(ctx, "bundle", "verify", file).Run(); err != nil {
		return fmt.Errorf("project bundle does not match its baseline")
	}
	heads, err := s.command(ctx, "bundle", "list-heads", file).Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(heads)) != content.Head+" "+syncRef(pair, "pending") {
		return fmt.Errorf("unexpected project bundle references")
	}
	if err = s.command(ctx, "-c", "protocol.file.allow=always", "-c", "transfer.fsckObjects=true", "-c", "core.protectHFS=true", "-c", "core.protectNTFS=true", "fetch", "--no-tags", "--no-write-fetch-head", file, "+"+syncRef(pair, "pending")+":"+syncRef(pair, "incoming")).Run(); err != nil {
		return fmt.Errorf("cannot import project bundle")
	}
	parents, err := s.command(ctx, "rev-list", "--parents", "-n", "1", content.Head).Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(parents)) != content.Head+" "+content.Base {
		return fmt.Errorf("unexpected project snapshot ancestry")
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
	raw, err := s.command(ctx, "ls-tree", "-r", "-z", "--full-tree", snapshot.Content.Head).Output()
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
	if len(entries) > publication.MaxFiles {
		return fmt.Errorf("too many project files")
	}
	for key := range snapshot.Files {
		if !seen[key] && !omitted[key] {
			return fmt.Errorf("project snapshot is incomplete")
		}
	}
	cmd := s.command(ctx, "cat-file", "--batch")
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
		if err != nil || size < 0 || size > publication.MaxFileBytes {
			return fmt.Errorf("project file exceeds 64 MiB")
		}
		total += size
		if total > publication.MaxBytes {
			return fmt.Errorf("project exceeds 256 MiB")
		}
		data := make([]byte, int(size)+1)
		if _, err = io.ReadFull(reader, data); err != nil {
			return err
		}
		if data[len(data)-1] != '\n' {
			return fmt.Errorf("invalid project blob framing")
		}
		data = data[:len(data)-1]
		hash := sha256.Sum256(data)
		f := snapshot.Files[e.path]
		if f.Hash != hex.EncodeToString(hash[:]) || f.Size != size || f.Executable != e.exec {
			return fmt.Errorf("project file does not match its snapshot")
		}
		if i, ok := changed[e.path]; ok {
			if snapshot.Changes[i].After == nil {
				return fmt.Errorf("deleted file in project tree")
			}
			snapshot.Changes[i].Data = data
		}
	}
	if err = cmd.Wait(); err != nil {
		return err
	}
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
	tree, err := s.command(ctx, "rev-parse", content.Head+"^{tree}").Output()
	if err != nil {
		return err
	}
	root, err := s.syncRoot(ctx, strings.TrimSpace(string(tree)))
	if err != nil {
		return err
	}
	return s.command(ctx, "update-ref", syncRef(pair, "baseline"), root).Run()
}
