package filesnapshots

import (
	"bytes"
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/safegit"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// GroupFileCap bounds the files listed per group; the rest is a count.
const GroupFileCap = 50

// PullRequest is the GitHub state of a group's branch. State is open, merged,
// closed, connect_github (the Crew owner has no connection) or unavailable.
type PullRequest struct {
	PendingChecks bool   `json:"-"`
	Number        int    `json:"number,omitempty"`
	URL           string `json:"url,omitempty"`
	State         string `json:"state"`
	Checks        string `json:"checks,omitempty"`
	Review        string `json:"review,omitempty"`
	// FailingChecks names the failing checks (bounded); Conflicts is true when
	// GitHub says the open PR cannot merge because of conflicts.
	FailingChecks []string `json:"failing_checks,omitempty"`
	Conflicts     bool     `json:"conflicts,omitempty"`
}

// Commit is one commit made during a chat.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Files   int    `json:"files"`
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
	// NextStep is the one Git step the card offers: share_pr, update_pr,
	// get_latest or none. Set by the caller with NextStep once PR is known.
	NextStep string `json:"next_step,omitempty"`
	// The chat summary's Git state (AddChatGitState): files that differ from
	// HEAD now, and the commits made since the chat first saw the checkout.
	// CommitsKnown is false when the chat has no saved starting commit.
	Uncommitted  []Change `json:"uncommitted,omitempty"`
	Commits      []Commit `json:"commits,omitempty"`
	CommitsKnown bool     `json:"commits_known,omitempty"`

	dirty      bool // files differ from HEAD now
	unpushed   int  // commits on HEAD that are on no remote branch
	behind     int  // commits on the upstream branch not in HEAD (as of the last fetch)
	historical bool // the saved checkout/branch is not the current working context
}

// NextStep decides the single next Git step for a repo group: first get the
// latest when the branch is behind its remote, then update the open pull
// request or share a new one when there is work no remote has yet.
func NextStep(g Group) string {
	if g.Kind != "repo" || g.GitHubRepo == "" || g.Branch == "" || strings.HasPrefix(g.Branch, "detached@") {
		return "none"
	}
	if g.behind > 0 {
		return "get_latest"
	}
	if !g.dirty && g.unpushed == 0 {
		return "none"
	}
	switch {
	case g.PR == nil || g.PR.State == "merged" || g.PR.State == "closed":
		return "share_pr"
	case g.PR.State == "open":
		return "update_pr"
	}
	return "none" // PR state unknown (GitHub not connected or unavailable)
}

