// Package safegit runs Git inside folders whose contents other people can
// write (shared Crews). Such a repo's own config and .gitattributes can name
// commands that Git would run during status or diff, so every call neutralizes
// them. All server-side Git on Crew folders must go through Command.
package safegit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Command builds a hardened git call in dir (a checkout whose .git is a
// directory). extraConfig holds key=value pairs such as
// "core.excludesFile=/path". A checkout whose .git is a file is refused here;
// use CommandIn with the Crew root.
//
// Filter and diff driver commands defined by the repo are blanked, so Git LFS
// filters do not run: LFS files show as pointer text in status and diff.
func Command(ctx context.Context, dir string, extraConfig []string, args ...string) *exec.Cmd {
	return CommandIn(ctx, dir, dir, extraConfig, args...)
}

func refused(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", "--version-refused-by-safegit")
	cmd.Env = env()
	return cmd
}

// confined resolves p and reports whether it lies inside root's real path.
func confined(root, p string) (string, bool) {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	q, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(r, q)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return q, true
}

// linkedGitDir resolves a ".git" file (linked worktree or submodule) to its
// git dir. Both the git dir and its commondir must stay inside root.
func linkedGitDir(root, dir, file string) (string, bool) {
	raw, err := os.ReadFile(file)
	if err != nil || len(raw) > 4096 {
		return "", false
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", false
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	gitDir, ok := confined(root, target)
	if !ok {
		return "", false
	}
	if common, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(common))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		if _, ok := confined(root, c); !ok {
			return "", false
		}
	}
	return gitDir, true
}

// CheckoutInside recognizes a plain repository or a real linked worktree.
// Its Git directory, common objects and worktree backlink must stay in root;
// borrowed object stores and symlinked markers are not admitted by this view.
func CheckoutInside(root, dir string) (gitDir, common string, ok bool) {
	real, ok := confined(root, dir)
	if !ok {
		return "", "", false
	}
	marker := filepath.Join(real, ".git")
	info, err := os.Lstat(marker)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", "", false
	}
	gitDir = marker
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return "", "", false
		}
		gitDir, ok = linkedGitDir(root, real, marker)
		if !ok {
			return "", "", false
		}
		backlink, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
		if err != nil {
			return "", "", false
		}
		back, ok := confined(root, strings.TrimSpace(string(backlink)))
		if !ok || back != marker {
			return "", "", false
		}
	}
	common = gitDir
	if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		p := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(p) {
			p = filepath.Join(gitDir, p)
		}
		common, ok = confined(root, p)
		if !ok {
			return "", "", false
		}
	} else if !os.IsNotExist(err) {
		return "", "", false
	}
	for _, objects := range []string{gitDir, common} {
		if _, err := os.Lstat(filepath.Join(objects, "objects", "info", "alternates")); !os.IsNotExist(err) {
			return "", "", false
		}
	}
	return gitDir, common, true
}

// CommandIn is Command for checkouts that may have a .git file; every git
// dir it uses must resolve inside root.
func CommandIn(ctx context.Context, root, dir string, extraConfig []string, args ...string) *exec.Cmd {
	var full []string
	for _, c := range extraConfig {
		full = append(full, "-c", c)
	}
	full = append(full,
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.pager=cat",
		"-c", "core.quotepath=off",
		"-c", "core.sshCommand=",
		"-c", "safe.directory="+dir,
		"-c", "protocol.allow=never",
		"-c", "diff.external=",
		"-c", "gc.auto=0",
	)
	gitDir := filepath.Join(dir, ".git")
	if info, err := os.Lstat(gitDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// A symlinked .git is followed only when it stays inside root.
			real, ok := confined(root, gitDir)
			if !ok {
				return refused(ctx)
			}
			gitDir = real
			if info, err = os.Stat(real); err != nil {
				return refused(ctx)
			}
		}
		if !info.IsDir() {
			resolved, ok := linkedGitDir(root, dir, gitDir)
			if !ok {
				return refused(ctx)
			}
			gitDir = resolved
		}
		// Explicit git dir and work tree: the repo's own config (core.worktree)
		// or a .git file cannot redirect Git outside the folder.
		full = append(full, "--git-dir", gitDir, "--work-tree", dir)
		overrides, ok := cachedDriverOverrides(ctx, root, dir, gitDir)
		if !ok {
			return refused(ctx) // fail closed: unknown drivers could run
		}
		for _, c := range overrides {
			full = append(full, "-c", c)
		}
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = env()
	return cmd
}

