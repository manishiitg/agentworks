package server

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Removing a binding never removes the selected folder. Retain a plain private
// backup of its Crew resources before deleting only our .agentworks directory.
func removeLocalCrew(folder string) error {
	roots, bound, err := localCrewRemovalRoots(folder)
	if err != nil {
		return err
	}
	if !bound {
		return fmt.Errorf("local Crew binding is unavailable")
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(state, "crew-removals", roots.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, time.Now().UTC().Format("20060102T150405.000000000")+".tar.gz"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	copyTree := func(base, prefix string) error {
		if _, err := os.Lstat(base); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		return filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(p)
				if err != nil {
					return err
				}
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(filepath.Join(prefix, rel))
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			return closeErr
		})
	}
	err = copyTree(roots.Resources, "resources")
	if err == nil {
		err = copyTree(roots.Private, "settings")
	}
	for _, close := range []func() error{tw.Close, gz.Close, file.Close} {
		if e := close(); err == nil {
			err = e
		}
	}
	if err != nil {
		return fmt.Errorf("back up Crew before removal: %w", err)
	}
	if err := os.RemoveAll(roots.Resources); err != nil {
		return err
	}
	return os.RemoveAll(roots.Private)
}