func gitCount(ctx context.Context, root, abs string, args ...string) int {
	out, err := gitIn(ctx, root, abs, append([]string{"rev-list", "--count"}, args...)...).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

// gitIn runs read-only Git in a user checkout through the shared hardened helper.
func gitIn(ctx context.Context, root, dir string, args ...string) *exec.Cmd {
	return safegit.CommandIn(ctx, root, dir, nil, args...)
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

// OutsideGitPaths narrows an existing path grant using the same checkout
// classification as GroupChanges. Apply it before comparing or paging files.
func OutsideGitPaths(root string, allow func(string) bool) func(string) bool {
	cache := map[string]string{}
	return func(file string) bool {
		return allow(file) && checkoutRoot(root, file, cache) == ""
	}
}

// GroupChanges groups a reply's changes by Git checkout; other changes are one
// "Crew files (not in Git)" group. newRepos are the checkouts that appeared
// during the reply (Comparison.NewRepos): their changes are already only the
// edits made since their checked-out commit, and the group says "new copy".
// allow takes root-relative paths and is the caller's current file guard.
func GroupChanges(ctx context.Context, root string, changes []Change, newRepos []string, allow func(string) bool) []Group {
	cache := map[string]string{}
	byRepo := map[string][]Change{}
	labels := map[string]*CheckoutLabel{}
	roots := map[string]string{}
	var plain []Change
	for _, c := range changes {
		if c.Checkout != nil {
			label := c.Checkout
			key := "saved:" + label.ID + ":" + label.Branch
			labels[key], roots[key] = label, label.Root
			byRepo[key] = append(byRepo[key], c)
			continue
		}
		if key := checkoutRoot(root, c.Path, cache); key != "" {
			dir := strings.TrimPrefix(key, "repo:")
			if dir == "." {
				dir = ""
			}
			byRepo[dir] = append(byRepo[dir], c)
			roots[dir] = dir
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
		dir := roots[d]
		g := repoGroup(ctx, root, dir, byRepo[d], allow)
		if label := labels[d]; label != nil {
			g.historical = historicalBranch(g, label)
			gitdir, err := gitIn(ctx, root, filepath.Join(root, filepath.FromSlash(dir)), "rev-parse", "--absolute-git-dir").Output()
			g.historical = g.historical || err != nil || digest(strings.TrimSpace(string(gitdir))) != label.ID
			g.Name, g.Branch, g.GitHubRepo = label.Name, label.Branch, label.GitHubRepo
			if g.historical {
				g.dirty, g.unpushed, g.behind = false, 0, 0
			}
		}
		for _, p := range newRepos {
			g.NewCheckout = g.NewCheckout || p == dir
		}
		out = append(out, g)
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
	if out, err := gitIn(ctx, root, abs, "symbolic-ref", "--short", "-q", "HEAD").Output(); err == nil {
		g.Branch = strings.TrimSpace(string(out))
	}
	if g.Branch == "" {
		if out, err := gitIn(ctx, root, abs, "rev-parse", "--short", "HEAD").Output(); err == nil {
			g.Branch = "detached@" + strings.TrimSpace(string(out))
		}
	}
	if out, err := gitIn(ctx, root, abs, "config", "--get", "remote.origin.url").Output(); err == nil {
		g.GitHubRepo = GitHubRepo(string(out))
	}
	files, ok := workingTreeEdits(ctx, root, abs, dir, allow)
	g.dirty = ok && len(files) > 0
	g.unpushed = gitCount(ctx, root, abs, "HEAD", "--not", "--remotes")
	g.behind = gitCount(ctx, root, abs, "HEAD..@{upstream}")
	// The card lists this reply's own changes, as saved.
	g.Files = changes
	return capGroup(g)
}

// workingTreeEdits lists files differing from HEAD in one checkout.
func workingTreeEdits(ctx context.Context, root, abs, dir string, allow func(string) bool) ([]Change, bool) {
	raw, err := gitIn(ctx, root, abs, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all").Output()
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
	if stats, err := gitIn(ctx, root, abs, "diff", "HEAD", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat", "-z", "--").Output(); err == nil {
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

// chatCommitCap bounds the commits a chat summary lists per checkout.
const chatCommitCap = 30

var commitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// AddChatGitState fills a repo group's uncommitted files and, when start (the
// commit the chat first saw) is known and still an ancestor of HEAD, the
// commits made since. Read-only.
func AddChatGitState(ctx context.Context, root string, g *Group, start string, allow func(string) bool) {
	if g.Kind != "repo" {
		return
	}
	abs := filepath.Join(root, filepath.FromSlash(g.Root))
	if files, ok := workingTreeEdits(ctx, root, abs, g.Root, allow); ok {
		if len(files) > GroupFileCap {
			files = files[:GroupFileCap]
		}
		g.Uncommitted = files
	}
	// A saved start reaches Git as an argument: only a full commit ID passes.
	if !commitID.MatchString(start) || gitIn(ctx, root, abs, "merge-base", "--is-ancestor", start, "HEAD").Run() != nil {
		return
	}
	g.CommitsKnown = true
	out, err := gitIn(ctx, root, abs, "log", "-n", strconv.Itoa(chatCommitCap), "--format=%x1e%H%x1f%s", "--shortstat", start+"..HEAD").Output()
	if err != nil {
		g.CommitsKnown = false
		return
	}
	for _, entry := range strings.Split(string(out), "\x1e") {
		head, rest, _ := strings.Cut(strings.TrimSpace(entry), "\n")
		sha, subject, ok := strings.Cut(head, "\x1f")
		if !ok || len(sha) < 40 {
			continue
		}
		c := Commit{SHA: sha, Subject: subject}
		if n, _, found := strings.Cut(strings.TrimSpace(rest), " file"); found {
			c.Files, _ = strconv.Atoi(strings.TrimSpace(n))
		}
		g.Commits = append(g.Commits, c)
	}
}

// ChatStarts returns, for each checkout, the HEAD commit the earliest of the
// given replies (oldest first) saw before it ran.
func (s Store) ChatStarts(ids []string) map[string]string {
	starts := map[string]string{}
	for _, id := range ids {
		r, err := s.read(id)
		if err != nil {
			continue
		}
		for p, head := range r.BeforeHeads {
			if _, seen := starts[p]; !seen {
				starts[p] = head
			}
		}
	}
	return starts
}
