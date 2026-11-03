// Package crewsync transfers authored project content between two admitted Crew
// roots. Private state, provider logins and native conversations never enter it.
package crewsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type File struct {
	Hash       string `json:"hash"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
}
type Manifest map[string]File
type Change struct {
	Path     string `json:"path"`
	Before   *File  `json:"before,omitempty"`
	After    *File  `json:"after,omitempty"`
	DataPath string `json:"-"`
}
type ContentSnapshot struct {
	Base       string `json:"base"`
	Head       string `json:"head"`
	BundlePath string `json:"-"`
}

// Payload describes an immutable binary artifact. Bytes travel separately from metadata.
type Payload struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

type Snapshot struct {
	StatePath  string             `json:"-"` // owner-scoped move records, admitted separately from working files
	DecodedDir string             `json:"-"`
	StagingDir string             `json:"-"`
	Payloads   map[string]Payload `json:"payloads,omitempty"`
	Content    *ContentSnapshot   `json:"content,omitempty"`
	Omitted    []string           `json:"omitted,omitempty"`
	Git        *GitSnapshot       `json:"git,omitempty"`
	Files      Manifest           `json:"files"`
	Changes    []Change           `json:"changes"`
}

// Both send and receive use this policy. Project ignore rules are additionally
// evaluated at the source. Runtime outputs are not authored source.
func Allowed(key string, directory bool) bool { return crewroot.SyncContentAllowed(key, directory) }

func rootFor(roots crewroot.Roots, key string) (string, string, error) {
	if !Allowed(key, false) {
		return "", "", fmt.Errorf("this file is not project content: %s", key)
	}
	area, rel, _ := strings.Cut(key, "/")
	if area == "knowledge" {
		physical, err := crewroot.ResolveContained(roots.Private, rel)
		if err != nil || !crewroot.Within(filepath.Join(roots.Private, "knowledgebase"), physical) {
			return "", "", fmt.Errorf("knowledge path leaves its Crew knowledge folder")
		}
		return roots.Private, rel, nil
	}
	if area == "files" {
		return roots.Files, rel, nil
	}
	return roots.Resources, rel, nil
}

func fingerprint(data []byte, executable bool) File {
	sum := sha256.Sum256(data)
	return File{Hash: hex.EncodeToString(sum[:]), Size: int64(len(data)), Executable: executable}
}

// CopyChecked bounds memory independently of the file size and verifies the
// admitted bytes before returning. The caller owns the reader and destination.
func CopyChecked(ctx context.Context, dst io.Writer, src io.Reader, expected File) error {
	return copyChecked(ctx, dst, src, expected, SyncLimits(ctx).FileBytes)
}

func copyChecked(ctx context.Context, dst io.Writer, src io.Reader, expected File, fileLimit int64) error {
	if expected.Size < 0 || expected.Size > fileLimit {
		return fmt.Errorf("project file exceeds sync limit")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), &contextReader{ctx, io.LimitReader(src, expected.Size+1)}, make([]byte, 64<<10))
	if err != nil {
		return err
	}
	if n != expected.Size || hex.EncodeToString(h.Sum(nil)) != expected.Hash {
		return fmt.Errorf("project file changed during capture or does not match its snapshot")
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readFile(ctx context.Context, root, rel string, dst io.Writer) (File, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return File{}, err
	}
	defer dir.Close()
	info, err := dir.Lstat(rel)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > SyncLimits(ctx).FileBytes {
		return File{}, fmt.Errorf("unsupported file or file exceeds sync limit: %s", rel)
	}
	f, err := dir.Open(rel)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), &contextReader{ctx, io.LimitReader(f, SyncLimits(ctx).FileBytes+1)}, make([]byte, 64<<10))
	if err != nil {
		return File{}, err
	}
	if n > SyncLimits(ctx).FileBytes {
		return File{}, fmt.Errorf("project file exceeds sync limit: %s", rel)
	}
	return File{Hash: hex.EncodeToString(h.Sum(nil)), Size: n, Executable: info.Mode()&0111 != 0}, nil
}

// Close removes only this operation's generated files, never working files or
// an admitted transfer's frozen artifacts. Call on capture errors as well.
func (s *Snapshot) Close() {
	if s.DecodedDir != "" {
		_ = os.RemoveAll(s.DecodedDir)
		s.DecodedDir = ""
	}
	if s.StagingDir != "" {
		_ = os.RemoveAll(s.StagingDir)
		s.StagingDir = ""
	}
}

// Capture sends only changed bytes. Expected fingerprints are retained for
// every deletion/replacement so edits outside AgentWorks are never overwritten.
func Capture(ctx context.Context, roots crewroot.Roots, previous Manifest, privateStageRoot string) (out Snapshot, err error) {
	out = Snapshot{Files: Manifest{}, Changes: []Change{}}
	if privateStageRoot == "" {
		return out, fmt.Errorf("private sync staging directory is required")
	}
	out.StagingDir, err = os.MkdirTemp(privateStageRoot, "capture-")
	if err != nil {
		return out, err
	}
	defer func() {
		if err != nil {
			out.Close()
		}
	}()
	var total int64
	ignoredFiles := map[string]bool{}
	areas := []struct{ name, root string }{{"files", roots.Files}, {"resources", roots.Resources}}
	if !roots.SeparateFiles() {
		areas = areas[1:]
	}
	if roots.Private != "" {
		areas = append(areas, struct{ name, root string }{"knowledge", roots.Private})
	}
	for _, area := range areas {
		ignored, err := publication.GitIgnored(ctx, area.root)
		if err != nil {
			return out, err
		}
		for rel := range ignored {
			ignoredFiles[area.name+"/"+rel] = true
		}
		err = filepath.WalkDir(area.root, func(p string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if p == area.root {
				return nil
			}
			rel, _ := filepath.Rel(area.root, p)
			rel = filepath.ToSlash(rel)
			key := area.name + "/" + rel
			// The resources tree is captured independently; never descend through it twice.
			if (area.name == "files" && p == roots.Resources) || !Allowed(key, entry.IsDir()) || ignored[rel] {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			file, err := readFile(ctx, area.root, rel, io.Discard)
			if err != nil {
				return err
			}
			total += file.Size
			if total > SyncLimits(ctx).Bytes || len(out.Files) >= SyncLimits(ctx).Files {
				return fmt.Errorf("project exceeds 256 MiB or 10,000 files; exclude generated files before syncing")
			}
			before, exists := previous[key]
			// NTFS does not represent the Unix execute bit. Keep its last synced
			// value so editing a script on Windows does not make it non-executable.
			if runtime.GOOS == "windows" && exists {
				file.Executable = before.Executable
			}
			out.Files[key] = file
			if !exists || before != file {
				stage, err := os.CreateTemp(out.StagingDir, "file-")
				if err != nil {
					return err
				}
				err = CopyContent(ctx, stage, roots, key, file)
				closeErr := stage.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
				change := Change{Path: key, After: &file, DataPath: stage.Name()}
				if exists {
					change.Before = &before
				}
				out.Changes = append(out.Changes, change)
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	for key, before := range previous {
		if _, exists := out.Files[key]; !exists {
			// Keep the last reconciled version while excluded. If this file is
			// included again, its unchanged destination is still a valid base.
			if ignoredFiles[key] {
				out.Files[key] = before
				out.Omitted = append(out.Omitted, key)
				continue
			}
			// If exclusions changed, leave the old copy alone rather than sending
			// a mutation for a path the current policy no longer admits.
			if Allowed(key, false) {
				out.Changes = append(out.Changes, Change{Path: key, Before: &before})
			}
		}
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].Path < out.Changes[j].Path })
	return out, nil
}

func sameFile(actual, expected File) bool {
	if runtime.GOOS == "windows" {
		actual.Executable = expected.Executable
	}
	return actual == expected
}

func checkChange(ctx context.Context, roots crewroot.Roots, change Change) error {
	root, rel, err := rootFor(roots, change.Path)
	if err != nil {
		return err
	}
	if _, err = crewroot.ResolveContained(root, rel); err != nil {
		return err
	}
	actual, err := readFile(ctx, root, rel, io.Discard)
	if os.IsNotExist(err) {
		if change.Before == nil || change.After == nil {
			return nil
		}
		return fmt.Errorf("%s was removed here; copy your version to another filename, then restore this path to its last synced contents and retry", change.Path)
	}
	if err != nil {
		return err
	}
	// A retry after a partial transfer can safely accept an already-applied file.
	if change.After != nil && sameFile(actual, *change.After) {
		return nil
	}
	if change.Before == nil || !sameFile(actual, *change.Before) {
		return fmt.Errorf("%s changed here; copy your version to another filename, then restore this path to its last synced contents and retry", change.Path)
	}
	return nil
}

// Apply validates the complete batch before its first write. Callers serialize
// this with the existing project mutation lock and save its manifest on success.
func Apply(ctx context.Context, roots crewroot.Roots, snapshot Snapshot) error {
	if len(snapshot.Changes) > SyncLimits(ctx).Files || len(snapshot.Files) > SyncLimits(ctx).Files {
		return fmt.Errorf("too many project files")
	}
	seen := map[string]bool{}
	var total int64
	for _, change := range snapshot.Changes {
		if seen[change.Path] {
			return fmt.Errorf("duplicate project file")
		}
		seen[change.Path] = true
		if change.After != nil {
			total += change.After.Size
			if total > SyncLimits(ctx).Bytes || snapshot.Files[change.Path] != *change.After {
				return fmt.Errorf("invalid project file content")
			}
			if err := change.CopyTo(ctx, io.Discard); err != nil {
				return err
			}
		} else if _, exists := snapshot.Files[change.Path]; exists {
			return fmt.Errorf("deleted file remains in snapshot")
		}
		if err := checkChange(ctx, roots, change); err != nil {
			return err
		}
	}
	if err := CheckGit(ctx, roots.Files, snapshot.Git); err != nil {
		return err
	}
	if err := ApplyGit(ctx, roots.Files, snapshot.Git); err != nil {
		return err
	}
	for _, change := range snapshot.Changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkChange(ctx, roots, change); err != nil {
			return err
		}
		root, rel, _ := rootFor(roots, change.Path)
		dir, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		if change.After == nil {
			err = dir.Remove(rel)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = dir.MkdirAll(path.Dir(rel), 0770)
			if err == nil {
				mode := os.FileMode(0660)
				if change.After.Executable {
					mode = 0770
				}
				var f *os.File
				f, err = dir.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
				if err == nil {
					info, statErr := f.Stat()
					err = statErr
					if err == nil && runtime.GOOS != "windows" && (info.Mode()&0111 != 0) != change.After.Executable {
						targetMode := info.Mode().Perm() &^ 0111
						if change.After.Executable {
							targetMode |= 0110
						}
						err = f.Chmod(targetMode)
					}
					if err == nil {
						// Finish this validated file even if the request is canceled.
						// A partial write matches neither retry baseline; honor
						// cancellation between files, as the prior byte write did.
						err = change.copyTo(context.WithoutCancel(ctx), f, SyncLimits(ctx).FileBytes)
					}
					closeErr := f.Close()
					if err == nil {
						err = closeErr
					}
				}
			}
		}
		dir.Close()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

// CopyContent reads through the Crew root, never following a path outside it.
func CopyContent(ctx context.Context, dst io.Writer, roots crewroot.Roots, key string, expected File) error {
	root, rel, err := rootFor(roots, key)
	if err != nil {
		return err
	}
	if _, err = crewroot.ResolveContained(root, rel); err != nil {
		return err
	}
	actual, err := readFile(ctx, root, rel, dst)
	if err != nil {
		return err
	}
	if !sameFile(actual, expected) {
		return fmt.Errorf("%s changed during capture; retry sync", key)
	}
	return nil
}

// DataPath is populated only by local capture or the verified bundle decoder;
// it is never accepted from JSON metadata.
func (c Change) CopyTo(ctx context.Context, dst io.Writer) error {
	return c.copyTo(ctx, dst, SyncLimits(ctx).FileBytes)
}

func (c Change) copyTo(ctx context.Context, dst io.Writer, fileLimit int64) error {
	if c.After == nil {
		return fmt.Errorf("missing project file metadata")
	}
	f, err := os.Open(c.DataPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return copyChecked(ctx, dst, f, *c.After, fileLimit)
}
