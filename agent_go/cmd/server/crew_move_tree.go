package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"syscall"
	"time"
)

// PLAT-442 step 4 / PLAT-450: the tree engine of the Crew move. Every operation goes through os.Root values (the
// source Crew folder, the destination folder), which confine all access to their directory: a symlink inside a Crew can
// never lead a read or a write out of it, and nothing is ever followed into another user's tree. A symlink inside the
// tree is copied AS a symlink (relative, staying inside the tree) or refused; devices, sockets and pipes are refused.

// crewTreeEntry is one entry of a Crew's tree, as walked (and, after a copy, hashed).
type crewTreeEntry struct {
	Rel     string      // path below the Crew folder ("" is the root itself)
	Kind    byte        // 'd' directory, 'f' regular file, 'l' symlink
	Mode    fs.FileMode // permission and setuid/setgid/sticky bits
	Size    int64
	ModTime time.Time
	UID     int
	GID     int
	Hash    string // sha256 of a regular file's content, after a copy
	Link    string // a symlink's target
}

// crewTreeFinding is something in a tree that blocks or deserves a note.
type crewTreeFinding struct {
	Rel    string
	Kind   string // "external-symlink", "internal-symlink", "special-file"
	Detail string
}

var errCrewTreeSpecial = errors.New("special file in a Crew folder")

// walkCrewTree lists every entry below root, directories before their children, in name order. It never follows a
// link. Findings report links (external ones block a move) and special files (an error).
func walkCrewTree(root *os.Root) ([]crewTreeEntry, []crewTreeFinding, error) {
	var entries []crewTreeEntry
	var findings []crewTreeFinding
	rootInfo, err := root.Lstat(".")
	if err != nil {
		return nil, nil, err
	}
	rootEntry := entryFromInfo("", rootInfo)
	rootEntry.Kind = 'd'
	entries = append(entries, rootEntry)
	var walk func(rel string) error
	walk = func(rel string) error {
		dir := rel
		if dir == "" {
			dir = "."
		}
		handle, err := root.Open(dir)
		if err != nil {
			return err
		}
		names, err := handle.Readdirnames(-1)
		_ = handle.Close()
		if err != nil {
			return err
		}
		sort.Strings(names)
		present := make(map[string]bool, len(names))
		for _, name := range names {
			present[name] = true
		}
		for _, name := range names {
			child := name
			if rel != "" {
				child = rel + "/" + name
			}
			// SQLite's shared-memory index (<db>-shm) holds no data: it is created and deleted as processes open and close the
			// database and is rebuilt on the next open. Copying it is meaningless and it can vanish mid-copy, which aborted a
			// real move (RTS 2026-10-05). The database and its -wal file are still copied and still must not change.
			if strings.HasSuffix(name, "-shm") && present[strings.TrimSuffix(name, "-shm")] {
				findings = append(findings, crewTreeFinding{Rel: child, Kind: "sqlite-shm", Detail: "SQLite shared-memory index, rebuilt on open"})
				continue
			}
			info, err := root.Lstat(child)
			if err != nil {
				return err
			}
			entry := entryFromInfo(child, info)
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, err := root.Readlink(child)
				if err != nil {
					return err
				}
				entry.Kind, entry.Link = 'l', target
				if external, why := crewLinkEscapes(child, target); external {
					findings = append(findings, crewTreeFinding{Rel: child, Kind: "external-symlink", Detail: why})
				} else {
					findings = append(findings, crewTreeFinding{Rel: child, Kind: "internal-symlink", Detail: "-> " + target})
				}
				entries = append(entries, entry)
			case info.IsDir():
				entry.Kind = 'd'
				entries = append(entries, entry)
				if err := walk(child); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				entry.Kind = 'f'
				entries = append(entries, entry)
			default:
				findings = append(findings, crewTreeFinding{Rel: child, Kind: "special-file", Detail: info.Mode().Type().String()})
				return fmt.Errorf("%w: %s (%s)", errCrewTreeSpecial, child, info.Mode().Type())
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return entries, findings, err
	}
	return entries, findings, nil
}

func entryFromInfo(rel string, info fs.FileInfo) crewTreeEntry {
	entry := crewTreeEntry{Rel: rel, Mode: info.Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky), Size: info.Size(), ModTime: info.ModTime(), UID: -1, GID: -1}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		entry.UID, entry.GID = int(st.Uid), int(st.Gid)
	}
	if info.IsDir() {
		entry.Size = 0
	}
	return entry
}

