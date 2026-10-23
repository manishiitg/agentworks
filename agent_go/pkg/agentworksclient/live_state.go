package agentworksclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func privateLiveDir(path string) error {
	_, existed := os.Lstat(path)
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if existed != nil { // created now: restrict it before anything is put inside
		if err := protectPrivateDir(path); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("live state must be a private directory, not a symlink")
	}
	if CheckPrivateAccess(path, info) != nil {
		// A folder left by an older run (or one whose access list was loosened) is the person's own:
		// make it private again. Where that is not possible, name the folder instead of failing blind.
		if protectPrivateDir(path) != nil || CheckPrivateAccess(path, info) != nil {
			return fmt.Errorf("the AgentWorks live folder %s is not private to your account; delete that folder (AgentWorks recreates it) and try again", path)
		}
	}
	return nil
}

func atomicLiveJSON(path string, value any) error {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || CheckPrivateAccess(path, info) != nil) {
		return errors.New("refusing non-private or redirected live state")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > 32<<20 {
		return errors.New("local state exceeds 32 MiB")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".live-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = SecurePrivateFile(f); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = syncJournalFile(f)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return syncJournalDir(dir)
}

func readLiveJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || CheckPrivateAccess(path, info) != nil || info.Size() > 32<<20 {
		return errors.New("invalid private live state")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}

func liveHash(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}