// PrivateStore runs Git on a server-private repository (gitDir, kept outside
// every shared folder) with workTree, a folder inside root, as the work tree
// and indexFile as the index. The checkout's own .git (config, hooks,
// info/exclude, index, HEAD, refs) is never read or written: Git sees only
// gitDir. Pathspecs are literal, no fsmonitor, hooks, pager, SSH or
// protocol runs, and the store's own filter/diff drivers are blanked as in
// CommandIn. Content conversions are off (autocrlf, safecrlf, the global
// attributes file); the caller's store keeps an info/attributes that unsets
// text, eol, ident, filter and working-tree-encoding for every path, which
// takes precedence over the work tree's .gitattributes.
func PrivateStore(ctx context.Context, root, gitDir, workTree, indexFile string, args ...string) *exec.Cmd {
	tree, ok := confined(root, workTree)
	if !ok {
		return refused(ctx)
	}
	overrides, ok := privateDriverOverrides(ctx, gitDir)
	if !ok {
		return refused(ctx)
	}
	full := []string{
		"--no-optional-locks",
		"--literal-pathspecs",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.pager=cat",
		"-c", "core.quotepath=off",
		"-c", "core.sshCommand=",
		"-c", "safe.directory=" + gitDir,
		"-c", "safe.directory=" + tree,
		"-c", "protocol.allow=never",
		"-c", "diff.external=",
		"-c", "gc.auto=0",
		"-c", "core.bare=false",
		"-c", "core.autocrlf=false",
		"-c", "core.safecrlf=false",
		"-c", "core.attributesFile=" + os.DevNull,
		"-c", "core.excludesFile=" + os.DevNull,
		"-c", "core.untrackedCache=false",
		"-c", "core.splitIndex=false",
		"-c", "core.symlinks=true",
		"-c", "core.fileMode=true",
		"-c", "core.trustctime=true",
		"-c", "core.checkStat=default",
		"-c", "core.ignoreCase=false",
		// Paths come from our own listing and are never checked out, so the
		// Windows/macOS checkout name guards would only refuse valid files.
		"-c", "core.protectNTFS=false",
		"-c", "core.protectHFS=false",
		"--git-dir", gitDir,
		"--work-tree", tree,
	}
	for _, c := range overrides {
		full = append(full, "-c", c)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Dir = tree
	cmd.Env = append(env(), "GIT_INDEX_FILE="+indexFile)
	return cmd
}

// configCache remembers each Git dir's driver overrides with the config files
// they came from, so a call does not start a second git process while those
// files are unchanged. Reply capture runs about seven Git commands per
// checkout; reading config before each one doubled the processes started.
var configCache sync.Map // gitDir -> cachedConfig

type cachedConfig struct {
	key       string
	overrides []string
}

// cachedDriverOverrides is driverOverrides, reused while every config file Git
// reads for gitDir (the common config and config.worktree) has the same
// contents. The files are read before Git runs, so an edit in between only
// causes a fresh read next time. Configs with includes are never cached: an
// included file can change while these stay the same.
func cachedDriverOverrides(ctx context.Context, root, dir, gitDir string) ([]string, bool) {
	key, cacheable := configKey(root, dir, gitDir)
	if cacheable {
		if cached, ok := configCache.Load(gitDir); ok && cached.(cachedConfig).key == key {
			return cached.(cachedConfig).overrides, true
		}
	}
	overrides, ok := driverOverrides(ctx, root, dir, gitDir)
	if ok && cacheable {
		configCache.Store(gitDir, cachedConfig{key: key, overrides: overrides})
	}
	return overrides, ok
}

func configKey(root, dir, gitDir string) (string, bool) {
	common := gitDir
	if raw, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(raw))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	} else if !os.IsNotExist(err) {
		return "", false
	}
	var key strings.Builder
	key.WriteString(root + "\x00" + dir + "\x00")
	for _, file := range []string{filepath.Join(common, "config"), filepath.Join(gitDir, "config.worktree")} {
		raw, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) || strings.Contains(strings.ToLower(string(raw)), "include") {
			return "", false
		}
		key.WriteString(file + "\x00" + string(raw) + "\x00")
	}
	return key.String(), true
}

func privateDriverOverrides(ctx context.Context, gitDir string) ([]string, bool) {
	// The private store's config lives in gitDir, so includes must stay there.
	return cachedDriverOverrides(ctx, gitDir, gitDir, gitDir)
}

func env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
}

// driverOverrides blanks every filter and diff driver command the repo's
// config defines, including drivers in files it includes (git status and diff
// follow include.path/includeIf) and config.worktree. No scope flag: --local
// would skip config.worktree; env() already turns off system and global
// config. Reading config runs nothing. ok is false when the config cannot be
// read or includes a file outside root.
func driverOverrides(ctx context.Context, root, dir, gitDir string) ([]string, bool) {
	cmd := exec.CommandContext(ctx, "git", "-c", "safe.directory="+dir, "--git-dir", gitDir, "config", "--includes", "--show-origin", "--name-only", "--null", "--list")
	cmd.Dir = dir
	cmd.Env = env()
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var cfg []string
	// Git quotes origins containing backslashes/control characters in its
	// line format. NUL framing preserves literal Windows paths and filenames.
	fields := strings.Split(string(out), "\x00")
	if len(fields)%2 != 1 || fields[len(fields)-1] != "" {
		return nil, false
	}
	for index := 0; index+1 < len(fields); index += 2 {
		origin, k := fields[index], fields[index+1]
		if origin == "" || k == "" {
			return nil, false
		}
		if file, isFile := strings.CutPrefix(origin, "file:"); isFile {
			if _, ok := confined(root, file); !ok {
				return nil, false
			}
		}
		i, j := strings.Index(k, "."), strings.LastIndex(k, ".")
		if i == j {
			continue // filter.<x> without a driver name, e.g. diff.renames
		}
		kind, name := k[:i], k[i+1:j]
		if kind != "filter" && kind != "diff" || seen[kind+"."+name] {
			continue
		}
		seen[kind+"."+name] = true
		if kind == "filter" {
			cfg = append(cfg, "filter."+name+".clean=", "filter."+name+".smudge=", "filter."+name+".process=", "filter."+name+".required=false")
		} else {
			cfg = append(cfg, "diff."+name+".textconv=", "diff."+name+".command=")
		}
	}
	return cfg, true
}
