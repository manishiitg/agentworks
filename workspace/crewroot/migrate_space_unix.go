//go:build linux || darwin

package crewroot

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io/fs"
	"os"
	"path/filepath"
)

// PLAT-856-383: reserve enough free space for both the backup and destination
// before moving a record. Conservatively require both copies on either volume.
func checkMigrationSpace(docs, state string, entries []privateMove) error {
	var bytes uint64
	for i, entry := range entries {
		before := bytes
		if err := filepath.WalkDir(entry.Source, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("%w: %s", errUncopyablePrivateEntry, path)
			}
			bytes += 4096
			if info.Mode().IsRegular() {
				// stat/list permission does not imply file-read permission.
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				if err := f.Close(); err != nil {
					return err
				}
				bytes += uint64(info.Size())
			}
			return nil
		}); err != nil {
			if isPrivateCache(entry.Source) && (errors.Is(err, os.ErrPermission) || errors.Is(err, errUncopyablePrivateEntry)) {
				// These are personal tool caches, not platform records. Move
				// the intact tree by rename; all actual records must be backed up.
				entries[i].UnbackedCache = true
				bytes = before
				continue
			}
			return err
		}
	}
	required := bytes*2 + 16*1024*1024
	for _, path := range []string{docs, state} {
		var stat unix.Statfs_t
		if err := unix.Statfs(path, &stat); err != nil {
			return err
		}
		available := uint64(stat.Bavail) * uint64(stat.Bsize)
		if available < required {
			return fmt.Errorf("Crew storage conversion needs %d bytes free for its backup and copied records; %s has %d bytes free", required, path, available)
		}
	}
	return nil
}
