package filesnapshots

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/safegit"
)

// Capture keeps one private Git index per checkout (and one for the Crew's
// loose files) as a persistent stat cache, so a capture reads only files whose
// stat Git reports changed, also after a restart.
//
// Contents stay raw by construction: the server reads every new or changed
// file itself through the confined os.Root and streams the bytes into
// fast-import, which stores data verbatim (no attributes, EOL, ident,
// encoding or filters). The index never supplies contents; it only remembers
// which blob a file had when its stat was recorded. update-index --index-info
// adds our blobs with empty stat data; update-index --refresh then records
// stat data only for files Git re-hashes to that same blob (a file edited
// after we read it stays dirty and is read again next time), and Git's
// racy-clean rules decide later whether that stat can be trusted. The
// store's info/attributes unsets every conversion so that re-hash is of the
// raw bytes too (otherwise a converted file would merely never become clean).
//
// Every capture works on a temporary copy of each index (refreshed first)
// that replaces it after the capture's commit and the head ref are written, so every blob an index names
// stays reachable and a killed capture leaves the previous index intact.

// headRef always points at the newest capture: it keeps every object the
// persisted indexes name reachable through retention cleanup.
const headRef = "refs/snapshot-index/head"
const indexWorkers = 4

// privateAttributes unsets every content conversion in the private store.
const privateAttributes = "* -text -eol -ident -filter -working-tree-encoding\n"

type checkout struct {
	prefix string   // Crew-relative directory; "" is the Crew root
	dir    string   // absolute work tree
	key    string   // private index name
	loose  bool     // files outside any listed checkout
	files  []string // validated candidates, relative to dir

	reused  bool            // a persisted index existed
	indexed map[string]bool // paths in the persisted index
	dirty   map[string]bool // indexed paths whose stat Git does not trust
	remove  []string
	add     []indexEntry
	count   int // entries after this capture
	tree    string
	changed bool
}

type indexEntry struct {
	mode, path string
	mark       int
	oid        string
}

func (c *checkout) full(p string) string {
	if c.prefix == "" {
		return p
	}
	return c.prefix + "/" + p
}

// checkoutKey names a checkout's private index. A renamed, moved or
// re-pointed checkout gets a new key, so it never reuses another's index.
func checkoutKey(rel, dir string) string {
	h := sha256.New()
	io.WriteString(h, rel+"\x00")
	dotGit := filepath.Join(dir, ".git")
	if real, err := filepath.EvalSymlinks(dotGit); err == nil {
		io.WriteString(h, real+"\x00")
		if info, err := os.Stat(real); err == nil && !info.IsDir() && info.Size() <= 4096 {
			if b, err := os.ReadFile(real); err == nil {
				h.Write(b) // a linked worktree's "gitdir: ..." line
			}
		}
	}
	return "repo-" + hex.EncodeToString(h.Sum(nil)[:16])
}

func (s Store) indexDir() string            { return filepath.Join(s.dir(), "index") }
func (s Store) indexFile(key string) string { return filepath.Join(s.indexDir(), key+".idx") }

// hasIndexes reports whether a capture has completed with the persisted
// indexes (a warm Crew), also across restarts.
func (s Store) hasIndexes() bool {
	_, err := os.Stat(filepath.Join(s.indexDir(), "ready"))
	return err == nil
}

func (s Store) indexCommand(ctx context.Context, c *checkout, index string, args ...string) *exec.Cmd {
	return safegit.PrivateStore(ctx, s.Root, s.gitDir(), c.dir, index, args...)
}

