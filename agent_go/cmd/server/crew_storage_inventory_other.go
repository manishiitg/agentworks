//go:build !linux

package server

import (
	"fmt"
	"io/fs"
)

func crewStorageMetadata(_ string, _ fs.FileInfo) (map[string]any, error) {
	return nil, fmt.Errorf("storage migration needs the Linux server")
}

func crewStorageFreeBytes(_ string) (uint64, error) {
	return 0, fmt.Errorf("storage migration needs the Linux server")
}
