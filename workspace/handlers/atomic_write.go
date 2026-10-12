package handlers

import (
	"github.com/manishiitg/coding-agent-loop/workspace/filemetadata"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to a temp file in the same directory and renames
// it over path, so a crash or dropped connection never leaves a half-written
// document. An existing file's permissions are kept. A symlink at path is
// written through (its target is replaced), never swapped for a regular file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	original, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if original != nil {
		defer original.Close()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if original != nil {
		err = filemetadata.Preserve(tmp, original)
	} else {
		err = tmp.Chmod(perm)
	}
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// createFileAtomic publishes a complete new file without replacing an existing
// file, directory or symlink, including one created by a writer outside our lock.
func createFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmp.Name(), path)
}