// cleanStale removes what a killed capture can leave: temporary indexes,
// marks, Git lock files and fast-import temporary packs. Every writer of the
// private store holds the Crew gate, so anything left now is stale.
func (s Store) cleanStale() {
	if entries, err := os.ReadDir(s.indexDir()); err == nil {
		for _, e := range entries {
			if n := e.Name(); strings.HasSuffix(n, ".tmp") || strings.HasSuffix(n, ".lock") {
				_ = os.Remove(filepath.Join(s.indexDir(), n))
			}
		}
	}
	_ = os.Remove(filepath.Join(s.gitDir(), "packed-refs.lock"))
	_ = filepath.WalkDir(filepath.Join(s.gitDir(), "refs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".lock") {
			_ = os.Remove(p)
		}
		return nil
	})
	if tmp, _ := filepath.Glob(filepath.Join(s.gitDir(), "objects", "pack", "tmp_*")); len(tmp) > 0 {
		for _, p := range tmp {
			_ = os.Remove(p)
		}
	}
}

func parallel(n int, fn func(int) error) error {
	sem := make(chan struct{}, indexWorkers)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = fn(i)
		}(i)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// status refreshes a temporary copy of a checkout's persisted index, then
// reads which paths it holds and which of them Git reports changed. The
// refresh records new stat data for files whose stat changed but whose bytes
// still match (a touch, a checkout of the same content, a racily clean
// entry), so they are not re-hashed on every later capture; the copy replaces
// the persisted index only when the capture commits.
func (s Store) status(ctx context.Context, c *checkout) error {
	c.indexed, c.dirty = map[string]bool{}, map[string]bool{}
	index := s.indexFile(c.key)
	if _, err := os.Stat(index); err != nil {
		return nil
	}
	tmp := index + ".tmp"
	if err := copyIndex(index, tmp); err != nil {
		return err
	}
	c.reused, c.changed = true, true
	// Git lstats the work-tree paths itself and follows a symlinked parent
	// directory; that only decides changed or unchanged. No content is
	// imported from it: contents come only from the confined reads below.
	_, err := s.runIndex(ctx, c, tmp, nil, "update-index", "-q", "--ignore-missing", "--refresh")
	var out []byte
	if err == nil {
		out, err = s.runIndex(ctx, c, tmp, nil, "ls-files", "-c", "-m", "-t", "-z")
	}
	if err != nil {
		if ctx.Err() == nil {
			_ = os.Remove(index) // unreadable: rebuild this checkout
		}
		return fmt.Errorf("read snapshot index: %w", err)
	}
	for _, e := range bytes.Split(out, []byte{0}) {
		if len(e) < 3 || e[1] != ' ' {
			continue
		}
		if p := string(e[2:]); e[0] == 'C' {
			c.dirty[p] = true
		} else {
			c.indexed[p] = true
		}
	}
	return nil
}

