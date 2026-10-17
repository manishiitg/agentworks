//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package publication

import (
	"fmt"
	"os"
)

func publicationMultipleLinks(_ *os.File, _ os.FileInfo) (bool, error) {
	return false, fmt.Errorf("file links cannot be verified on this computer")
}
