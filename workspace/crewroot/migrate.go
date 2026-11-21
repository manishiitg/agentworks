package crewroot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/filemetadata"
)

var errUncopyablePrivateEntry = errors.New("non-regular filesystem entry cannot be backed up")

func copyPrivateEntry(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(link, target)
	}
	if info.IsDir() {
		if err := os.MkdirAll(target, 0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPrivateEntry(filepath.Join(source, e.Name()), filepath.Join(target, e.Name())); err != nil {
				return err
			}
		}
	} else {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s", errUncopyablePrivateEntry, source)
		}
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Open(target)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := filemetadata.Preserve(out, in); err != nil {
		return err
	}
	if info.IsDir() {
		if err := out.Chmod(info.Mode().Perm() | (info.Mode() & os.ModeSetgid)); err != nil {
			return err
		}
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}

// EnsurePrivateRoot preserves the existing Crew slot group. Only the exact
// private Crew tree is granted by the sandbox, never its shared parent.
func EnsurePrivateRoot(docs, id, workingRoot string) (string, error) {
	private, err := PrivateRoot(docs, id)
	if err != nil {
		return "", err
	}
	for _, parent := range []string{filepath.Join(docs, "_system"), filepath.Join(docs, filepath.FromSlash(PrivateDirectory))} {
		if err := os.MkdirAll(parent, 0711); err != nil {
			return "", err
		}
		info, err := os.Stat(parent)
		if err != nil {
			return "", err
		}
		if err := os.Chmod(parent, info.Mode()|0011); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(private, 0750); err != nil {
		return "", err
	}
	source, err := os.Open(workingRoot)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.Open(private)
	if err != nil {
		return "", err
	}
	defer target.Close()
	if err := filemetadata.Preserve(target, source); err != nil {
		return "", err
	}
	if err := target.Chmod(0750 | os.ModeSetgid); err != nil {
		return "", err
	}
	return private, nil
}