func (s Store) runIndex(ctx context.Context, c *checkout, index string, stdin []byte, args ...string) ([]byte, error) {
	cmd := s.indexCommand(ctx, c, index, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("snapshot index %s: %w (%s)", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// copyIndex copies the persisted index for an update, keeping its mtime:
// Git's racy-clean check compares entry times with the index file's time.
func copyIndex(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	if err = os.WriteFile(to, data, 0600); err != nil {
		return err
	}
	return os.Chtimes(to, info.ModTime(), info.ModTime())
}

// writeCheckout applies removals and new blobs to the checkout's temporary
// index (refreshed by status, or new) and writes its tree.
func (s Store) writeCheckout(ctx context.Context, c *checkout) error {
	index := s.indexFile(c.key) + ".tmp"
	if !c.reused && len(c.add) == 0 {
		return nil
	}
	c.changed = true
	if len(c.remove) > 0 {
		if _, err := s.runIndex(ctx, c, index, []byte(strings.Join(c.remove, "\x00")+"\x00"), "update-index", "-z", "--force-remove", "--stdin"); err != nil {
			return err
		}
	}
	if len(c.add) > 0 {
		var in bytes.Buffer
		for _, e := range c.add {
			fmt.Fprintf(&in, "%s %s\t%s\x00", e.mode, e.oid, e.path)
		}
		if _, err := s.runIndex(ctx, c, index, in.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return err
		}
		// Record stat data, only for files that still hash to our blob. As in
		// status, Git's own lstat/re-hash here decides only changed or not.
		if _, err := s.runIndex(ctx, c, index, nil, "update-index", "-q", "--ignore-missing", "--refresh"); err != nil {
			return err
		}
	}
	if c.count == 0 {
		return nil
	}
	out, err := s.runIndex(ctx, c, index, nil, "write-tree")
	if err != nil {
		if ctx.Err() == nil {
			_ = os.Remove(s.indexFile(c.key)) // names a missing object: rebuild this checkout
		}
		return err
	}
	c.tree = strings.TrimSpace(string(out))
	return nil
}

// compose writes one commit holding every checkout tree under its
// Crew-relative prefix plus the loose files, and moves headRef to it.
func (s Store) compose(ctx context.Context, ref string, checkouts []*checkout) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "commit %s\nmark :1\ncommitter AgentWorks <snapshot@example.com> %d +0000\ndata 14\nFile snapshot\n\ndeleteall\n", ref, time.Now().Unix())
	var loose *checkout
	var mounted []*checkout
	rootMounted := false
	for _, c := range checkouts {
		if c.tree == "" {
			continue
		}
		if c.loose {
			loose = c
			continue
		}
		mounted = append(mounted, c)
		rootMounted = rootMounted || c.prefix == ""
	}
	sort.Slice(mounted, func(i, j int) bool {
		if len(mounted[i].prefix) != len(mounted[j].prefix) {
			return len(mounted[i].prefix) < len(mounted[j].prefix)
		}
		return mounted[i].prefix < mounted[j].prefix
	})
	// Loose files are the base tree unless a checkout sits at the root or
	// above a loose file (a walked fallback checkout); then each is added.
	looseBase := loose != nil && !rootMounted
	if looseBase {
		prefixes := map[string]bool{}
		for _, c := range mounted {
			prefixes[c.prefix] = true
		}
		for _, f := range loose.files {
			for dir := path.Dir(f); dir != "." && looseBase; dir = path.Dir(dir) {
				looseBase = !prefixes[dir]
			}
		}
	}
	if looseBase {
		fmt.Fprintf(&b, "M 040000 %s \"\"\n", loose.tree)
	}
	for _, c := range mounted {
		fmt.Fprintf(&b, "M 040000 %s %s\n", c.tree, strconv.Quote(c.prefix))
	}
	if loose != nil && !looseBase {
		index := s.indexFile(loose.key)
		if loose.changed {
			index += ".tmp"
		}
		out, err := s.runIndex(ctx, loose, index, nil, "ls-files", "-s", "-z")
		if err != nil {
			return err
		}
		for _, e := range bytes.Split(out, []byte{0}) {
			meta, name, ok := strings.Cut(string(e), "\t")
			fields := strings.Fields(meta)
			if ok && len(fields) == 3 {
				fmt.Fprintf(&b, "M %s %s %s\n", fields[0], fields[1], strconv.Quote(name))
			}
		}
	}
	if ref != headRef {
		fmt.Fprintf(&b, "\nreset %s\nfrom :1\n", headRef)
	}
	b.WriteString("\ndone\n")
	cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--force", "--date-format=raw")
	cmd.Stdin = &b
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("snapshot commit failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type pending struct {
	c          *checkout
	path, full string
	size       int64
}

// capture captures the Crew into ref, importing at most budget bytes of
// new contents. Paths skipped for size, budget or read errors are left out of
// the tree and out of every index, so no older blob stands in for them.
func (s Store) capture(ctx context.Context, ref, prior string, stats *CaptureStats, skippedState map[string]string, budget int64) (string, []string, []string, error) {
	s.cleanStale()
	if err := os.MkdirAll(s.indexDir(), 0700); err != nil {
		return "", nil, nil, err
	}
	checkouts, omitted, err := s.candidates(ctx, stats)
	if err != nil {
		return "", nil, nil, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return "", nil, nil, err
	}
	defer root.Close()
	all := []string{}
	for _, c := range checkouts {
		for _, f := range c.files {
			all = append(all, c.full(f))
		}
	}
	stats.Eligible = len(all)
	if prior != "" {
		omitted = append(omitted, s.vanishedOnDisk(ctx, root, prior, all)...)
	}
	if err := parallel(len(checkouts), func(i int) error { return s.status(ctx, checkouts[i]) }); err != nil {
		return "", nil, nil, err
	}
	var todo []pending
	for _, c := range checkouts {
		if c.reused {
			stats.IndexesReused++
		} else if len(c.files) > 0 {
			stats.IndexesNew++
		}
		want := make(map[string]bool, len(c.files))
		for _, f := range c.files {
			want[f] = true
			if c.indexed[f] && !c.dirty[f] {
				stats.Reused++
				stats.Captured++
				c.count++
				continue
			}
			todo = append(todo, pending{c: c, path: f, full: c.full(f)})
		}
		// Deleted, newly ignored or newly excluded paths leave the index.
		for p := range c.indexed {
			if !want[p] {
				c.remove = append(c.remove, p)
			}
		}
		sort.Strings(c.remove)
	}
	skipped := []string{}
	drop := func(w pending) {
		if w.c.indexed[w.path] {
			w.c.remove = append(w.c.remove, w.path)
		}
	}
	// Metadata is evidence of an observed change, not a captured blob. Keep it
	// only for skipped regular files; unreadable/non-regular paths stay unknown.
	rememberSkipped := func(path string, info os.FileInfo) {
		if info.Mode().IsRegular() {
			skippedState[path] = fmt.Sprintf("%d:%d:%d", info.Size(), info.ModTime().UnixNano(), info.Mode())
		}
	}
	// Concurrent deletes are ordinary working-tree changes. An unreadable path
	// is omitted on both sides of the comparison, never shown as a deletion.
	skipRead := func(w pending, err error) {
		drop(w)
		if !errors.Is(err, fs.ErrNotExist) {
			skipped = append(skipped, w.full)
			stats.ReadSkipped++
		}
	}
	present := todo[:0]
	for _, w := range todo {
		if err := ctx.Err(); err != nil {
			return "", nil, nil, err
		}
		info, err := root.Lstat(w.full)
		if err != nil {
			skipRead(w, err)
			continue
		}
		w.size = info.Size()
		present = append(present, w)
	}
	// A large asset encountered first must not starve ordinary source files.
	sort.Slice(present, func(i, j int) bool {
		if present[i].size == present[j].size {
			return present[i].full < present[j].full
		}
		return present[i].size < present[j].size
	})
	if err := s.importBlobs(ctx, root, present, budget, stats, &skipped, rememberSkipped, skipRead, drop); err != nil {
		return "", nil, nil, err
	}
	if err := parallel(len(checkouts), func(i int) error { return s.writeCheckout(ctx, checkouts[i]) }); err != nil {
		return "", nil, nil, err
	}
	for _, c := range checkouts {
		stats.Removed += len(c.remove)
	}
	if err := s.compose(ctx, ref, checkouts); err != nil {
		if ctx.Err() == nil {
			for _, c := range checkouts {
				_ = os.Remove(s.indexFile(c.key)) // a tree is unusable: rebuild
			}
		}
		return "", nil, nil, err
	}
	// The commit and headRef now hold every blob the new indexes name.
	keep := map[string]bool{}
	for _, c := range checkouts {
		keep[c.key+".idx"] = true
		if c.changed {
			if c.count == 0 {
				_ = os.Remove(s.indexFile(c.key) + ".tmp")
				_ = os.Remove(s.indexFile(c.key))
				continue
			}
			if err := os.Rename(s.indexFile(c.key)+".tmp", s.indexFile(c.key)); err != nil {
				return "", nil, nil, err
			}
		}
	}
	if entries, err := os.ReadDir(s.indexDir()); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".idx") && !keep[e.Name()] {
				_ = os.Remove(filepath.Join(s.indexDir(), e.Name())) // checkout gone
			}
		}
	}
	if err := os.WriteFile(filepath.Join(s.indexDir(), "ready"), nil, 0600); err != nil {
		return "", nil, nil, err
	}
	out, err := s.command(ctx, "rev-parse", "--verify", ref).Output()
	return strings.TrimSpace(string(out)), skipped, omitted, err
}

