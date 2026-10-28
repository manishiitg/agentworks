package filesnapshots

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GroupFileCap bounds the files listed per group; the rest is a count.
const GroupFileCap = 50

// PullRequest is the GitHub state of a group's branch. State is open, merged,
// closed, connect_github (the Crew owner has no connection) or unavailable.
type PullRequest struct {
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
	State  string `json:"state"`
	Checks string `json:"checks,omitempty"`
	Review string `json:"review,omitempty"`
}

// Group is one repository (or the non-Git Crew files) in a reply's changes.
type Group struct {
	Kind        string       `json:"kind"` // repo | crew_files
	Root        string       `json:"root"` // Crew-relative checkout folder; "" for the Crew root
	Name        string       `json:"name"`
	Branch      string       `json:"branch,omitempty"`
	NewCheckout bool         `json:"new_checkout"`
	GitHubRepo  string       `json:"github_repo,omitempty"` // owner/name when origin is on GitHub
	Files       []Change     `json:"files"`
	TotalFiles  int          `json:"total_files"`
	MoreCount   int          `json:"more_count"`
	PR          *PullRequest `json:"pr,omitempty"`
}

// gitIn runs read-only Git in a user checkout. The checkout is untrusted:
// hooks, fsmonitor, external diff/textconv and user/system config are off.
func gitIn(ctx context.Context, dir string, args ...string) *exec.Cmd {
	flags := []string{"-c", "safe.directory=*", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.pager=cat", "-c", "protocol.allow=never", "--no-optional-locks"}
	cmd := exec.CommandContext(ctx, "git", append(flags, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	return cmd
}

// checkoutRoot returns the deepest folder at or above file (inside root) that
// has its own .git directory or file, as a root-relative path.
func checkoutRoot(root, file string, cache map[string]string) string {
	dir := path.Dir(file)
	var walked []string
	found := ""
	for {
		if v, ok := cache[dir]; ok {
			found = v
			break
		}
		walked = append(walked, dir)
		abs := filepath.Join(root, filepath.FromSlash(dir), ".git")
		if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink == 0 {
			found = "repo:" + dir
			break
		}
		if dir == "." || dir == "" {
			found = ""
			break
		}
		dir = path.Dir(dir)
	}
	for _, d := range walked {
		cache[d] = found
	}
	return found
}

// GroupChanges groups a reply's changes by Git checkout. A checkout that the
// reply added whole collapses to one group listing only files edited in it
// (working tree vs HEAD); other changes are one "Crew files (not in Git)" group.
// allow takes root-relative paths and is the caller's current file guard.
func GroupChanges(ctx context.Context, root string, changes []Change, allow func(string) bool) []Group {
	cache := map[string]string{}
	byRepo := map[string][]Change{}
	var plain []Change
	for _, c := range changes {
		if key := checkoutRoot(root, c.Path, cache); key != "" {
			dir := strings.TrimPrefix(key, "repo:")
			if dir == "." {
				dir = ""
			}
			byRepo[dir] = append(byRepo[dir], c)
		} else {
			plain = append(plain, c)
		}
	}
	dirs := make([]string, 0, len(byRepo))
	for d := range byRepo {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	out := []Group{}
	for _, d := range dirs {
		out = append(out, repoGroup(ctx, root, d, byRepo[d], allow))
	}
	if len(plain) > 0 {
		out = append(out, capGroup(Group{Kind: "crew_files", Name: "Crew files (not in Git)", Files: plain}))
	}
	return out
}

func capGroup(g Group) Group {
	sort.Slice(g.Files, func(i, j int) bool { return g.Files[i].Path < g.Files[j].Path })
	g.TotalFiles = len(g.Files)
	if len(g.Files) > GroupFileCap {
		g.MoreCount = len(g.Files) - GroupFileCap
		g.Files = g.Files[:GroupFileCap]
	}
	if g.Files == nil {
		g.Files = []Change{}
	}
	return g
}

func repoGroup(ctx context.Context, root, dir string, changes []Change, allow func(string) bool) Group {
	abs := filepath.Join(root, filepath.FromSlash(dir))
	name := path.Base(dir)
	if dir == "" {
		name = "Crew folder"
	}
	g := Group{Kind: "repo", Root: dir, Name: name}
	allAdded := true
	for _, c := range changes {
		if c.Status != "A" {
			allAdded = false
		}
	}
	// A checkout the reply created lists its files as adds. Treat it as new
	// when everything is an add and the adds cover the checkout (a few files
	// may be skipped by capture rules, so a small tracked remainder is fine).
	tracked := 0
	if out, err := gitIn(ctx, abs, "ls-files", "-z").Output(); err == nil {
		tracked = bytes.Count(out, []byte{0})
	}
	g.NewCheckout = allAdded && (len(changes) >= tracked || len(changes) >= 20)
	if out, err := gitIn(ctx, abs, "symbolic-ref", "--short", "-q", "HEAD").Output(); err == nil {
		g.Branch = strings.TrimSpace(string(out))
	}
	if g.Branch == "" {
		if out, err := gitIn(ctx, abs, "rev-parse", "--short", "HEAD").Output(); err == nil {
			g.Branch = "detached@" + strings.TrimSpace(string(out))
		}
	}
	if out, err := gitIn(ctx, abs, "config", "--get", "remote.origin.url").Output(); err == nil {
		g.GitHubRepo = GitHubRepo(string(out))
	}
	files, ok := workingTreeEdits(ctx, abs, dir, allow)
	switch {
	case ok && (len(files) > 0 || g.NewCheckout):
		g.Files = files
	default:
		// Nothing differs from HEAD now (for example the reply committed):
		// show what the reply changed, as before.
		g.Files = changes
	}
	return capGroup(g)
}

// workingTreeEdits lists files differing from HEAD in one checkout.
func workingTreeEdits(ctx context.Context, abs, dir string, allow func(string) bool) ([]Change, bool) {
	raw, err := gitIn(ctx, abs, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all").Output()
	if err != nil {
		return nil, false
	}
	var files []Change
	for _, row := range bytes.Split(raw, []byte{0}) {
		if len(row) < 4 {
			continue
		}
		xy, p := string(row[:2]), path.Join(dir, string(row[3:]))
		if xy == "!!" || !allow(p) {
			continue
		}
		status := "M"
		switch {
		case xy == "??" || strings.Contains(xy, "A"):
			status = "A"
		case strings.Contains(xy, "D"):
			status = "D"
		}
		files = append(files, Change{Path: p, Status: status})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	byPath := map[string]*Change{}
	for i := range files {
		byPath[files[i].Path] = &files[i]
	}
	if stats, err := gitIn(ctx, abs, "diff", "HEAD", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat", "-z", "--").Output(); err == nil {
		for _, row := range bytes.Split(stats, []byte{0}) {
			f := bytes.SplitN(row, []byte{'\t'}, 3)
			if len(f) != 3 {
				continue
			}
			c := byPath[path.Join(dir, string(f[2]))]
			if c == nil {
				continue
			}
			if string(f[0]) == "-" {
				c.Binary = true
				continue
			}
			a, aerr := strconv.Atoi(string(f[0]))
			r, rerr := strconv.Atoi(string(f[1]))
			if aerr == nil && rerr == nil {
				c.Added, c.Removed = &a, &r
			}
		}
	}
	// Untracked files are not in the diff: count their lines (bounded).
	shown := 0
	for i := range files {
		if shown++; shown > GroupFileCap {
			break
		}
		c := &files[i]
		if c.Status != "A" || c.Added != nil || c.Binary {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(c.Path, dir), "/")
		data, err := os.ReadFile(filepath.Join(abs, filepath.FromSlash(rel)))
		if err != nil || len(data) > 1<<20 {
			continue
		}
		if bytes.IndexByte(data, 0) >= 0 {
			c.Binary = true
			continue
		}
		n := bytes.Count(data, []byte{'\n'})
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
		zero := 0
		c.Added, c.Removed = &n, &zero
	}
	return files, true
}

// GitHubRepo returns "owner/name" for a github.com remote URL, else "".
// Credentials in the URL are never kept.
func GitHubRepo(remote string) string {
	r := strings.TrimSpace(remote)
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		r = strings.TrimPrefix(r, p)
	}
	if i := strings.Index(r, "@"); i >= 0 && !strings.Contains(r[:i], "/") {
		r = r[i+1:]
	}
	if !strings.HasPrefix(r, "github.com/") && !strings.HasPrefix(r, "github.com:") {
		return ""
	}
	r = strings.TrimSuffix(strings.Trim(strings.TrimPrefix(r, "github.com"), "/:"), ".git")
	parts := strings.Split(r, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
