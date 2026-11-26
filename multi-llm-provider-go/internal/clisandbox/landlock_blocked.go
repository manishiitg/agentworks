package clisandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// Landlock rules only add access, so a blocked path inside a granted folder
// (a workflow's planning/, its raw db.sqlite, an AGENTS.md) cannot be taken
// back with a rule. Instead the grant is split: the folder that contains the
// blocked path stops being granted as a whole, and each of its entries is
// granted on its own, down the path to the blocked one, which is left out.
// Nothing is mounted, so this works for every launch (tmux, structured, slot)
// without namespaces.
//
// A write grant that contains a blocked path stays readable as a whole (a
// blocked-write path may still be read); it loses write access on itself, so
// no new entry can be created directly in it, and its other entries keep
// write access. A blocked path that may not even be read is also cut out of
// every read grant. Blocked paths that do not exist yet cannot be created: the
// folder that would hold them is no longer writable.

// splitAroundBlocked returns the read and write grants with every blocked path
// carved out. blockedRead may be neither read nor written; blockedWrite may
// only be read. Paths must be canonical.
func splitAroundBlocked(read, write, blockedRead, blockedWrite []string) ([]string, []string) {
	for _, blocked := range append(append([]string{}, blockedRead...), blockedWrite...) {
		write = carveOut(write, blocked, &read)
	}
	for _, blocked := range blockedRead {
		read = carveOut(read, blocked, nil)
	}
	return canonicalUnique(read), canonicalUnique(write)
}

// carveOut replaces every grant that contains blocked with grants for the
// entries around it. A grant equal to or inside blocked is dropped. When
// keepRead is set, a write grant that had to be split stays readable as a
// whole by adding it there.
func carveOut(grants []string, blocked string, keepRead *[]string) []string {
	blocked = filepath.Clean(blocked)
	out := make([]string, 0, len(grants))
	for _, grant := range grants {
		g := filepath.Clean(grant)
		switch {
		case g == blocked || strings.HasPrefix(g, blocked+string(filepath.Separator)):
			if keepRead != nil {
				*keepRead = append(*keepRead, g)
			}
		case strings.HasPrefix(blocked, g+string(filepath.Separator)):
			if keepRead != nil {
				*keepRead = append(*keepRead, g)
			}
			out = append(out, entriesAround(g, blocked)...)
		default:
			out = append(out, g)
		}
	}
	return out
}

// entriesAround lists, for every folder from root down to blocked's parent,
// the entries that are not on the way to blocked.
func entriesAround(root, blocked string) []string {
	var out []string
	dir := root
	for dir != blocked {
		rel, err := filepath.Rel(dir, blocked)
		if err != nil {
			return out
		}
		next := filepath.Join(dir, strings.Split(rel, string(filepath.Separator))[0])
		entries, err := os.ReadDir(dir)
		if err != nil {
			return out
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if path != next {
				out = append(out, path)
			}
		}
		dir = next
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return out
		}
	}
	return out
}

// outside drops the paths equal to or inside root.
func outside(paths []string, root string) []string {
	if root == "" {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			continue
		}
		out = append(out, path)
	}
	return out
}

// mountedBlocks picks the blocked paths the launcher mounts over instead of
// splitting a grant: an existing blocked path inside a read or write grant is
// hidden, an existing blocked-write path inside a write grant is made
// read-only. The rest (missing paths, paths outside every grant) are returned
// for splitAroundBlocked: a missing blocked file cannot be mounted over, and
// splitting its folder keeps it from being created there.
func mountedBlocks(read, write, blockedRead, blockedWrite []string) (hidden, readOnly, splitRead, splitWrite []string) {
	inside := func(path string, grants []string) bool {
		for _, grant := range grants {
			if path == grant || strings.HasPrefix(path, grant+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	present := func(path string) bool { _, err := os.Lstat(path); return err == nil }
	all := append(append([]string{}, read...), write...)
	for _, path := range blockedRead {
		if present(path) && inside(path, all) {
			hidden = append(hidden, path)
		} else {
			splitRead = append(splitRead, path)
		}
	}
	for _, path := range blockedWrite {
		if present(path) && inside(path, write) {
			readOnly = append(readOnly, path)
		} else {
			splitWrite = append(splitWrite, path)
		}
	}
	// A missing path inside a mounted one needs nothing more: nothing can be
	// created under a read-only or hidden mount (a Crew's settings/ covers its
	// records that do not exist yet).
	covered := func(path string) bool { return inside(path, hidden) || inside(path, readOnly) }
	keep := func(paths []string) []string {
		out := paths[:0:0]
		for _, path := range paths {
			if !covered(path) {
				out = append(out, path)
			}
		}
		return out
	}
	return hidden, readOnly, keep(splitRead), keep(splitWrite)
}