// importBlobs reads new and changed files through the confined root, smallest
// first within budget, and imports their exact bytes as blobs.
func (s Store) importBlobs(ctx context.Context, root *os.Root, present []pending, budget int64, stats *CaptureStats, skipped *[]string, rememberSkipped func(string, os.FileInfo), skipRead func(pending, error), drop func(pending)) error {
	if len(present) == 0 {
		return nil
	}
	marks := filepath.Join(s.indexDir(), "marks.tmp")
	defer os.Remove(marks)
	cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--export-marks="+marks)
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	w := bufio.NewWriterSize(input, 1<<20)
	var total int64
	mark := 0
	added := []indexEntry{}
	owners := []*checkout{}
	blob := func(p pending, mode string, data []byte) error {
		mark++
		if _, e := fmt.Fprintf(w, "blob\nmark :%d\ndata %d\n", mark, len(data)); e != nil {
			return e
		}
		if _, e := w.Write(data); e != nil {
			return e
		}
		if e := w.WriteByte('\n'); e != nil {
			return e
		}
		added = append(added, indexEntry{mode: mode, path: p.path, mark: mark})
		owners = append(owners, p.c)
		stats.Changed++
		stats.Captured++
		p.c.count++
		return nil
	}
	skip := func(p pending, info os.FileInfo, budgetSkip bool) {
		drop(p)
		rememberSkipped(p.full, info)
		*skipped = append(*skipped, p.full)
		if budgetSkip {
			stats.BudgetSkipped++
		} else {
			stats.SizeSkipped++
		}
	}
	for _, p := range present {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		info, e := root.Lstat(p.full)
		if e != nil {
			skipRead(p, e)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := root.Readlink(p.full)
			if e != nil {
				skipRead(p, e)
				continue
			}
			err = blob(p, "120000", []byte(target))
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
			skip(p, info, false)
			continue
		}
		if total+info.Size() > budget {
			skip(p, info, true)
			continue
		}
		f, e := root.Open(p.full)
		if e != nil {
			skipRead(p, e)
			continue
		}
		// The open handle's size/type is authoritative if the file changed during enumeration.
		actual, e := f.Stat()
		if e != nil {
			f.Close()
			skipRead(p, e)
			continue
		}
		if !actual.Mode().IsRegular() || actual.Size() > MaxFileBytes {
			f.Close()
			skip(p, actual, false)
			continue
		}
		data, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		f.Close()
		if e != nil {
			skipRead(p, e)
			continue
		}
		if len(data) > MaxFileBytes || total+int64(len(data)) > budget {
			skip(p, actual, len(data) <= MaxFileBytes)
			continue
		}
		total += int64(len(data))
		stats.ImportedBytes += int64(len(data))
		mode := "100644"
		if actual.Mode()&0111 != 0 {
			mode = "100755"
		}
		err = blob(p, mode, data)
	}
	if err == nil {
		if _, err = w.WriteString("done\n"); err == nil {
			err = w.Flush()
		}
	}
	input.Close()
	if err != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return fmt.Errorf("snapshot import failed: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	if mark == 0 {
		return nil
	}
	raw, err := os.ReadFile(marks)
	if err != nil {
		return err
	}
	oids := make(map[int]string, mark)
	for _, line := range strings.Split(string(raw), "\n") {
		name, oid, ok := strings.Cut(line, " ")
		if n, e := strconv.Atoi(strings.TrimPrefix(name, ":")); ok && e == nil {
			oids[n] = oid
		}
	}
	for i, e := range added {
		if e.oid = oids[e.mark]; e.oid == "" {
			return errors.New("snapshot import lost a blob")
		}
		owners[i].add = append(owners[i].add, e)
	}
	return nil
}
