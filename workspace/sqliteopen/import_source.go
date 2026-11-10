package sqliteopen

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

var ErrImportSourceChanged = errors.New("imported source differs from its private backup; keeping SQL authoritative and both files unchanged")

// RetireImportedJSON runs after the importing transaction commits. Across
// volumes it publishes a complete private backup before removing the source;
// the caller uses its committed marker to resume cleanup, never replay import.
func RetireImportedJSON(sourcePath, backupPath string) error {
	if _, err := os.Lstat(sourcePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if backup, err := os.ReadFile(backupPath); err == nil {
		source, e := os.ReadFile(sourcePath)
		if e != nil {
			return e
		}
		if !bytes.Equal(backup, source) {
			return ErrImportSourceChanged
		}
		return os.Remove(sourcePath)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(sourcePath, backupPath); err == nil {
		return os.Chmod(backupPath, 0600)
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temp, err := os.CreateTemp(filepath.Dir(backupPath), "history-backup-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = io.Copy(temp, source)
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), backupPath); err != nil {
		return err
	}
	return os.Remove(sourcePath)
}