// crewLinkEscapes reports whether a symlink at rel with the given target leaves the Crew folder: an absolute target,
// or a relative one that resolves above the Crew's root.
func crewLinkEscapes(rel, target string) (bool, string) {
	// A link to a system program or library (a virtualenv's python, a .bin shim) gives nobody anything: every account can
	// read /usr already, and the link is copied as a link, never followed. Everything else that leaves the folder (a
	// login file, an app folder, /tmp, another Crew) still blocks the move.
	if path.IsAbs(target) && crewSystemLinkTarget(target) {
		return false, ""
	}
	if path.IsAbs(target) || strings.HasPrefix(target, "\\") {
		return true, "absolute target " + target
	}
	resolved := path.Clean(path.Join(path.Dir(rel), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return true, "target " + target + " leaves the Crew folder"
	}
	return false, ""
}

// crewSystemLinkTarget says whether an absolute link target is inside a read-only system tree.
func crewSystemLinkTarget(target string) bool {
	clean := path.Clean(target)
	for _, root := range []string{"/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/"} {
		if strings.HasPrefix(clean, root) && !strings.Contains(clean, "..") {
			return true
		}
	}
	return false
}

// crewTreeSummaryOf counts a walked tree (the root entry is not a directory of the Crew's own).
func crewTreeSummaryOf(entries []crewTreeEntry) crewTreeSummary {
	var s crewTreeSummary
	for _, entry := range entries {
		switch entry.Kind {
		case 'd':
			if entry.Rel != "" {
				s.Dirs++
			}
		case 'f':
			s.Files++
			s.Bytes += entry.Size
		case 'l':
			s.Symlinks++
		}
	}
	return s
}

// crewTreeDigest is a stable digest of what a tree contains: every entry's path, kind, permission bits, size and
// content hash (or link target). Ownership and times are not part of it (they are checked separately).
func crewTreeDigest(entries []crewTreeEntry) string {
	sorted := append([]crewTreeEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Rel < sorted[j].Rel })
	sum := sha256.New()
	for _, entry := range sorted {
		// The group bits are left out: the copy may widen them for the owner's slot account (see copyCrewTree).
		fmt.Fprintf(sum, "%s\t%c\t%o\t%d\t%s\t%s\n", entry.Rel, entry.Kind, uint32(entry.Mode&^0o070), entry.Size, entry.Hash, entry.Link)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// copyOptions control one copy.
type copyOptions struct {
	// Hook is called with ("copy", n) after every file; returning an error stops the copy (a crash in tests).
	Hook func(point string, n int) error
}

// copyCrewTree copies a walked tree from src into the (empty) dst, returning the entries with the content hashes of
// the bytes actually written. Files are written to new files created exclusively; symlinks are created as symlinks;
// directories get their final permissions, group and times after their contents.
func copyCrewTree(src, dst *os.Root, entries []crewTreeEntry, opts copyOptions) ([]crewTreeEntry, error) {
	out := make([]crewTreeEntry, len(entries))
	copy(out, entries)
	appUID := os.Getuid()
	files := 0
	for i := range out {
		entry := &out[i]
		if entry.Rel == "" {
			continue
		}
		switch entry.Kind {
		case 'd':
			if err := dst.Mkdir(entry.Rel, 0o700); err != nil {
				return out, err
			}
		case 'f':
			hash, err := copyOneFile(src, dst, entry)
			if err != nil {
				return out, fmt.Errorf("%s: %w", entry.Rel, err)
			}
			entry.Hash = hash
			files++
			if opts.Hook != nil {
				if err := opts.Hook("copy", files); err != nil {
					return out, err
				}
			}
		case 'l':
			if err := dst.Symlink(entry.Link, entry.Rel); err != nil {
				return out, fmt.Errorf("%s: %w", entry.Rel, err)
			}
		}
	}
	// Attributes, children first so a directory's own mode and times are applied last.
	for i := len(out) - 1; i >= 0; i-- {
		entry := out[i]
		rel := entry.Rel
		if rel == "" {
			rel = "."
		}
		if entry.GID >= 0 {
			if err := dst.Lchown(rel, -1, entry.GID); err != nil && entry.GID != os.Getegid() {
				return out, fmt.Errorf("%s: cannot keep group %d (the move must run as an account that belongs to it): %w", rel, entry.GID, err)
			}
		}
		if entry.Kind == 'l' {
			continue
		}
		mode := entry.Mode
		// A copy never carries a set-user-id or set-group-id bit on a file (a Crew's files are data); folders keep setgid, which
		// is how a slot tree keeps its group.
		if entry.Kind != 'd' {
			mode &^= fs.ModeSetuid | fs.ModeSetgid
		}
		// A file or folder the owner's slot account created is owned by that account; the copy is owned by the app
		// account, so the owner's slot keeps its access through the group (the slot's tree is group read/write).
		if entry.UID >= 0 && entry.UID != appUID {
			mode |= 0o060
			if entry.Kind == 'd' {
				mode |= 0o010
			}
		}
		if err := dst.Chmod(rel, mode); err != nil {
			return out, fmt.Errorf("%s: %w", rel, err)
		}
		if err := dst.Chtimes(rel, entry.ModTime, entry.ModTime); err != nil {
			return out, fmt.Errorf("%s: %w", rel, err)
		}
	}
	return out, nil
}

func copyOneFile(src, dst *os.Root, entry *crewTreeEntry) (string, error) {
	in, err := src.Open(entry.Rel)
	if err != nil {
		return "", err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: no longer a regular file", errCrewTreeSpecial)
	}
	out, err := dst.OpenFile(entry.Rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, sum), in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if written != entry.Size {
		return "", fmt.Errorf("the file changed while it was copied (%d bytes read, %d expected)", written, entry.Size)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// hashCrewTree hashes every regular file of a tree in place (a fresh walk), returning the entries with hashes.
func hashCrewTree(root *os.Root) ([]crewTreeEntry, error) {
	entries, _, err := walkCrewTree(root)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Kind != 'f' {
			continue
		}
		file, err := root.Open(entries[i].Rel)
		if err != nil {
			return nil, err
		}
		sum := sha256.New()
		n, err := io.Copy(sum, file)
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entries[i].Rel, err)
		}
		if n != entries[i].Size {
			return nil, fmt.Errorf("%s: read %d bytes, expected %d", entries[i].Rel, n, entries[i].Size)
		}
		entries[i].Hash = hex.EncodeToString(sum.Sum(nil))
	}
	return entries, nil
}

// sameCrewSnapshot reports whether two walks of the same tree saw the same entries (a change between them means the
// tree was written while it was being copied).
func sameCrewSnapshot(a, b []crewTreeEntry) (bool, string) {
	if len(a) != len(b) {
		return false, fmt.Sprintf("%d entries, then %d", len(a), len(b))
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Rel != y.Rel || x.Kind != y.Kind || x.Size != y.Size || x.Link != y.Link || x.Mode != y.Mode || !x.ModTime.Equal(y.ModTime) {
			return false, "changed: " + x.Rel
		}
	}
	return true, ""
}

// compareCrewTrees reports the first difference between a source walk (with hashes from the copy) and the destination's
// content, permissions and links; group and times are compared when the source recorded them.
func compareCrewTrees(want []crewTreeEntry, got []crewTreeEntry) error {
	if len(want) != len(got) {
		return fmt.Errorf("entry count differs: %d, expected %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		switch {
		case w.Rel != g.Rel || w.Kind != g.Kind:
			return fmt.Errorf("entry %d: %q (%c), expected %q (%c)", i, g.Rel, g.Kind, w.Rel, w.Kind)
		case w.Kind == 'f' && (w.Size != g.Size || w.Hash != g.Hash):
			return fmt.Errorf("%s: content differs from the source", w.Rel)
		case w.Kind == 'l' && w.Link != g.Link:
			return fmt.Errorf("%s: link target %q, expected %q", w.Rel, g.Link, w.Link)
		case w.Kind != 'l' && w.Mode&^0o070 != g.Mode&^0o070:
			// Only the owner-slot widening of group bits may differ.
			return fmt.Errorf("%s: mode %v, expected %v", w.Rel, g.Mode, w.Mode)
		case w.GID >= 0 && g.GID >= 0 && w.GID != g.GID:
			return fmt.Errorf("%s: group %d, expected %d", w.Rel, g.GID, w.GID)
		}
	}
	return nil
}
