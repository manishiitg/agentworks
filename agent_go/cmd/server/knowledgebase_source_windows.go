//go:build windows

package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// knowledgeReadSource reads one migration source file inside root. os.Root
// refuses any path or link that leads outside root; a link as the file itself
// is refused too, matching the Unix rule of never following one.
func knowledgeReadSource(root, relative string) ([]byte, error) {
	if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(filepath.ToSlash(relative), "../") {
		return nil, fmt.Errorf("invalid source-relative path")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("source root must be canonical and absolute")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if info, err := r.Lstat(relative); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source must be a regular file")
	}
	f, err := r.Open(relative)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 10*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 10*1024*1024 {
		return nil, fmt.Errorf("source file exceeds 10 MiB")
	}
	return b, nil
}
