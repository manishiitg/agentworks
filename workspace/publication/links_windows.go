//go:build windows

package publication

import (
	"os"
	"syscall"
)

func publicationMultipleLinks(file *os.File, _ os.FileInfo) (bool, error) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return false, err
	}
	return info.NumberOfLinks > 1, nil
}
