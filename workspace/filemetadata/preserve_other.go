//go:build !linux && !darwin

package filemetadata

import "os"

func Preserve(dst, src *os.File) error {
	info, err := src.Stat()
	if err != nil {
		return err
	}
	return dst.Chmod(info.Mode())
}
