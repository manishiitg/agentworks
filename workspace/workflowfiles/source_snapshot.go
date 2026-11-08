package workflowfiles

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/filemetadata"
)

// MaterializeSourceSnapshot links immutable source bytes into one run. The
// trusted runtime root supplies the existing slot group, including when an old
// cache lost its setgid bit. Local roots remain private. Callers admit source
// filenames before entering this filesystem writer.
func MaterializeSourceSnapshot(ctx context.Context, root *os.Root, cachePath, outputPath string, files map[string][]byte) error {
	dirMode, fileMode, err := ServerFileModes(root)
	if err != nil {
		return err
	}
	fileMode &^= 0222
	policy, err := root.Open(".")
	if err != nil {
		return err
	}
	defer policy.Close()
	prepareDirectory := func(path string) (*os.Root, error) {
		dir, err := OpenDirectory(root, path, true)
		if err != nil {
			return nil, err
		}
		file, err := dir.Open(".")
		if err == nil {
			err = filemetadata.InheritGroup(file, policy)
			file.Close()
		}
		if err == nil {
			err = dir.Chmod(".", dirMode)
		}
		if err != nil {
			dir.Close()
			return nil, err
		}
		return dir, nil
	}
	cache, err := prepareDirectory(cachePath)
	if err != nil {
		return err
	}
	defer cache.Close()
	output, err := prepareDirectory(outputPath)
	if err != nil {
		return err
	}
	defer output.Close()
	for name, data := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean, err := CleanRelative(name)
		if err != nil || clean == "." {
			return fmt.Errorf("invalid source snapshot filename")
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		if info, err := cache.Lstat(hash); os.IsNotExist(err) {
			// Create inside the repaired setgid cache. The service umask cannot
			// strip required group read: the final mode is applied explicitly.
			temporary := ".new-" + rand.Text()
			file, err := cache.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, writeErr := file.Write(data)
			if writeErr == nil {
				writeErr = file.Chmod(fileMode)
			}
			closeErr := file.Close()
			if writeErr == nil && closeErr == nil {
				err = cache.Link(temporary, hash)
			}
			_ = cache.Remove(temporary)
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
			if err != nil && !os.IsExist(err) {
				return err
			}
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
			return fmt.Errorf("invalid immutable script snapshot")
		}
		// Reused blobs may predate the slot-group fix. Repair that same inode,
		// so its already-linked run copies become readable too, without copies.
		blob, err := cache.Open(hash)
		if err != nil {
			return err
		}
		err = filemetadata.InheritGroup(blob, policy)
		if err == nil {
			err = blob.Chmod(fileMode)
		}
		blob.Close()
		if err != nil {
			return err
		}
		parent := outputPath
		for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(clean)), "/") {
			if part == "." {
				continue
			}
			parent = filepath.Join(parent, part)
			dir, err := prepareDirectory(parent)
			if err != nil {
				return err
			}
			dir.Close()
		}
		target := filepath.Join(outputPath, clean)
		if err := root.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := root.Link(filepath.Join(cachePath, hash), target); err != nil {
			return err
		}
	}
	return nil
}
