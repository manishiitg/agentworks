package knowledgebase

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Brain's notes are plain files in its live folder, which is a normal Git working folder: the Brain chat edits them
// directly and runs git there, like every other product (owner, 2026-10-06: "agent should get raw access to file
// system and it should use git normally"). Brain picks up what changed on disk before it answers a call, so its
// records (IDs, versions, folders, deletions) follow the files: new files become notes, edited files get a new version,
// removed files are recorded as deletions, and new directories become folders. Dot-files (.git, .gitignore,
// .kb-registry.json), names Brain does not accept and programs stay on disk but are not notes.

type diskState struct {
	mu        sync.Mutex
	signature string
}

// SyncDisk picks up changes made directly in Brain's folder (a shell, an editor, git pull), recorded as made by actor.
func (s *Service) SyncDisk(ctx context.Context, actor string) error {
	unlock, err := s.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.recover(); err != nil {
		return err
	}
	return s.syncDiskLocked(actor)
}

// syncDiskLocked runs with the content lock held.
func (s *Service) syncDiskLocked(actor string) error {
	sig, err := s.diskSignature()
	if err != nil {
		return err
	}
	s.disk.mu.Lock()
	unchanged := sig == s.disk.signature
	s.disk.mu.Unlock()
	if unchanged {
		return nil
	}
	files, err := s.diskFiles()
	if err != nil {
		return err
	}
	regs, err := s.registries()
	if err != nil {
		return err
	}
	if !diskDiffers(regs, files) {
		s.rememberDisk(sig)
		return nil
	}
	if strings.TrimSpace(actor) == "" {
		actor = "disk"
	}
	changes, err := s.gitImportChanges(Principal{IdentityID: actor}, regs, files)
	if err != nil {
		return err
	}
	if err := s.transact(changes); err != nil {
		return err
	}
	s.recordHistory(actor, "Edits in Brain's folder")
	after, err := s.diskSignature()
	if err != nil {
		return err
	}
	s.rememberDisk(after)
	return nil
}

func (s *Service) rememberDisk(sig string) {
	s.disk.mu.Lock()
	s.disk.signature = sig
	s.disk.mu.Unlock()
}

// noteOwnWrite records Brain's own writes so the next call does not re-read every file for them.
func (s *Service) noteOwnWrite() {
	if sig, err := s.diskSignature(); err == nil {
		s.rememberDisk(sig)
	}
}

// diskNote reports whether a live-folder path (slash-separated, relative) can be a note or folder.
func diskNote(rel string, dir bool) bool {
	for i, part := range strings.Split(rel, "/") {
		last := i == strings.Count(rel, "/")
		if strings.HasPrefix(part, ".") || !validName(part, last && !dir) {
			return false
		}
	}
	return true
}

// diskSignature is cheap: names, sizes and modification times of the files that can be notes.
func (s *Service) diskSignature() (string, error) {
	var parts []string
	err := filepath.WalkDir(s.live, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(s.live, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if !diskNote(rel, d.IsDir()) || d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			parts = append(parts, rel+"/")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		parts = append(parts, rel+"|"+strconv.FormatInt(info.Size(), 10)+"|"+strconv.FormatInt(info.ModTime().UnixNano(), 10))
		return nil
	})
	sort.Strings(parts)
	return digest([]byte(strings.Join(parts, "\n"))), err
}

// diskFiles reads every file that can be a note; programs and files over the size limit are left out.
func (s *Service) diskFiles() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(s.live, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(s.live, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if !diskNote(rel, d.IsDir()) || d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileBytes {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if isExecutable(b) {
			return nil
		}
		files[rel] = b
		return nil
	})
	return files, err
}

// diskDiffers reports whether the files differ from Brain's records (a note added, changed or removed).
func diskDiffers(regs []folderRegistry, files map[string][]byte) bool {
	known := 0
	for _, r := range regs {
		for _, e := range r.Entries {
			b, ok := files[e.Path]
			if !ok || digest(b) != e.Fingerprint {
				return true
			}
			known++
		}
	}
	if known != len(files) {
		return true
	}
	// A new empty directory becomes a folder.
	folders := map[string]bool{}
	for _, r := range regs {
		folders[r.Path] = true
	}
	for name := range files {
		for dir := path.Dir(name); dir != "." && dir != ""; dir = path.Dir(dir) {
			if !folders[dir] {
				return true
			}
		}
	}
	return false
}
